// Package bootstrap opens what the API and the orchestrator both start from:
// the logger, the database pool, the Redis client and object storage.
package bootstrap

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"transcoder/server/internal/config"
	"transcoder/server/internal/pkg/s3"
	"transcoder/server/internal/services/storage"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// SetupLogger installs the default slog logger: JSON in production, text
// otherwise.
func SetupLogger(cfg config.Config, level slog.Level) {
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewTextHandler(os.Stdout, opts)
	if cfg.IsProduction() {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}

// OpenDatabase opens the Postgres pool. It does not migrate the schema.
func OpenDatabase(cfg config.DatabaseConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: cfg.URL, PreferSimpleProtocol: true}), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger: gormlogger.New(log.New(os.Stdout, "", log.LstdFlags), gormlogger.Config{
			SlowThreshold: time.Second, LogLevel: gormlogger.Warn, IgnoreRecordNotFoundError: true,
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetConnMaxLifetime(time.Hour)
	return db, nil
}

// CloseDatabase closes the pool behind db.
func CloseDatabase(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// OpenRedis connects to Redis and checks the connection.
func OpenRedis(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	kv := redis.NewClient(opt)
	if err := kv.Ping(ctx).Err(); err != nil {
		_ = kv.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return kv, nil
}

// OpenStorage builds the S3 client. It fails when the bucket or credentials
// are not configured; it does not contact the store.
func OpenStorage(ctx context.Context, cfg config.S3Config) (*storage.S3Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := s3.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open s3 client: %w", err)
	}
	return storage.NewS3Service(client), nil
}
