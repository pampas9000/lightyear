package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"transcoder/server/internal/api"
	"transcoder/server/internal/config"
	"transcoder/server/internal/models"
	"transcoder/server/internal/pkg/s3"
	"transcoder/server/internal/services/storage"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	aws_s3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application failed", "error", err)
		os.Exit(1)
	}
}

func setupLogger(cfg config.Config) {
	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}

	if cfg.IsProduction() {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
}

func run() error {
	config := config.Load()
	setupLogger(config)
	ctx := context.Background()

	db, err := openDatabase(ctx, config.DB)
	if err != nil {
		return err
	}

	// Open Redis Client
	kv, err := openRedis(ctx, config.Redis)
	if err != nil {
		return err
	}
	defer kv.Close()

	// Open AWS S3 Client
	s3Client, err := openS3(ctx, config.S3)
	if err != nil {
		return err
	}

	// Initialize Storage Service with S3 Client
	s3Service := storage.NewS3Service(s3Client)

	// Initialize Fiber App with Storage Service
	app := newFiberApp(db, kv, config, s3Service)

	slog.Info("server starting",
		"env", config.AppEnv,
		"addr", config.ListenAddr,
		"auto_migrate", config.DB.AutoMigrate,
	)

	if err := app.Listen(config.ListenAddr); err != nil {
		return fmt.Errorf("listen on %s: %w", config.ListenAddr, err)
	}

	return nil
}

func openDatabase(ctx context.Context, cfg config.DatabaseConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.URL), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	if cfg.AutoMigrate {
		if err := db.AutoMigrate(
			&models.User{},
			&models.UserOauthAccount{},
			&models.File{},
			&models.Workflow{},
			&models.Task{},
			&models.Job{},
		); err != nil {
			return nil, fmt.Errorf("auto migrate schema: %w", err)
		}
	}

	slog.Info("postgres connected")
	return db, nil
}

func openRedis(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	kv := redis.NewClient(opt)

	if err := kv.Ping(ctx).Err(); err != nil {
		_ = kv.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	slog.Info("redis connected")
	return kv, nil
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

	app.Use(logger.New())

	r := api.NewRouter(db, kv, cfg, s3Service)
	r.Install(app)

	return app
}
