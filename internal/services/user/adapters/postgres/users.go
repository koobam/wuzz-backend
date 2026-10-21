// Package postgres adapts the user service's ports to a Postgres-backed
// store via pgx. No ORM — the queries are small and explicit enough that one
// would only add indirection.
package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/events"
	"github.com/koobam/wuzz-backend/internal/pkg/outbox"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/user/app/command"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

type Users struct{ pool *postgres.Pool }

func NewUsers(pool *postgres.Pool) *Users { return &Users{pool: pool} }

const userColumns = `id, username, name, phone, email, points, streak, xp, birthdate, status, created_at, updated_at, roles`

func (r *Users) ByPhone(ctx context.Context, phone string) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE phone = $1`, phone)
	return scanUser(row)
}

func (r *Users) ByID(ctx context.Context, id string) (domain.User, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return domain.User{}, apperr.NotFound("User not found")
	}
	row := r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, n)
	return scanUser(row)
}

func (r *Users) ExistsUsername(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, username).Scan(&exists)
	if err != nil {
		return false, apperr.Internal("Could not check username availability").WithCause(err)
	}
	return exists, nil
}

// Create inserts the account and, in the same transaction, the user.registered
// event: the pair commits or rolls back together, so a consumer is never told
// about an account that does not exist, nor missed one that does.
// List returns a page of accounts, newest first, and the total matching the
// filter. query matches username, name or phone, case-insensitively.
func (r *Users) List(ctx context.Context, f command.ListFilter) ([]domain.User, int64, error) {
	const where = `
		WHERE ($1 = '' OR status = $1)
		  AND ($2 = '' OR username ILIKE '%' || $2 || '%' OR name ILIKE '%' || $2 || '%' OR phone LIKE '%' || $2 || '%')`

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`+where, f.Status, f.Query).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("Could not list accounts").WithCause(err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT `+userColumns+` FROM users`+where+` ORDER BY id DESC LIMIT $3 OFFSET $4`,
		f.Status, f.Query, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, apperr.Internal("Could not list accounts").WithCause(err)
	}
	defer rows.Close()

	var out []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

func (r *Users) SetStatus(ctx context.Context, id, status string) (domain.User, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return domain.User{}, apperr.NotFound("User not found")
	}
	row := r.pool.QueryRow(ctx,
		`UPDATE users SET status = $2, updated_at = now() WHERE id = $1 RETURNING `+userColumns, n, status)
	return scanUser(row)
}

func (r *Users) Create(ctx context.Context, u domain.User) (domain.User, error) {
	var email *string
	if u.Email != "" {
		email = &u.Email
	}

	var created domain.User
	err := postgres.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO users (username, name, phone, email, birthdate, status)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING `+userColumns,
			u.Username, u.Name, u.Phone, email, u.Birthdate, u.Status,
		)
		var err error
		if created, err = scanUser(row); err != nil {
			return err
		}

		envelope, err := events.New(events.TypeUserRegistered, events.AggregateUser, created.ID, 1,
			events.UserRegistered{UserID: created.ID, Username: created.Username, Name: created.Name})
		if err != nil {
			return err
		}
		return outbox.Append(ctx, tx, envelope)
	})
	if err != nil {
		return domain.User{}, apperr.Internal("Could not create the account").WithCause(err)
	}
	return created, nil
}

type row interface {
	Scan(dest ...any) error
}

func scanUser(r row) (domain.User, error) {
	var (
		u         domain.User
		id        int64
		email     *string
		birthdate *time.Time
	)
	err := r.Scan(&id, &u.Username, &u.Name, &u.Phone, &email, &u.Points, &u.Streak, &u.XP, &birthdate, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.Roles)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, apperr.NotFound("User not found")
		}
		return domain.User{}, apperr.Internal("Could not load the account").WithCause(err)
	}
	u.ID = strconv.FormatInt(id, 10)
	if email != nil {
		u.Email = *email
	}
	u.Birthdate = birthdate
	return u, nil
}
