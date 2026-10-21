// Command migrate applies one service's goose migrations and exits. Which
// service is named by MIGRATE_SERVICE (docker-compose sets it per
// migrate-<service> entry); the database is POSTGRES_DSN, same variable
// every other command reads.
package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("migrate failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	service := os.Getenv("MIGRATE_SERVICE")
	if service == "" {
		return fmt.Errorf("migrate: MIGRATE_SERVICE is required, e.g. \"user\"")
	}
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		if os.Getenv("APP_ENV") != "" && os.Getenv("APP_ENV") != "dev" {
			return fmt.Errorf("migrate: POSTGRES_DSN is required")
		}
		dsn = config.DefaultDSN(config.Service(service))
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("migrate: open: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	dir := service
	if err := goose.Up(db, dir); err != nil {
		return fmt.Errorf("migrate: up (%s): %w", service, err)
	}

	slog.Info("migrations applied", slog.String("service", service))
	return nil
}
