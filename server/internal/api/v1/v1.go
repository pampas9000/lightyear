// Package v1 is the public, API-key authenticated HTTP API mounted at /v1.
// It reuses the task and upload services of the web console's /api but has
// its own routes, representations and error codes.
package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"transcoder/server/internal/api/middleware"
	"transcoder/server/internal/api/response"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/services/upload"
	"transcoder/server/internal/services/usage"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const (
	// DownloadURLTTL is how long the download URLs in task responses stay valid.
	DownloadURLTTL = time.Hour
	// etagWindow bounds how old a cached copy answered with 304 can be; see taskETag.
	etagWindow = 30 * time.Minute
	// retryAfterSeconds is the suggested polling interval for unfinished tasks.
	retryAfterSeconds = 2
	// HeaderIdempotencyKey carries the client's idempotency key on POST /v1/tasks.
	HeaderIdempotencyKey = "Idempotency-Key"
	// HeaderIdempotentReplayed is "true" when the response replays an earlier request.
	HeaderIdempotentReplayed = "Idempotent-Replayed"
)

type Handler struct {
	tasks   *task.Service
	uploads *upload.Service
	files   *lifecycle.Service
	sign    Signer
	quotas  *usage.Quotas
	limiter middleware.Limiter
	now     func() time.Time
}

// WithUsage serves GET /v1/usage from quotas and rate limits write requests
// with limiter (nil: no limit).
func (h *Handler) WithUsage(quotas *usage.Quotas, limiter middleware.Limiter) *Handler {
	h.quotas, h.limiter = quotas, limiter
	return h
}

func New(tasks *task.Service, uploads *upload.Service, files *lifecycle.Service, sign Signer) *Handler {
	return &Handler{tasks: tasks, uploads: uploads, files: files, sign: sign, now: time.Now}
}

// Install mounts /v1 on app, authenticated by API key.
func (h *Handler) Install(app fiber.Router, keys *apikey.Service) {
	v1 := app.Group("/v1", middleware.APIKeyAuth(keys))
	read := middleware.RequireScope(apikey.ScopeTasksRead)
	write := middleware.RequireScope(apikey.ScopeTasksWrite)
	// Requests that start work or change state share one bucket per user.
	// The later steps of an upload (complete, parts, abort) belong to an
	// upload that was already counted, so they are not limited.
	limit := middleware.RateLimit(h.limiter)

	v1.Post("/uploads", write, limit, h.CreateUpload)
	v1.Post("/uploads/:id/complete", write, h.CompleteUpload)
	v1.Get("/uploads/:id/parts", write, h.ListParts)
	v1.Post("/uploads/:id/parts/:part_number/url", write, h.SignPart)
	v1.Post("/uploads/:id/abort", write, h.AbortUpload)

	v1.Post("/tasks", write, limit, h.CreateTask)
	v1.Get("/tasks", read, h.ListTasks)
	v1.Get("/tasks/:id", read, h.GetTask)
	v1.Post("/tasks/:id/cancel", write, limit, h.CancelTask)
	v1.Post("/tasks/:id/retry", write, limit, h.RetryTask)
	v1.Delete("/tasks/:id", write, limit, h.DeleteTask)

	v1.Delete("/files/:id", write, limit, h.DeleteFile)

	v1.Get("/usage", read, h.GetUsage)
}

func owner(c fiber.Ctx) uuid.UUID {
	id, _ := middleware.UserID(c)
	return id
}

var errEmptyBody = errors.New("request body is required")

// decodeJSON strictly decodes body into dst: unknown fields are errors, so
// misspelled or /api-only fields (such as idempotency_key) are not silently
// ignored. An empty body leaves dst untouched when optional is true.
func decodeJSON(body []byte, dst any, optional bool) error {
	if len(bytes.TrimSpace(body)) == 0 {
		if optional {
			return nil
		}
		return errEmptyBody
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

func badBody(c fiber.Ctx, err error) error {
	return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeBodyInvalid,
		"Invalid request body.", map[string]any{"reason": err.Error()})
}

func badID(c fiber.Ctx, param string) error {
	return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid,
		"Invalid "+param+".", response.ValidationDetail{Field: param, Reason: "must be a valid UUID"})
}

func setRetryAfter(c fiber.Ctx) {
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(retryAfterSeconds))
}

func internalError(c fiber.Ctx, action string, err error) error {
	return response.RespondInternal(c, "Failed to "+action+".", err)
}
