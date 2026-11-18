package v1

import (
	"errors"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/services/upload"
	"transcoder/server/internal/services/usage"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Error is a /v1 client error before it is written. The MCP server reports
// the same Code, Message and Details as a tool error, so both entry points
// classify service errors here.
type Error struct {
	Status  int
	Code    response.ResponseCode
	Message string
	Details any
}

func (e *Error) respond(c fiber.Ctx) error {
	if e.Details == nil {
		return response.RespondError(c, e.Status, e.Code, e.Message)
	}
	return response.RespondErrorWithDetails(c, e.Status, e.Code, e.Message, e.Details)
}

func newError(status int, code response.ResponseCode, message string, details any) *Error {
	return &Error{Status: status, Code: code, Message: message, Details: details}
}

// quotaError classifies usage.ErrQuotaExceeded; nil for other errors.
func quotaError(err error) *Error {
	if !errors.Is(err, usage.ErrQuotaExceeded) {
		return nil
	}
	message, detail, ok := response.QuotaExceeded(err)
	if !ok {
		return nil
	}
	return newError(fiber.StatusForbidden, response.CodeQuotaExceeded, message, detail)
}

// TaskError classifies an error from the task and lifecycle services. It
// returns nil for errors that are not the client's: those are internal.
func TaskError(err error) *Error {
	switch {
	case errors.Is(err, task.ErrNoItemsProvided):
		return newError(fiber.StatusBadRequest, response.CodeParamRequired, "items are required.", response.ValidationDetail{Field: "items", Reason: "empty"})
	case errors.Is(err, task.ErrItemSource):
		return newError(fiber.StatusBadRequest, response.CodeParamRequired, err.Error(), response.ValidationDetail{Field: "items", Reason: "exactly one of file_id or url is required"})
	case errors.Is(err, task.ErrInvalidURL):
		return newError(fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: "items", Reason: "invalid url"})
	case errors.Is(err, task.ErrFileNotFound):
		return newError(fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: "items", Reason: "file not found or not uploaded"})
	case errors.Is(err, task.ErrWorkflowNotFound):
		return newError(fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: "workflow_id", Reason: "not found"})
	case errors.Is(err, task.ErrIdempotencyKey):
		return newError(fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: HeaderIdempotencyKey, Reason: "too long"})
	case errors.Is(err, imageoptions.ErrInvalid),
		errors.Is(err, compression.ErrInvalidTarget),
		errors.Is(err, compression.ErrInvalidOutputs),
		errors.Is(err, compression.ErrTooManyOutputs),
		errors.Is(err, compression.ErrTooManyJobs),
		errors.Is(err, task.ErrOptionsRequired),
		errors.Is(err, task.ErrTargetNotAllowed),
		errors.Is(err, task.ErrTooManyItems),
		errors.Is(err, task.ErrRetrySingleJob),
		errors.Is(err, task.ErrInvalidJobID),
		errors.Is(err, task.ErrNoJobsToRetry):
		return newError(fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), nil)
	case errors.Is(err, usage.ErrQuotaExceeded):
		return quotaError(err)
	case errors.Is(err, task.ErrIdempotencyConflict):
		return newError(fiber.StatusConflict, response.CodeIdempotencyConflict,
			"Idempotency-Key was already used with a different request.", nil)
	case errors.Is(err, task.ErrJobNotRetryable), errors.Is(err, lifecycle.ErrTaskActive):
		return newError(fiber.StatusConflict, response.CodeTaskConflict, err.Error(), nil)
	case errors.Is(err, task.ErrSourceUnavailable):
		return newError(fiber.StatusConflict, response.CodeFileGone, err.Error(), nil)
	case errors.Is(err, task.ErrTaskNotFound), errors.Is(err, lifecycle.ErrTaskNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return newError(fiber.StatusNotFound, response.CodeTaskNotFound, "Task not found.", nil)
	}
	return nil
}

// UploadError classifies an error from the upload service; nil means internal.
func UploadError(err error) *Error {
	switch {
	case errors.Is(err, usage.ErrQuotaExceeded):
		return quotaError(err)
	case errors.Is(err, upload.ErrInvalidSize):
		return newError(fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: "size", Reason: "must be positive"})
	case errors.Is(err, upload.ErrInvalidHash):
		return newError(fiber.StatusBadRequest, response.CodeParamInvalid, "sha256 must be a hex SHA-256.", response.ValidationDetail{Field: "sha256", Reason: "must be 64 hex characters"})
	case errors.Is(err, upload.ErrInvalidPart):
		return newError(fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: "part_number", Reason: "out of range"})
	case errors.Is(err, upload.ErrNotFound), errors.Is(err, upload.ErrForbidden):
		return newError(fiber.StatusNotFound, response.CodeFileNotFound, "Upload not found.", nil)
	case errors.Is(err, upload.ErrNotMultipart), errors.Is(err, upload.ErrAlreadyUploaded), errors.Is(err, upload.ErrObjectMissing):
		return newError(fiber.StatusConflict, response.CodeParamInvalid, err.Error(), nil)
	}
	return nil
}

// respondTaskError maps task service errors to /v1 responses.
func respondTaskError(c fiber.Ctx, err error, action string) error {
	if e := TaskError(err); e != nil {
		return e.respond(c)
	}
	return internalError(c, action, err)
}

func respondUploadError(c fiber.Ctx, err error, action string) error {
	if errors.Is(err, errBadUploadID) {
		return badID(c, "id")
	}
	if e := UploadError(err); e != nil {
		return e.respond(c)
	}
	return internalError(c, action, err)
}
