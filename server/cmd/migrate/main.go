package main

import (
	"fmt"
	"log/slog"
	"os"

	"transcoder/server/internal/config"
	"transcoder/server/internal/schema"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	cfg := config.Load()
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  cfg.DB.URL,
		PreferSimpleProtocol: true,
	}), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		slog.Error("open postgres", "error", err)
		os.Exit(1)
	}
	sqlDB, err := db.DB()
	if err != nil {
		slog.Error("open postgres pool", "error", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	if err := schema.Migrate(db); err != nil {
		slog.Error("migrate schema", "error", err)
		os.Exit(1)
	}
	fmt.Println("database migration completed")
}
