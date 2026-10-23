// Command migrate runs one service's goose migrations and exits. Which service is named by
// MIGRATE_SERVICE (docker-compose sets it per migrate-<service> entry); the database is
// POSTGRES_DSN, same variable every other command reads.
//
//	migrate            apply everything not applied yet (also: migrate up)
//	migrate status     list every migration and whether it ran
//	migrate down [n]   undo the last migration, or the last n (or "all"); same as MIGRATE_STEPS=n
//
// up refuses a migration that removes something the previous version of the service may
// still use (see guard.go) unless MIGRATE_ALLOW_DESTRUCTIVE=true. down runs the
// "-- +goose Down" half of each migration, newest first; it is refused in production unless
// MIGRATE_ALLOW_DOWN=yes is set on purpose, since it can drop columns and tables and the data
// in them does not come back.
package main

import (
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"

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
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "up":
		if err := checkPending(db, dir); err != nil {
			return err
		}
		if err := goose.Up(db, dir); err != nil {
			return fmt.Errorf("migrate: up (%s): %w", service, err)
		}
		slog.Info("migrations applied", slog.String("service", service))
	case "status":
		return goose.Status(db, dir)
	case "down":
		return down(db, service, dir)
	default:
		return fmt.Errorf("migrate: unknown command %q (up, down or status)", command)
	}
	return nil
}

// down undoes MIGRATE_STEPS migrations (default 1; "all" undoes every one), newest first.
func down(db *sql.DB, service, dir string) error {
	if os.Getenv("APP_ENV") == "production" && os.Getenv("MIGRATE_ALLOW_DOWN") != "yes" {
		return fmt.Errorf("migrate: down is refused in production; set MIGRATE_ALLOW_DOWN=yes if you mean it (it can drop data)")
	}

	steps := os.Getenv("MIGRATE_STEPS")
	if len(os.Args) > 2 {
		steps = os.Args[2]
	}
	if steps == "all" {
		if err := goose.DownTo(db, dir, 0); err != nil {
			return fmt.Errorf("migrate: down all (%s): %w", service, err)
		}
		slog.Info("every migration undone", slog.String("service", service))
		return nil
	}

	n := 1
	if steps != "" {
		var err error
		if n, err = strconv.Atoi(steps); err != nil || n < 1 {
			return fmt.Errorf("migrate: MIGRATE_STEPS must be a positive number or \"all\", got %q", steps)
		}
	}
	for i := 0; i < n; i++ {
		if err := goose.Down(db, dir); err != nil {
			return fmt.Errorf("migrate: down step %d of %d (%s): %w", i+1, n, service, err)
		}
	}
	slog.Info("migrations undone", slog.String("service", service), slog.Int("steps", n))
	return nil
}

// checkPending refuses the migrations about to run if any of them removes something the
// previous version may still use, unless MIGRATE_ALLOW_DESTRUCTIVE=true.
func checkPending(db *sql.DB, dir string) error {
	if os.Getenv("MIGRATE_ALLOW_DESTRUCTIVE") == "true" {
		return nil
	}
	current, err := goose.EnsureDBVersion(db)
	if err != nil {
		return fmt.Errorf("migrate: read the current version: %w", err)
	}
	all, err := goose.CollectMigrations(dir, 0, goose.MaxVersion)
	if err != nil {
		return fmt.Errorf("migrate: collect migrations: %w", err)
	}
	flagged := map[string][]string{}
	for _, m := range all {
		if m.Version <= current {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, m.Source)
		if err != nil {
			return fmt.Errorf("migrate: read %s: %w", m.Source, err)
		}
		if what := destructiveUp(string(body)); len(what) > 0 {
			flagged[m.Source] = what
		}
	}
	if len(flagged) > 0 {
		return refuseDestructive(flagged)
	}
	return nil
}
