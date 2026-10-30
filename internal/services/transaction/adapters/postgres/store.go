// Package postgres adapts transaction's Store port to Postgres via pgx. Every
// write appends its event to the outbox in the same transaction.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/events"
	"github.com/koobam/wuzz-backend/internal/pkg/outbox"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/transaction/adapters/searchindex"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

type Store struct {
	pool *postgres.Pool
	// ix is the search index of the order list; nil lists straight from Postgres (see search.go).
	ix  *searchindex.Index
	log *slog.Logger
	// newCode makes a fresh code for an order created at the given time; the tests replace it.
	newCode func(time.Time) string
}

func NewStore(pool *postgres.Pool) *Store { return &Store{pool: pool, newCode: domain.NewCode} }

const cols = `id, outlet_id, customer_name, customer_phone, lines, total, status, created_by, version, created_at, updated_at, code`

// jsonLine is the stored shape of an order line.
type jsonLine struct {
	ItemID int64   `json:"itemId"`
	Name   string  `json:"name"`
	Unit   string  `json:"unit"`
	Price  int64   `json:"price"`
	Qty    float64 `json:"qty"`
	// Older orders have neither.
	Variant   string         `json:"variant,omitempty"`
	Modifiers []jsonModifier `json:"modifiers,omitempty"`
	// The catalog ids of the options chosen, for costing.
	VariantOptionIDs  []int64 `json:"variantOptionIds,omitempty"`
	ModifierOptionIDs []int64 `json:"modifierOptionIds,omitempty"`
}

type jsonModifier struct {
	Group string `json:"group,omitempty"`
	Name  string `json:"name"`
	Price int64  `json:"price"`
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (domain.Order, error) {
	var (
		o   domain.Order
		raw []byte
	)
	err := r.Scan(&o.ID, &o.OutletID, &o.CustomerName, &o.CustomerPhone, &raw, &o.Total, &o.Status, &o.CreatedBy, &o.Version, &o.CreatedAt, &o.UpdatedAt, &o.Code)
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
		line := domain.Line{ItemID: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty, Variant: l.Variant,
			VariantOptionIDs: l.VariantOptionIDs, ModifierOptionIDs: l.ModifierOptionIDs}
		for _, m := range l.Modifiers {
			line.Modifiers = append(line.Modifiers, domain.Modifier{Group: m.Group, Name: m.Name, Price: m.Price})
		}
		o.Lines = append(o.Lines, line)
	}
	return o, nil
}

func toJSONLines(lines []domain.Line) []jsonLine {
	out := make([]jsonLine, 0, len(lines))
	for _, l := range lines {
		line := jsonLine{ItemID: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty, Variant: l.Variant,
			VariantOptionIDs: l.VariantOptionIDs, ModifierOptionIDs: l.ModifierOptionIDs}
		for _, m := range l.Modifiers {
			line.Modifiers = append(line.Modifiers, jsonModifier{Group: m.Group, Name: m.Name, Price: m.Price})
		}
		out = append(out, line)
	}
	return out
}

// maxCodeAttempts is how many times a new order is tried with a different code when the one chosen is already taken.
const maxCodeAttempts = 10

// CreateOrder stores a new order and gives it its code: the one the client suggested if it is valid and not used by another order,
// otherwise a fresh one, tried again (up to 10 times) whenever the database says the code is taken. A retry of an id that is already
// stored returns that order with the code it already has, whatever code the retry carried.
func (s *Store) CreateOrder(ctx context.Context, o domain.Order) (domain.Order, bool, error) {
	now := time.Now()
	code := o.Code
	if !domain.ValidCode(code) {
		code = s.newCode(now)
	}
	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		stored, created, err := s.insertOrder(ctx, o, code, now)
		if !isCodeTaken(err) {
			return stored, created, err
		}
		if attempt < maxCodeAttempts-1 {
			code = s.newCode(now)
		}
	}
	return domain.Order{}, false, apperr.Internal("Could not give the order a unique transaction code; please try again")
}

// isCodeTaken says whether err is the unique constraint on orders.code.
func isCodeTaken(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "orders_code_key"
}

