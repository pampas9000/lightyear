package api

import (
	"log/slog"
	"runtime/debug"

	"transcoder/server/internal/api/response"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

// NewApp builds the HTTP app with r's routes. Every response a handler or
// the framework writes is the response envelope: errors that reach Fiber
// (unknown routes, oversized bodies, panics) go through
// response.ErrorHandler.
func NewApp(r *Router) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:         "transcoder-server",
		StructValidator: &structValidator{validate: validator.New()},
		ErrorHandler:    response.ErrorHandler,
	})
	app.Use(requestid.New())
	app.Use(logger.New())
	// A panic becomes an error for ErrorHandler: a 500 envelope whose cause
	// is logged with the request ID, never sent.
	app.Use(recover.New(recover.Config{EnableStackTrace: true, StackTraceHandler: logPanic}))
	r.Install(app)
	return app
}

func logPanic(c fiber.Ctx, r any) {
	slog.ErrorContext(c.Context(), "panic", "request_id", requestid.FromContext(c), "method", c.Method(),
		"path", c.Path(), "panic", r, "stack", string(debug.Stack()))
}

type structValidator struct {
	validate *validator.Validate
}

func (v *structValidator) Validate(out any) error {
	return v.validate.Struct(out)
}
