package migrations_test

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/koobam/wuzz-backend/migrations"
)

var services = []string{"user", "catalog", "transaction", "gateway"}

// sqlFiles lists every migration file as "service/name.sql".
func sqlFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, svc := range services {
		entries, err := fs.ReadDir(migrations.FS, svc)
		if err != nil {
			t.Fatalf("read %s: %v", svc, err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".sql") {
				out = append(out, path.Join(svc, e.Name()))
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no migrations found")
	}
	return out
}

// TestEveryMigrationHasUpAndDown is the house rule: a migration can be undone. The Down half
// must hold a statement of its own, so "-- +goose Down" followed by nothing does not pass.
func TestEveryMigrationHasUpAndDown(t *testing.T) {
	for _, file := range sqlFiles(t) {
		raw, err := fs.ReadFile(migrations.FS, file)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		up := strings.Index(body, "-- +goose Up")
		down := strings.Index(body, "-- +goose Down")
		switch {
		case up < 0:
			t.Errorf("%s: no \"-- +goose Up\"", file)
		case down < 0:
			t.Errorf("%s: no \"-- +goose Down\": every migration must be reversible", file)
		case down < up:
			t.Errorf("%s: Down comes before Up", file)
		default:
			if !hasStatement(body[down+len("-- +goose Down"):]) {
				t.Errorf("%s: the Down section has no statement; write what undoes the Up", file)
			}
			if !hasStatement(body[up+len("-- +goose Up") : down]) {
				t.Errorf("%s: the Up section has no statement", file)
			}
		}
	}
}

// hasStatement reports whether s holds something other than comments and blank lines.
func hasStatement(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			return true
		}
	}
	return false
}

// TestRoundTrip applies every migration of a service, undoes them all, and applies them again.
// A Down that does not undo its Up (or does not run at all) fails here, not on a bad night.
// It needs a Postgres it may create databases in: set MIGRATIONS_TEST_DSN, for example
// postgres://postgres:pw@localhost:5432/postgres?sslmode=disable. Skipped otherwise.
func TestRoundTrip(t *testing.T) {
	admin := os.Getenv("MIGRATIONS_TEST_DSN")
	if admin == "" {
		t.Skip("MIGRATIONS_TEST_DSN not set")
	}
	root, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}

	for _, svc := range services {
		t.Run(svc, func(t *testing.T) {
			name := fmt.Sprintf("roundtrip_%s_%d", svc, time.Now().UnixNano())
			if _, err := root.Exec("CREATE DATABASE " + name); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = root.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })

			dsn := regexp.MustCompile(`/[^/?]*\?`).ReplaceAllString(admin, "/"+name+"?")
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			if err := goose.Up(db, svc); err != nil {
				t.Fatalf("up: %v", err)
			}
			if err := goose.DownTo(db, svc, 0); err != nil {
				t.Fatalf("down to nothing: %v", err)
			}
			// Nothing of ours may be left behind: only goose's own bookkeeping table.
			var left int
			if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name <> 'goose_db_version'`).Scan(&left); err != nil {
				t.Fatal(err)
			}
			if left != 0 {
				t.Errorf("%d table(s) still exist after undoing every migration", left)
			}
			if err := goose.Up(db, svc); err != nil {
				t.Fatalf("up again: %v", err)
			}
		})
	}
}
