// Package postgres adapts transaction's Store port to Postgres via pgx. Every
// write appends its event to the outbox in the same transaction.
package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/events"
	"github.com/koobam/wuzz-backend/internal/pkg/outbox"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/transaction/app"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

type Store struct{ pool *postgres.Pool }

func NewStore(pool *postgres.Pool) *Store { return &Store{pool: pool} }

const cols = `id, outlet_id, customer_name, customer_phone, lines, total, status, created_by, version, created_at, updated_at`

// jsonLine is the stored shape of an order line.
type jsonLine struct {
	ItemID int64   `json:"itemId"`
	Name   string  `json:"name"`
	Unit   string  `json:"unit"`
	Price  int64   `json:"price"`
	Qty    float64 `json:"qty"`
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (domain.Order, error) {
	var (
		o   domain.Order
		raw []byte
	)
	err := r.Scan(&o.ID, &o.OutletID, &o.CustomerName, &o.CustomerPhone, &raw, &o.Total, &o.Status, &o.CreatedBy, &o.Version, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, apperr.NotFound("Order not found")
	}
	if err != nil {
		return o, apperr.Internal("Could not load the order").WithCause(err)
	}
	var lines []jsonLine
	if err := json.Unmarshal(raw, &lines); err != nil {
		return o, apperr.Internal("Could not read the order's items").WithCause(err)
	}
	for _, l := range lines {
		o.Lines = append(o.Lines, domain.Line{ItemID: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty})
	}
	return o, nil
}

func toJSONLines(lines []domain.Line) []jsonLine {
	out := make([]jsonLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, jsonLine{ItemID: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty})
	}
	return out
}

func (s *Store) CreateOrder(ctx context.Context, o domain.Order) (domain.Order, bool, error) {
	var (
		stored  domain.Order
		created bool
	)
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		lines, err := json.Marshal(toJSONLines(o.Lines))
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO orders (id, outlet_id, customer_name, customer_phone, lines, total, status, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO NOTHING`,
			o.ID, o.OutletID, o.CustomerName, o.CustomerPhone, lines, o.Total, o.Status, o.CreatedBy)
		if err != nil {
			return err
		}
		created = tag.RowsAffected() == 1

		if stored, err = scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM orders WHERE id = $1`, o.ID)); err != nil {
			return err
		}
		if !created {
			return nil // a retry: nothing new happened, so no event
		}
		return emit(ctx, tx, events.TypeOrderCreated, stored)
	})
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return domain.Order{}, false, err
		}
		return domain.Order{}, false, apperr.Internal("Could not create the order").WithCause(err)
	}
	return stored, created, nil
}

func (s *Store) GetOrder(ctx context.Context, id string) (domain.Order, error) {
	if !validUUID(id) {
		return domain.Order{}, apperr.NotFound("Order not found")
	}
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM orders WHERE id = $1`, id))
}

func (s *Store) ListOrders(ctx context.Context, f app.ListFilter) ([]domain.Order, int64, error) {
	const where = `WHERE (coalesce(cardinality($1::bigint[]), 0) = 0 OR outlet_id = ANY($1)) AND ($2 = '' OR status = $2)`
	// An empty OutletIDs from a staff caller never reaches here (app.List
	// rejects non-staff), so "empty = all" only ever means an admin's.
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM orders `+where, f.OutletIDs, f.Status).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("Could not list orders").WithCause(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM orders `+where+` ORDER BY created_at DESC LIMIT $3 OFFSET $4`,
		f.OutletIDs, f.Status, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, apperr.Internal("Could not list orders").WithCause(err)
	}
	defer rows.Close()
	var out []domain.Order
	for rows.Next() {
		o, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}

func (s *Store) UpdateStatus(ctx context.Context, id, status string) (domain.Order, error) {
	var updated domain.Order
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		current, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM orders WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if current.Status == status {
			updated = current // already there: idempotent for a retried tap
			return nil
		}
		if !domain.CanMoveTo(current.Status, status) {
			return apperr.Conflict("An order that is " + current.Status + " cannot become " + status)
		}
		if updated, err = scan(tx.QueryRow(ctx, `
			UPDATE orders SET status = $2, version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+cols, id, status)); err != nil {
			return err
		}
		return emit(ctx, tx, events.TypeOrderStatusChanged, updated)
	})
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return domain.Order{}, err
		}
		return domain.Order{}, apperr.Internal("Could not update the order").WithCause(err)
	}
	return updated, nil
}

func emit(ctx context.Context, tx pgx.Tx, typ string, o domain.Order) error {
	lines := make([]events.OrderLine, 0, len(o.Lines))
	for _, l := range o.Lines {
		lines = append(lines, events.OrderLine{ItemID: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty})
	}
	env, err := events.New(typ, events.AggregateOrder, o.ID, o.Version, events.OrderChanged{
		ID: o.ID, OutletID: o.OutletID, CustomerName: o.CustomerName, CustomerPhone: o.CustomerPhone,
		Lines: lines, Total: o.Total, Status: o.Status, CreatedBy: o.CreatedBy,
	})
	if err != nil {
		return err
	}
	return outbox.Append(ctx, tx, env)
}
