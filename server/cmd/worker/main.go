// Command worker is the Go orchestrator: it dispatches jobs, records
// results, recovers stuck jobs and runs the storage lifecycle sweeps. The
// Rust compute worker that processes jobs is compute/crates/worker.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
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
	// Checked first: it needs no network, so a missing variable is reported
	// before anything else can fail.
	store, err := bootstrap.OpenStorage(ctx, cfg.S3)
	if err != nil {
		return err
	}
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
	retention := lifecycle.Days(cfg.Retention.SourceDays, cfg.Retention.OutputDays)
	slog.Info("orchestrator started")

	// Both stop with ctx; run returns once both have.
	var wg sync.WaitGroup
	wg.Go(func() {
		lifecycle.NewService(db, store, cfg.S3.Bucket, retention).Run(ctx, lifecycle.SweepInterval)
	})
	err = orchestrator.Run(ctx, db, kv, orchestrator.Limits{
		MaxProcessingPerOwner: cfg.Dispatch.MaxProcessingPerUser,
		ExecutionLimit:        cfg.Dispatch.ExecutionLimit,
		TargetExecutionLimit:  cfg.Dispatch.TargetExecutionLimit,
	}, retention)
	// orchestrator.Run returns early only on a setup error; stop the sweeps too.
	stop()
	wg.Wait()
	return err
}
