package main

import (
	"flag"
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
	reset := flag.Bool("reset", false, "drop every application table and recreate the schema (destroys all data)")
	flag.Parse()
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

	run, done := schema.Migrate, "database migration completed"
	if *reset {
		run, done = schema.Reset, "database reset and migrated"
	}
	if err := run(db); err != nil {
		slog.Error("migrate schema", "error", err)
		os.Exit(1)
	}
	fmt.Println(done)
}