func (s *Store) insertOrder(ctx context.Context, o domain.Order, code string, now time.Time) (domain.Order, bool, error) {
	var (
		stored  domain.Order
		created bool
	)
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		lines, err := json.Marshal(toJSONLines(o.Lines))
		if err != nil {
			return err
		}
		// created_at is set here, not by the database, so the day in the code and the day of the order are the same moment.
		tag, err := tx.Exec(ctx, `
			INSERT INTO orders (id, outlet_id, customer_name, customer_phone, lines, total, status, created_by, code, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
			ON CONFLICT (id) DO NOTHING`,
			o.ID, o.OutletID, o.CustomerName, o.CustomerPhone, lines, o.Total, o.Status, o.CreatedBy, code, now)
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
		if errors.As(err, &ae) || isCodeTaken(err) {
			return domain.Order{}, false, err
		}
		return domain.Order{}, false, apperr.Internal("Could not create the order").WithCause(err)
	}
	if created {
		s.sync(ctx, stored)
	}
	return stored, created, nil
}

func (s *Store) GetOrder(ctx context.Context, id string) (domain.Order, error) {
	if !validUUID(id) {
		return domain.Order{}, apperr.NotFound("Order not found")
	}
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM orders WHERE id = $1`, id))
}

// Stats counts orders by status, sums the fulfilled revenue, and returns a row for each of the last
// `days` days (the calendar is Asia/Jakarta's, a day with no orders is a row of zeros). An empty
// outletIDs means every outlet; only an admin's request gets that far (see domain.Stats).
func (s *Store) Stats(ctx context.Context, outletIDs []int64, r domain.StatsRange) (domain.Stats, error) {
	out := domain.Stats{ByStatus: map[string]int64{}}
	// The range, in Asia/Jakarta's calendar. With no from/to it is the last `days` days, today included; the dates
	// come in as text (NULL when absent) and are cast here, so Go never has to know the time zone.
	var from, to *string
	if r.From != "" {
		from, to = &r.From, &r.To
	}
	const window = `WITH r AS (
		SELECT COALESCE($2::date, (now() AT TIME ZONE 'Asia/Jakarta')::date - ($4::int - 1)) AS d_from,
		       COALESCE($3::date, (now() AT TIME ZONE 'Asia/Jakarta')::date) AS d_to)`

	rows, err := s.pool.Query(ctx, window+`
		SELECT o.status, count(*), COALESCE(sum(o.total) FILTER (WHERE o.status = 'fulfilled'), 0)
		  FROM orders o, r
		 WHERE (coalesce(cardinality($1::bigint[]), 0) = 0 OR o.outlet_id = ANY($1))
		   AND (o.created_at AT TIME ZONE 'Asia/Jakarta')::date BETWEEN r.d_from AND r.d_to
		 GROUP BY o.status`, outletIDs, from, to, r.Days)
	if err != nil {
		return domain.Stats{}, apperr.Internal("Could not load order statistics").WithCause(err)
	}
	for rows.Next() {
		var status string
		var n, revenue int64
		if err := rows.Scan(&status, &n, &revenue); err != nil {
			rows.Close()
			return domain.Stats{}, apperr.Internal("Could not load order statistics").WithCause(err)
		}
		out.ByStatus[status] = n
		out.Revenue += revenue
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.Stats{}, apperr.Internal("Could not load order statistics").WithCause(err)
	}

	dayRows, err := s.pool.Query(ctx, window+`
		SELECT to_char(g.day, 'YYYY-MM-DD'),
		       count(o.id),
		       count(o.id) FILTER (WHERE o.status = 'fulfilled'),
		       count(o.id) FILTER (WHERE o.status = 'cancelled')
		  FROM r
		 CROSS JOIN generate_series(r.d_from, r.d_to, interval '1 day') AS g(day)
		  LEFT JOIN orders o
		    ON (o.created_at AT TIME ZONE 'Asia/Jakarta')::date = g.day::date
		   AND (coalesce(cardinality($1::bigint[]), 0) = 0 OR o.outlet_id = ANY($1))
		 GROUP BY g.day ORDER BY g.day`, outletIDs, from, to, r.Days)
	if err != nil {
		return domain.Stats{}, apperr.Internal("Could not load order statistics").WithCause(err)
	}
	defer dayRows.Close()
	for dayRows.Next() {
		var d domain.DayStats
		if err := dayRows.Scan(&d.Date, &d.Orders, &d.Fulfilled, &d.Cancelled); err != nil {
			return domain.Stats{}, apperr.Internal("Could not load order statistics").WithCause(err)
		}
		out.Days = append(out.Days, d)
	}
	return out, dayRows.Err()
}

// orderBy maps an domain.SortFields name to its SQL. Ties fall back to newest first, then id, so paging is stable.
var orderBy = map[string]string{
	"created_at":    "created_at",
	"total":         "total",
	"status":        "status",
	"customer_name": "lower(customer_name)",
	"outlet_id":     "outlet_id",
	"id":            "id::text",
	"code":          "code",
}

// orderWhere is the one WHERE clause of the orders list and the orders summary, so the two always match the same orders. Its
// parameters are, in order, the outlets, the status, the search text, and the first and last day (Asia/Jakarta calendar days of
// created_at; empty text means no bound). Use filterArgs to fill them.
const orderWhere = `WHERE (coalesce(cardinality($1::bigint[]), 0) = 0 OR outlet_id = ANY($1)) AND ($2 = '' OR status = $2)
		AND ($3 = '' OR starts_with(id::text, lower(regexp_replace($3, '^trx-', '', 'i'))) OR strpos(lower(code), lower($3)) > 0
		OR strpos(lower(customer_name), lower($3)) > 0 OR strpos(customer_phone, $3) > 0)
		AND ($4 = '' OR (created_at AT TIME ZONE 'Asia/Jakarta')::date >= $4::date)
		AND ($5 = '' OR (created_at AT TIME ZONE 'Asia/Jakarta')::date <= $5::date)`

func filterArgs(f domain.ListFilter) []any {
	return []any{f.OutletIDs, f.Status, f.Query, f.From, f.To}
}

func (s *Store) ListOrders(ctx context.Context, f domain.ListFilter) ([]domain.Order, int64, error) {
	if s.ix.Enabled() {
		return s.searchOrders(ctx, f)
	}
	const where = orderWhere
	// An empty OutletIDs from a staff caller never reaches here (app.List
	// rejects non-staff), so "empty = all" only ever means an admin's.
	expr, ok := orderBy[f.SortBy]
	if !ok {
		expr = orderBy["created_at"]
	}
	dir := " ASC"
	if f.Desc {
		dir = " DESC"
	}
	order := expr + dir
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM orders `+where, filterArgs(f)...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("Could not list orders").WithCause(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM orders `+where+` ORDER BY `+order+`, created_at DESC, id LIMIT $6 OFFSET $7`,
		append(filterArgs(f), f.Limit, f.Offset)...)
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

func (s *Store) UpdateStatus(ctx context.Context, id, status, actor string, effect domain.StatusEffect) (domain.Order, error) {
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
		// Inside the transaction, with the order's row locked: a failure here rolls the status change back.
		if effect != nil {
			if err := effect(ctx, current, updated); err != nil {
				return err
			}
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
	s.sync(ctx, updated)
	return updated, nil
}

func emit(ctx context.Context, tx pgx.Tx, typ string, o domain.Order) error {
	lines := make([]events.OrderLine, 0, len(o.Lines))
	for _, l := range o.Lines {
		lines = append(lines, events.OrderLine{ItemID: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty})
	}
	env, err := events.New(typ, events.AggregateOrder, o.ID, o.Version, events.OrderChanged{
		ID: o.ID, Code: o.Code, OutletID: o.OutletID, CustomerName: o.CustomerName, CustomerPhone: o.CustomerPhone,
		Lines: lines, Total: o.Total, Status: o.Status, CreatedBy: o.CreatedBy,
	})
	if err != nil {
		return err
	}
	return outbox.Append(ctx, tx, env)
}

// SummarizeOrders counts and adds up the orders the filter matches in one query. Every matching order counts, whatever its status: the
// summary says exactly what the list would show. The sums are integers; the averages are worked out from them in the app.
func (s *Store) SummarizeOrders(ctx context.Context, f domain.ListFilter) (domain.SummaryCounts, error) {
	var c domain.SummaryCounts
	where, args, err := s.summaryScope(ctx, f)
	if err != nil {
		return c, err
	}
	err = s.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status = 'fulfilled'),
		       count(*) FILTER (WHERE status = 'cancelled'),
		       count(*) FILTER (WHERE status IN ('created', 'processing', 'ready')),
		       count(*),
		       COALESCE(sum(total), 0)::bigint,
		       COALESCE(sum(jsonb_array_length(lines)), 0)::bigint
		FROM orders `+where, args...).Scan(&c.Orders, &c.Fulfilled, &c.Cancelled, &c.Open, &c.Counted, &c.Value, &c.Lines)
	if err != nil {
		return c, apperr.Internal("Could not summarise orders").WithCause(err)
	}
	return c, nil
}
