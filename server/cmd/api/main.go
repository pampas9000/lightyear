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
	"transcoder/server/internal/schema"
	"transcoder/server/internal/services/storage"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
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
	if err := config.ValidateAPI(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The API runs no background work: the storage lifecycle sweeps run in
	// the orchestrator (cmd/worker). Storage is checked first because it
	// needs no network, so a missing variable is reported before anything
	// else can fail.
	s3Service, err := bootstrap.OpenStorage(ctx, config.S3)
	if err != nil {
		return err
	}

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

func newFiberApp(db *gorm.DB, kv *redis.Client, cfg config.Config, s3Service *storage.S3Service) *fiber.App {
	return api.NewApp(api.NewRouter(db, kv, cfg, s3Service))
}
