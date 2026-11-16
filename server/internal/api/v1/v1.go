// Package v1 is the public, API-key authenticated HTTP API mounted at /v1.
// It reuses the task and upload services of the web console's /api but has
// its own routes, representations and error codes.
package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"transcoder/server/internal/api/middleware"
	"transcoder/server/internal/api/response"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/services/upload"

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
	sign    Signer
	now     func() time.Time
}

func New(tasks *task.Service, uploads *upload.Service, sign Signer) *Handler {
	return &Handler{tasks: tasks, uploads: uploads, sign: sign, now: time.Now}
}

// Install mounts /v1 on app, authenticated by API key.
func (h *Handler) Install(app fiber.Router, keys *apikey.Service) {
	v1 := app.Group("/v1", middleware.APIKeyAuth(keys))
	read := middleware.RequireScope(apikey.ScopeTasksRead)
	write := middleware.RequireScope(apikey.ScopeTasksWrite)

	v1.Post("/uploads", write, h.CreateUpload)
	v1.Post("/uploads/:id/complete", write, h.CompleteUpload)
	v1.Get("/uploads/:id/parts", write, h.ListParts)
	v1.Post("/uploads/:id/parts/:part_number/url", write, h.SignPart)
	v1.Post("/uploads/:id/abort", write, h.AbortUpload)

	v1.Post("/tasks", write, h.CreateTask)
	v1.Get("/tasks", read, h.ListTasks)
	v1.Get("/tasks/:id", read, h.GetTask)
	v1.Post("/tasks/:id/cancel", write, h.CancelTask)
	v1.Post("/tasks/:id/retry", write, h.RetryTask)
}

func owner(c fiber.Ctx) uuid.UUID {
	id, _ := uuid.Parse(fmt.Sprint(c.Locals("user_id")))
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
	return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal,
		"Failed to "+action+".", map[string]any{"reason": err.Error()})
}
