package api

import (
	"context"
	"fmt"
	"time"

	"transcoder/server/internal/api/handlers"
	"transcoder/server/internal/api/mcpserver"
	"transcoder/server/internal/api/middleware"
	v1 "transcoder/server/internal/api/v1"
	"transcoder/server/internal/config"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/ratelimit"
	"transcoder/server/internal/services/storage"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/services/upload"
	"transcoder/server/internal/services/usage"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/session"
	fiber_redis "github.com/gofiber/storage/redis/v3"
	"github.com/redis/go-redis/v9"
)

// Router composes feature-specific API handlers and mounts them under /api
// (web console, session auth) and /v1 (public API, API key auth).
type Router struct {
	job      *handlers.Handler
	workflow *handlers.WorkflowHandler
	task     *handlers.TaskHandler
	upload   *handlers.UploadHandler
	draft    *handlers.DraftHandler
	file     *handlers.FileHandler
	auth     *handlers.AuthHandler
	apiKeys  *handlers.APIKeyHandler
	usage    *handlers.UsageHandler
	session  *session.Store
	keys     *apikey.Service
	v1       *v1.Handler
	mcp      *mcpserver.Server
	// origin is FRONTEND_URL as scheme://host[:port], the only Origin
	// accepted for state-changing /api requests.
	origin string
}

// NewRouter wires feature handlers with the dependencies they need.
func NewRouter(db *gorm.DB, kv *redis.Client, cfg config.Config, s3Storage *storage.S3Service) *Router {
	store := session.NewStore(session.Config{
		Storage:        fiber_redis.NewFromConnection(kv),
		CookieHTTPOnly: true,
		CookiePath:     "/",
		CookieSecure:   cfg.IsProduction(),
		CookieSameSite: "Lax",
		IdleTimeout:    7 * 24 * time.Hour,
	})

	quotas := usage.NewQuotas(db, usage.Limits{
		StorageBytes: cfg.Quota.StorageBytes, MonthlyUnits: cfg.Quota.MonthlyUnits,
	})
	limiter := ratelimit.New(kv, "rl:v1:write:", cfg.RateLimit.PerMinute, cfg.RateLimit.Burst)
	tasks := task.NewService(db, kv).WithQuotas(quotas)
	uploads := upload.NewService(db, s3Storage, cfg.S3.Bucket, RetentionPolicy(cfg)).WithQuotas(quotas)
	keys := apikey.NewService(db)
	files := lifecycle.NewService(db, s3Storage, cfg.S3.Bucket, RetentionPolicy(cfg))
	sign := func(ctx context.Context, path, filename string, ttl time.Duration) (string, error) {
		return s3Storage.GetPresignedURL(ctx, cfg.S3.Bucket, path, filename, false, ttl)
	}

	// The MCP server is static configuration (tools and embedded schemas);
	// failing to build it is a programming error.
	mcp, err := mcpserver.New(tasks, uploads, keys, sign, limiter)
	if err != nil {
		panic(fmt.Sprintf("build MCP server: %v", err))
	}

	origin, err := middleware.NormalizeOrigin(cfg.FrontendURL)
	if err != nil {
		panic(fmt.Sprintf("FRONTEND_URL: %v", err))
	}

	return &Router{
		origin:   origin,
		job:      handlers.NewHandler(db),
		workflow: handlers.NewWorkflowHandler(db),
		task:     handlers.NewTaskHandler(tasks, files),
		upload:   handlers.NewUploadHandler(uploads),
		draft:    handlers.NewDraftHandler(db, s3Storage, cfg.S3.Bucket),
		file:     handlers.NewFileHandler(db, s3Storage, cfg.S3.Bucket, files),
		auth:     handlers.NewAuthHandler(db, store, cfg),
		apiKeys:  handlers.NewAPIKeyHandler(keys),
		usage:    handlers.NewUsageHandler(quotas),
		session:  store,
		keys:     keys,
		v1:       v1.New(tasks, uploads, files, sign).WithUsage(quotas, limiter),
		mcp:      mcp,
	}
}

// RetentionPolicy is the global storage retention from cfg.
func RetentionPolicy(cfg config.Config) lifecycle.Policy {
	return lifecycle.Days(cfg.Retention.SourceDays, cfg.Retention.OutputDays)
}

// Install mounts all API modules.
//
// Keep this root router focused on composition only.
// Feature-specific routing should live in subpackages such as
// `internal/api/handlers`.
func (r *Router) Install(app fiber.Router) {
	// Every state-changing /api request, login and logout included, must
	// come from the frontend's origin. /v1 and /mcp use API keys instead.
	api := app.Group("/api", middleware.RequireOrigin(r.origin))

	// Public routes
	r.auth.Install(api)

	// Protected routes
	protected := api.Group("/", middleware.AuthMiddleware(r.session))
	protected.Get("/me", r.auth.Me)
	r.job.Install(protected)
	r.workflow.Install(protected)
	r.task.Install(protected)
	r.upload.Install(protected)
	r.draft.Install(protected)
	r.file.Install(protected)
	r.apiKeys.Install(protected)
	r.usage.Install(protected)

	// Public API: its own routes and representations, API key auth only.
	r.v1.Install(app, r.keys)

	// MCP for agents: same API keys, services and limits as /v1.
	r.mcp.Install(app)
}
