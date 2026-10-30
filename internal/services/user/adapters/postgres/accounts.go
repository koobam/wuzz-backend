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
	adminSelect = `SELECT id, name, phone, role, COALESCE(outlet_id, 0), status, created_at, updated_at FROM admins`
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

// accountOrder maps a whitelisted sort name to its SQL; ties are broken by id.
var accountOrder = map[string]string{"id": "id", "name": "lower(name)", "created_at": "created_at"}

// orderBy builds an ORDER BY from a whitelist: the column and the direction come from fixed strings, never from the caller's text.
func orderBy(columns map[string]string, by, dir string) string {
	col, ok := columns[by]
	if !ok {
		col = columns["id"]
	}
	d := "ASC"
	if dir == "desc" {
		d = "DESC"
	}
	if col == "id" {
		return "id " + d
	}
	return col + " " + d + ", id ASC"
}

// List pages one kind of account, in the order asked for (newest first by default). The table is picked from the kind, never from the
// caller's text, so nothing user-supplied reaches the SQL but the bound filter values.
func (r *Accounts) List(ctx context.Context, kind string, f domain.ListFilter) ([]domain.Account, int64, error) {
	table := "staff"
	if kind == domain.KindAdmin {
		table = "admins"
	}
	const where = `
		WHERE ($1 = '' OR status = $1)
		  AND ($2 = '' OR name ILIKE '%' || $2 || '%' OR phone LIKE '%' || $2 || '%')
		  AND ($3::bigint = 0 OR outlet_id = $3)`

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+where, f.Status, f.Query, f.OutletID).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("Could not list accounts").WithCause(err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT id, name, phone, role, COALESCE(outlet_id, 0), status, created_at, updated_at FROM `+table+where+
			` ORDER BY `+orderBy(accountOrder, f.SortBy, f.SortDir)+` LIMIT $4 OFFSET $5`,
		f.Status, f.Query, f.OutletID, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, apperr.Internal("Could not list accounts").WithCause(err)
	}
	defer rows.Close()

	var out []domain.Account
	for rows.Next() {
		var (
			a  domain.Account
			id int64
		)
		if err := rows.Scan(&id, &a.Name, &a.Phone, &a.Role, &a.OutletID, &a.Status, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, 0, apperr.Internal("Could not list accounts").WithCause(err)
		}
		a.ID = domain.AccountID(kind, id)
		a.Kind = kind
		out = append(out, a)
	}
	return out, total, rows.Err()
}
