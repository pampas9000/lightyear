package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"transcoder/server/internal/bootstrap"
	"transcoder/server/internal/config"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/orchestrator"
)

func main() {
	if err := run(); err != nil {
		slog.Error("orchestrator failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	bootstrap.SetupLogger(cfg, slog.LevelInfo)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := bootstrap.OpenDatabase(cfg.DB)
	if err != nil {
		return err
	}
	defer bootstrap.CloseDatabase(db)
	kv, err := bootstrap.OpenRedis(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer kv.Close()
	slog.Info("orchestrator started")
	return orchestrator.Run(ctx, db, kv, orchestrator.Limits{
		MaxProcessingPerOwner: cfg.Dispatch.MaxProcessingPerUser,
		ExecutionLimit:        cfg.Dispatch.ExecutionLimit,
		TargetExecutionLimit:  cfg.Dispatch.TargetExecutionLimit,
	}, lifecycle.Days(cfg.Retention.SourceDays, cfg.Retention.OutputDays))
}
