package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"transcoder/server/internal/config"
	"transcoder/server/internal/services/orchestrator"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func main() {
	if err := run(); err != nil {
		slog.Error("orchestrator failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if cfg.IsProduction() {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, opts)))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, opts)))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: cfg.DB.URL, PreferSimpleProtocol: true}), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger: gormlogger.New(log.New(os.Stdout, "", log.LstdFlags), gormlogger.Config{
			SlowThreshold: time.Second, LogLevel: gormlogger.Warn, IgnoreRecordNotFoundError: true,
		}),
	})
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetConnMaxLifetime(time.Hour)
	opt, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return err
	}
	kv := redis.NewClient(opt)
	defer kv.Close()
	if err := kv.Ping(ctx).Err(); err != nil {
		return err
	}
	slog.Info("orchestrator started")
	return orchestrator.Run(ctx, db, kv)
}
