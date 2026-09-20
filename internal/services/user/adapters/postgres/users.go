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
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

type Users struct{ pool *postgres.Pool }

func NewUsers(pool *postgres.Pool) *Users { return &Users{pool: pool} }

const userColumns = `id, username, name, phone, email, points, streak, xp, birthdate, status, created_at, updated_at`

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

func (r *Users) Create(ctx context.Context, u domain.User) (domain.User, error) {
	var email *string
	if u.Email != "" {
		email = &u.Email
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (username, name, phone, email, birthdate, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+userColumns,
		u.Username, u.Name, u.Phone, email, u.Birthdate, u.Status,
	)
	created, err := scanUser(row)
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
	err := r.Scan(&id, &u.Username, &u.Name, &u.Phone, &email, &u.Points, &u.Streak, &u.XP, &birthdate, &u.Status, &u.CreatedAt, &u.UpdatedAt)
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
