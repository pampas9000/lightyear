package response

import "github.com/gofiber/fiber/v3"

// Response is the unified API response envelope for all HTTP handlers.
//
// Notes:
//   - success responses should use a stable "ok.*" code
//   - error responses should use a stable "error.*" code
//   - Data is always present in the JSON payload to keep the shape consistent
//   - Details is optional and should only be used for machine-readable error context
type Response[T any] struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
	//
	Details any `json:"details,omitempty"`
}

// SuccessResponse builds a success envelope with data.
func SuccessResponse[T any](code ResponseCode, message string, data T) Response[T] {
	return Response[T]{
		Success: true,
		Code:    normalizeResponseCode(code, CodeOK),
		Message: message,
		Data:    data,
	}
}

// EmptySuccessResponse builds a success envelope with a null data payload.
func EmptySuccessResponse(code ResponseCode, message string) Response[any] {
	return Response[any]{
		Success: true,
		Code:    normalizeResponseCode(code, CodeOK),
		Message: message,
		Data:    nil,
	}
}

// ErrorResponse builds an error envelope with a null data payload.
func ErrorResponse(code ResponseCode, message string) Response[any] {
	return Response[any]{
		Success: false,
		Code:    normalizeResponseCode(code, CodeInternal),
		Message: message,
		Data:    nil,
	}
}

// ErrorResponseWithDetails builds an error envelope with optional machine-readable details.
func ErrorResponseWithDetails(code ResponseCode, message string, details any) Response[any] {
	return Response[any]{
		Success: false,
		Code:    normalizeResponseCode(code, CodeInternal),
		Message: message,
		Data:    nil,
		Details: details,
	}
}

// RespondSuccess writes a success response envelope.
func RespondSuccess[T any](c fiber.Ctx, status int, code ResponseCode, message string, data T) error {
	return c.Status(status).JSON(SuccessResponse(code, message, data))
}

// RespondEmptySuccess writes a success response envelope with a null data payload.
func RespondEmptySuccess(c fiber.Ctx, status int, code ResponseCode, message string) error {
	return c.Status(status).JSON(EmptySuccessResponse(code, message))
}

// RespondError writes an error response envelope.
func RespondError(c fiber.Ctx, status int, code ResponseCode, message string) error {
	return c.Status(status).JSON(ErrorResponse(code, message))
}

// RespondErrorWithDetails writes an error response envelope with machine-readable details.
func RespondErrorWithDetails(c fiber.Ctx, status int, code ResponseCode, message string, details any) error {
	return c.Status(status).JSON(ErrorResponseWithDetails(code, message, details))
}

func normalizeResponseCode(code ResponseCode, fallback ResponseCode) string {
	if code == "" {
		return fallback.String()
	}
	return code.String()
}
