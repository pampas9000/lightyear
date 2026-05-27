package api

import (
	"transcoder/server/internal/api/handlers"
	"transcoder/server/internal/api/middleware"
	"transcoder/server/internal/config"
	"transcoder/server/internal/services/storage"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/session"
	fiber_redis "github.com/gofiber/storage/redis/v3"
	"github.com/redis/go-redis/v9"
)

// Router composes feature-specific API handlers and mounts them under /api.
type Router struct {
	job      *handlers.Handler
	workflow *handlers.WorkflowHandler
	task     *handlers.TaskHandler
	upload   *handlers.UploadHandler
	auth     *handlers.AuthHandler
	session  *session.Store
}

// NewRouter wires feature handlers with the dependencies they need.
func NewRouter(db *gorm.DB, kv *redis.Client, cfg config.Config, s3Storage *storage.S3Service) *Router {
	store := session.NewStore(session.Config{
		Storage:        fiber_redis.NewFromConnection(kv),
		CookieHTTPOnly: true,
		CookieSecure:   cfg.IsProduction(),
	})

	return &Router{
		job:      handlers.NewHandler(db, kv),
		workflow: handlers.NewWorkflowHandler(db),
		task:     handlers.NewTaskHandler(db, kv),
		upload:   handlers.NewUploadHandler(db, cfg, s3Storage),
		auth:     handlers.NewAuthHandler(db, store, cfg),
		session:  store,
	}
}

// Install mounts all API modules.
//
// Keep this root router focused on composition only.
// Feature-specific routing should live in subpackages such as
// `internal/api/handlers`.
func (r *Router) Install(app fiber.Router) {
	api := app.Group("/api")

	// Public routes
	r.auth.Install(api)

	// Protected routes
	protected := api.Group("/", middleware.AuthMiddleware(r.session))
	protected.Get("/me", r.auth.Me)
	r.job.Install(protected)
	r.workflow.Install(protected)
	r.task.Install(protected)
	r.upload.Install(protected)
}
