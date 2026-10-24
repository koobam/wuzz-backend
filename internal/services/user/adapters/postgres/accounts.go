package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// Accounts reads admins and staff, each from its own table.
type Accounts struct{ pool *postgres.Pool }

func NewAccounts(pool *postgres.Pool) *Accounts { return &Accounts{pool: pool} }

// outlet_id is NULL for anyone not tied to an outlet; an admin has no such column.
const (
	adminSelect = `SELECT id, name, phone, role, 0::bigint, status, created_at, updated_at FROM admins`
	staffSelect = `SELECT id, name, phone, role, COALESCE(outlet_id, 0), status, created_at, updated_at FROM staff`
)

// ByPhone looks in admins first, then staff. A phone number is in one of them at most in practice;
// if it were in both, the admin wins, as the account with more access to answer for.
func (r *Accounts) ByPhone(ctx context.Context, phone string) (domain.Account, error) {
	a, err := r.scan(ctx, domain.KindAdmin, adminSelect+` WHERE phone = $1`, phone)
	if err == nil || !apperr.IsKind(err, apperr.KindNotFound) {
		return a, err
	}
	return r.scan(ctx, domain.KindStaff, staffSelect+` WHERE phone = $1`, phone)
}

func (r *Accounts) ByID(ctx context.Context, id string) (domain.Account, error) {
	kind, n, ok := domain.ParseAccountID(id)
	if !ok {
		return domain.Account{}, apperr.NotFound("Account not found")
	}
	if kind == domain.KindAdmin {
		return r.scan(ctx, kind, adminSelect+` WHERE id = $1`, n)
	}
	return r.scan(ctx, kind, staffSelect+` WHERE id = $1`, n)
}

func (r *Accounts) scan(ctx context.Context, kind, query string, arg any) (domain.Account, error) {
	var (
		a  domain.Account
		id int64
	)
	err := r.pool.QueryRow(ctx, query, arg).
		Scan(&id, &a.Name, &a.Phone, &a.Role, &a.OutletID, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Account{}, apperr.NotFound("Account not found")
		}
		return domain.Account{}, apperr.Internal("Could not load the account").WithCause(err)
	}
	a.ID = domain.AccountID(kind, id)
	a.Kind = kind
	return a, nil
}
