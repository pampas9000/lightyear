// Package mcpserver is the remote MCP server mounted at /mcp: a second entry
// point next to /v1 for agents. It authenticates with the same API keys and
// scopes, and its three tools call the same task and upload services, rate
// limiter and quotas as /v1, returning the /v1 representations as
// structured content. Errors are tool results carrying the /v1 error code
// and details.
//
// The transport is stateless Streamable HTTP with plain JSON responses: no
// sessions, no server-sent events. get_task waits by polling instead.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"transcoder/server/internal/api/middleware"
	v1 "transcoder/server/internal/api/v1"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/services/upload"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// Path is where the MCP endpoint is mounted.
	Path = "/mcp"
	// MaxWaitSeconds caps get_task's wait_seconds, below the tool-call
	// timeouts of common clients.
	MaxWaitSeconds = 30
	// pollInterval is how often get_task re-reads a task while waiting.
	pollInterval = time.Second
	// apiKeyExtra is the TokenInfo.Extra entry holding the *models.APIKey.
	apiKeyExtra = "api_key"
)

const instructions = `lightyear converts and compresses images asynchronously.

1. Get a source: either pass a public http(s) URL to submit_task directly, or call create_upload and send the file's bytes to the returned URL yourself (with an HTTP client such as curl, not through a tool argument).
2. Call submit_task with the sources and the output options (format is required; target.max_bytes asks for the smallest file under a size).
3. Call get_task with wait_seconds until status is COMPLETED, PARTIALLY_FAILED, FAILED or CANCELLED. Each completed job has output_file.download_url (valid for 1 hour) and compression statistics.

Image bytes never pass through tool arguments or results.`

// Server serves /mcp.
type Server struct {
	tasks   *task.Service
	uploads *upload.Service
	keys    *apikey.Service
	sign    v1.Signer
	limiter middleware.Limiter
	now     func() time.Time
	poll    time.Duration
	mcp     *sdk.Server
}

// New builds the MCP server. limiter may be nil (no rate limit); it should be
// the one /v1 uses, so both entry points share each user's bucket.
func New(tasks *task.Service, uploads *upload.Service, keys *apikey.Service, sign v1.Signer, limiter middleware.Limiter) (*Server, error) {
	s := &Server{
		tasks: tasks, uploads: uploads, keys: keys, sign: sign, limiter: limiter,
		now: time.Now, poll: pollInterval,
	}
	s.mcp = sdk.NewServer(&sdk.Implementation{Name: "lightyear", Title: "lightyear", Version: "1.0.0"},
		&sdk.ServerOptions{Instructions: instructions})
	if err := s.addTools(); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler is the net/http handler for /mcp, authentication included.
func (s *Server) Handler() http.Handler {
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.mcp }, &sdk.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// Every request carries an API key, so a page reaching the server
		// through DNS rebinding gets nowhere without one; and behind a
		// reverse proxy on the same host the check would reject every
		// request (local address, public Host).
		DisableLocalhostProtection: true,
	})
	bearer := auth.RequireBearerToken(s.verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})
	return withRequestID(challenge(bearer(h)))
}

// Install mounts /mcp on app. The request ID of the requestid middleware is
// handed to the net/http handler as the X-Request-ID request header, so that
// internal errors report the ID the response carries.
func (s *Server) Install(app fiber.Router) {
	h := adaptor.HTTPHandler(s.Handler())
	app.All(Path, func(c fiber.Ctx) error {
		if rid := requestid.FromContext(c); rid != "" {
			c.Request().Header.Set(fiber.HeaderXRequestID, rid)
		}
		return h(c)
	})
}

type requestIDKey struct{}

// withRequestID puts the X-Request-ID request header into the context, where
// tool handlers find it: the SDK runs them under the request's context.
func withRequestID(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rid := r.Header.Get(fiber.HeaderXRequestID); rid != "" {
			r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, rid))
		}
		h.ServeHTTP(w, r)
	})
}

func requestID(ctx context.Context) string {
	rid, _ := ctx.Value(requestIDKey{}).(string)
	return rid
}

// verify authenticates an API key the way /v1 does.
func (s *Server) verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	key, err := s.keys.Authenticate(ctx, token)
	if errors.Is(err, apikey.ErrInvalidKey) {
		return nil, fmt.Errorf("%w: invalid or revoked API key", auth.ErrInvalidToken)
	}
	if err != nil {
		// The SDK writes the error text into the response; keep it generic.
		slog.ErrorContext(ctx, "verify API key", "error", err)
		return nil, errors.New("failed to verify API key")
	}
	return &auth.TokenInfo{
		Scopes: key.Scopes,
		UserID: key.OwnerID.String(),
		Extra:  map[string]any{apiKeyExtra: key},
	}, nil
}

// challenge adds the WWW-Authenticate header of /v1 to 401 responses.
func challenge(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&challengeWriter{ResponseWriter: w}, r)
	})
}

type challengeWriter struct{ http.ResponseWriter }

func (w *challengeWriter) WriteHeader(code int) {
	if code == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lightyear"`)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *challengeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// caller is the authenticated API key of a tool call.
func caller(req *sdk.CallToolRequest) *models.APIKey {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return nil
	}
	key, _ := req.Extra.TokenInfo.Extra[apiKeyExtra].(*models.APIKey)
	return key
}
