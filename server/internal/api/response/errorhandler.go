package response

import (
	"errors"

	"github.com/gofiber/fiber/v3"
)

// ErrorHandler is the app's fiber.Config.ErrorHandler: errors that reach the
// framework (unknown routes, oversized bodies, recovered panics, a handler
// returning an error instead of writing a response) are answered with the
// envelope too. A *fiber.Error keeps its status; anything else is a 500 whose
// cause stays in the log (RespondInternal).
func ErrorHandler(c fiber.Ctx, err error) error {
	var fe *fiber.Error
	if !errors.As(err, &fe) {
		return RespondInternal(c, "Internal server error.", err)
	}
	switch status := fe.Code; {
	case status == fiber.StatusNotFound:
		return RespondError(c, status, CodeRouteNotFound, "Route not found.")
	case status == fiber.StatusMethodNotAllowed:
		return RespondError(c, status, CodeMethodNotAllowed, "Method not allowed.")
	case status == fiber.StatusRequestEntityTooLarge:
		return RespondError(c, status, CodeBodyTooLarge, "Request body too large.")
	case status == fiber.StatusUnauthorized:
		return RespondError(c, status, CodeUnauthorized, fe.Message)
	case status == fiber.StatusForbidden:
		return RespondError(c, status, CodeForbidden, fe.Message)
	case status == fiber.StatusTooManyRequests:
		return RespondError(c, status, CodeRateLimited, fe.Message)
	case status >= 400 && status < 500:
		return RespondError(c, status, CodeRequestInvalid, fe.Message)
	}
	return RespondInternal(c, "Internal server error.", err)
}
