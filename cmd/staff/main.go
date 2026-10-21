// Command staff creates or updates a staff account and sets its roles.
//
//	go run ./cmd/staff -phone 081234500099 -name "Admin Ani" -roles admin
//	go run ./cmd/staff -phone 081234500098 -name "Kasir Budi" -roles outlet:1
//
// Staff sign in with a phone + one-time code like everyone else, so the
// account is keyed by phone. Pass -roles "" to demote an account to a customer.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

func main() {
	phone := flag.String("phone", "", "phone number")
	name := flag.String("name", "", "display name (used when the account is new)")
	roles := flag.String("roles", "admin", "comma-separated roles: admin, outlet:<id>")
	flag.Parse()

	if err := run(*phone, *name, *roles); err != nil {
		slog.Error("staff failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(rawPhone, name, rawRoles string) error {
	phone, err := domain.NormalisePhone(rawPhone)
	if err != nil {
		return err
	}
	var roles []string
	for _, r := range strings.Split(rawRoles, ",") {
		if r = strings.TrimSpace(r); r != "" {
			roles = append(roles, r)
		}
	}
	if name == "" {
		name = "Staff " + phone[len(phone)-4:]
	}

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://wuzz:wuzz@localhost:5433/wuzz?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	// username is derived from the phone: unique because the phone is, and it
	// satisfies the users_username_format constraint.
	username := "staff" + phone[max(0, len(phone)-8):]
	var id int64
	err = pool.QueryRow(context.Background(), `
		INSERT INTO users (username, name, phone, roles)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (phone) DO UPDATE SET roles = EXCLUDED.roles, updated_at = now()
		RETURNING id`, username, name, phone, roles).Scan(&id)
	if err != nil {
		return err
	}
	fmt.Printf("user %d (%s) roles=%v\n", id, phone, roles)
	return nil
}
