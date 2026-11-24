package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"transcoder/server/internal/api"
	"transcoder/server/internal/bootstrap"
	"transcoder/server/internal/config"
	"transcoder/server/internal/pkg/s3"
	"transcoder/server/internal/schema"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/storage"

	"gorm.io/gorm"

	aws_s3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application failed", "error", err)
		os.Exit(1)
	}
}

// shutdownTimeout bounds how long in-flight requests get to finish after
// SIGINT or SIGTERM.
const shutdownTimeout = 10 * time.Second

func run() error {
	// Every timestamp the API returns is UTC ("…Z"). Without this, times made
	// in Go (download and upload URL expiry, revoked_at, created_at on create
	// responses) carry the host's offset while times read back from Postgres
	// carry the session's, so one response could mix "Z" and "+08:00".
	time.Local = time.UTC
	config := config.Load()
	bootstrap.SetupLogger(config, slog.LevelDebug)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := bootstrap.OpenDatabase(config.DB)
	if err != nil {
		return err
	}
	defer bootstrap.CloseDatabase(db)
	if config.DB.AutoMigrate {
		slog.Info("running auto migration (AUTO_MIGRATE=true)")
		if err := schema.Migrate(db); err != nil {
			return fmt.Errorf("auto migrate schema: %w", err)
		}
	}
	slog.Info("postgres connected")

	kv, err := bootstrap.OpenRedis(ctx, config.Redis)
	if err != nil {
		return err
	}
	defer kv.Close()
	slog.Info("redis connected")

	s3Client, err := openS3(ctx, config.S3)
	if err != nil {
		return err
	}
	s3Service := storage.NewS3Service(s3Client)

	// Background sweeps stop with ctx: retention (expire sources and
	// outputs, retry failed deletions) and, hourly, unfinished uploads and
	// stale drafts.
	lifecycle.NewService(db, s3Service, config.S3.Bucket, api.RetentionPolicy(config)).Start(ctx, 10*time.Minute)

	app := newFiberApp(db, kv, config, s3Service)
	slog.Info("server starting",
		"env", config.AppEnv,
		"addr", config.ListenAddr,
		"auto_migrate", config.DB.AutoMigrate,
	)
	listened := make(chan error, 1)
	go func() { listened <- app.Listen(config.ListenAddr) }()
	select {
	case err := <-listened:
		if err != nil {
			return fmt.Errorf("listen on %s: %w", config.ListenAddr, err)
		}
		return nil
	case <-ctx.Done():
	}
	// A second signal now ends the process at once.
	stop()
	slog.Info("shutting down", "timeout", shutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	if err := <-listened; err != nil {
		return fmt.Errorf("listen on %s: %w", config.ListenAddr, err)
	}
	slog.Info("server stopped")
	return nil
}

type structValidator struct {
	validate *validator.Validate
}

func openS3(ctx context.Context, cfg config.S3Config) (*aws_s3.Client, error) {
	client, err := s3.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open s3 client: %w", err)
	}

	slog.Info("s3 connected")
	return client, nil
}

// Validator needs to implement the Validate method
func (v *structValidator) Validate(out any) error {
	return v.validate.Struct(out)
}

func newFiberApp(db *gorm.DB, kv *redis.Client, cfg config.Config, s3Service *storage.S3Service) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:         "transcoder-server",
		StructValidator: &structValidator{validate: validator.New()},
	})

	app.Use(requestid.New())
	app.Use(logger.New())

	r := api.NewRouter(db, kv, cfg, s3Service)
	r.Install(app)

	return app
}
