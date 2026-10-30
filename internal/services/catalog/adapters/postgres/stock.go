package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// The stock ledger. Every change is one immutable row of stock_movements, written in the same transaction that updates the
// balance row it changes, with that row locked (FOR UPDATE) so concurrent movements never interleave. What a movement is
// worth is decided by the domain's valuation functions from the exact balance read under the lock.

const movementCols = `mv.id, mv.outlet_id, mv.material_id, m.sku, m.name, m.unit, mv.kind, mv.reason, mv.qty_delta::text, mv.value_delta,
	mv.balance_qty::text, mv.balance_value, mv.ref_type, mv.ref_id, COALESCE(mv.ref_code, ''), mv.supplier, mv.doc_no, mv.note, mv.created_by, mv.created_at,
	COALESCE(mv.idempotency_key, '')`

func scanMovement(r pgx.Row) (domain.StockMovement, error) {
	var mv domain.StockMovement
	var qd, bq string
	err := r.Scan(&mv.ID, &mv.OutletID, &mv.MaterialID, &mv.SKU, &mv.Name, &mv.Unit, &mv.Kind, &mv.Reason, &qd, &mv.ValueDelta, &bq,
		&mv.BalanceValue, &mv.RefType, &mv.RefID, &mv.RefCode, &mv.Supplier, &mv.DocNo, &mv.Note, &mv.CreatedBy, &mv.CreatedAt, &mv.IdempotencyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return mv, apperr.NotFound("Movement not found")
	}
	if err != nil {
		return mv, internal("Could not read the movement", err)
	}
	if mv.QtyDelta, err = ratFromText(qd); err != nil {
		return mv, internal("Could not read the movement", err)
	}
	if mv.BalanceQty, err = ratFromText(bq); err != nil {
		return mv, internal("Could not read the movement", err)
	}
	return mv, nil
}

func getMovement(ctx context.Context, q queryer, id int64) (domain.StockMovement, error) {
	return scanMovement(q.QueryRow(ctx, `SELECT `+movementCols+` FROM stock_movements mv JOIN materials m ON m.id = mv.material_id WHERE mv.id = $1`, id))
}

func movementByKey(ctx context.Context, q queryer, outlet int64, key string) (domain.StockMovement, bool, error) {
	mv, err := scanMovement(q.QueryRow(ctx, `SELECT `+movementCols+` FROM stock_movements mv JOIN materials m ON m.id = mv.material_id
		WHERE mv.outlet_id = $1 AND mv.idempotency_key = $2`, outlet, key))
	var ae *apperr.Error
	if errors.As(err, &ae) && ae.Kind == apperr.KindNotFound {
		return mv, false, nil
	}
	return mv, err == nil, err
}

// moveSpec is one movement to book. Plan is asked once the balance row is locked.
type moveSpec struct {
	Outlet, Material int64
	Kind, Reason     string
	Plan             func(b domain.StockState, std *big.Rat, unit string) (domain.Plan, error)
	RefType, RefID   string
	RefCode          string
	Supplier, DocNo  string
	Note, By, Key    string
}

// errNoChange is returned by a Plan that finds nothing to book (a count that matches the book).
var errNoChange = errors.New("stock: nothing to book")

// applyMove books one movement inside tx and returns it; created is false when the idempotency key had already been used.
func applyMove(ctx context.Context, tx pgx.Tx, m moveSpec) (domain.StockMovement, bool, error) {
	if m.Key != "" {
		if mv, ok, err := movementByKey(ctx, tx, m.Outlet, m.Key); err != nil || ok {
			return mv, false, err
		}
	}
	var price int64
	var buyQty, unit string
	if err := tx.QueryRow(ctx, `SELECT purchase_price, purchase_qty::text, unit FROM materials WHERE id = $1`, m.Material).Scan(&price, &buyQty, &unit); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StockMovement{}, false, apperr.Invalid(fmt.Sprintf("Material %d does not exist", m.Material))
		}
		return domain.StockMovement{}, false, err
	}
	bq, err := ratFromText(buyQty)
	if err != nil {
		return domain.StockMovement{}, false, err
	}
	std := domain.Material{PurchasePrice: price, PurchaseQty: bq}.UnitCost()

	// The balance row is created if it is missing, then locked: a second movement on it waits here for the first to commit.
	if _, err := tx.Exec(ctx, `INSERT INTO stock_balances (outlet_id, material_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, m.Outlet, m.Material); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23503" {
			return domain.StockMovement{}, false, apperr.Invalid(fmt.Sprintf("Outlet %d does not exist", m.Outlet))
		}
		return domain.StockMovement{}, false, err
	}
	var qtyText string
	var value int64
	if err := tx.QueryRow(ctx, `SELECT qty::text, value FROM stock_balances WHERE outlet_id = $1 AND material_id = $2 FOR UPDATE`, m.Outlet, m.Material).Scan(&qtyText, &value); err != nil {
		return domain.StockMovement{}, false, err
	}
	// A retry that raced the first request finds its key only now, once the first has committed and released the row.
	if m.Key != "" {
		if mv, ok, err := movementByKey(ctx, tx, m.Outlet, m.Key); err != nil || ok {
			return mv, false, err
		}
	}
	qty, err := ratFromText(qtyText)
	if err != nil {
		return domain.StockMovement{}, false, err
	}
	state := domain.StockState{Qty: qty, Value: value}
	plan, err := m.Plan(state, std, unit)
	if err != nil {
		return domain.StockMovement{}, false, err
	}
	after := state.After(plan)
	if _, err := tx.Exec(ctx, `UPDATE stock_balances SET qty = $3::numeric, value = $4, updated_at = now() WHERE outlet_id = $1 AND material_id = $2`,
		m.Outlet, m.Material, domain.FormatRat(after.Qty, 4), after.Value); err != nil {
		return domain.StockMovement{}, false, err
	}
	var key *string
	if m.Key != "" {
		key = &m.Key
	}
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO stock_movements (outlet_id, material_id, kind, reason, qty_delta, value_delta, balance_qty, balance_value, ref_type, ref_id,
		                             ref_code, supplier, doc_no, note, created_by, idempotency_key)
		VALUES ($1, $2, $3, $4, $5::numeric, $6, $7::numeric, $8, $9, $10, NULLIF($11, ''), $12, $13, $14, $15, $16) RETURNING id`,
		m.Outlet, m.Material, m.Kind, m.Reason, domain.FormatRat(plan.QtyDelta, 4), plan.ValueDelta, domain.FormatRat(after.Qty, 4), after.Value,
		m.RefType, m.RefID, m.RefCode, m.Supplier, m.DocNo, m.Note, m.By, key).Scan(&id); err != nil {
		return domain.StockMovement{}, false, err
	}
	mv, err := getMovement(ctx, tx, id)
	return mv, true, err
}

func isDuplicateKey(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "stock_movements_idempotency_key"
}

func wrapStock(err error, msg string) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23503" {
		return apperr.Invalid("The outlet or the material does not exist")
	}
	return apperr.Internal(msg).WithCause(err)
}

// move books one movement in its own transaction.
func (s *Store) move(ctx context.Context, m moveSpec) (domain.StockMovement, bool, error) {
	var mv domain.StockMovement
	var created bool
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		mv, created, err = applyMove(ctx, tx, m)
		return err
	})
	if isDuplicateKey(err) && m.Key != "" {
		if got, ok, e := movementByKey(ctx, s.pool, m.Outlet, m.Key); e == nil && ok {
			return got, false, nil
		}
	}
	return mv, created, err
}

func (s *Store) ReceiveStock(ctx context.Context, r domain.StockReceipt, by string) (domain.StockMovement, bool, error) {
	mv, created, err := s.move(ctx, moveSpec{
		Outlet: r.OutletID, Material: r.MaterialID, Kind: domain.KindReceipt,
		Plan: func(b domain.StockState, _ *big.Rat, _ string) (domain.Plan, error) {
			return domain.PlanReceipt(b, r.Qty, r.TotalCost), nil
		},
		Supplier: r.Supplier, DocNo: r.DocNo, Note: r.Note, By: by, Key: r.Key,
	})
	return mv, created, wrapStock(err, "Could not record the receipt")
}

func (s *Store) WasteStock(ctx context.Context, w domain.StockWaste, by string) (domain.StockMovement, bool, error) {
	mv, created, err := s.move(ctx, moveSpec{
		Outlet: w.OutletID, Material: w.MaterialID, Kind: domain.KindWaste, Reason: w.Reason,
		Plan: func(b domain.StockState, std *big.Rat, unit string) (domain.Plan, error) {
			return domain.PlanOutManual(b, w.Qty, std, unit)
		},
		Note: w.Note, By: by, Key: w.Key,
	})
	return mv, created, wrapStock(err, "Could not record the waste")
}

// AdjustStock books a stock count: counted minus book. Nothing is booked (and nil returned) when they are equal.
func (s *Store) AdjustStock(ctx context.Context, a domain.StockAdjust, by string) (*domain.StockMovement, bool, error) {
	mv, created, err := s.move(ctx, moveSpec{
		Outlet: a.OutletID, Material: a.MaterialID, Kind: domain.KindAdjustment, Reason: domain.ReasonCountCorrection,
		Plan: func(b domain.StockState, std *big.Rat, _ string) (domain.Plan, error) {
			delta := new(big.Rat).Sub(a.CountedQty, b.Qty)
			switch delta.Sign() {
			case 0:
				return domain.Plan{}, errNoChange
			case 1:
				return domain.PlanAdjustUp(b, delta, std), nil
			}
			return domain.PlanOut(b, new(big.Rat).Neg(delta), std), nil
		},
		Note: a.Note, By: by, Key: a.Key,
	})
	if errors.Is(err, errNoChange) {
		return nil, false, nil
	}
	return &mv, created, wrapStock(err, "Could not record the count")
}

// TransferStock moves stock between two outlets: a transfer_out and a transfer_in with the same ref, in one transaction. The
// stock arrives worth exactly what left. Both balance rows are locked in outlet order, so two opposite transfers cannot deadlock.
func (s *Store) TransferStock(ctx context.Context, t domain.StockTransfer, by string) (out, in domain.StockMovement, created bool, err error) {
	err = postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if t.Key != "" {
			if o, ok, e := movementByKey(ctx, tx, t.FromOutletID, t.Key); e != nil {
				return e
			} else if ok {
				if i, e := scanMovement(tx.QueryRow(ctx, `SELECT `+movementCols+` FROM stock_movements mv JOIN materials m ON m.id = mv.material_id
					WHERE mv.ref_type = 'transfer' AND mv.ref_id = $1 AND mv.kind = 'transfer_in'`, o.RefID)); e != nil {
					return e
				} else {
					out, in, created = o, i, false
					return nil
				}
			}
		}
		for _, o := range []int64{t.FromOutletID, t.ToOutletID} {
			if _, e := tx.Exec(ctx, `INSERT INTO stock_balances (outlet_id, material_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, o, t.MaterialID); e != nil {
				var pg *pgconn.PgError
				if errors.As(e, &pg) && pg.Code == "23503" {
					return apperr.Invalid(fmt.Sprintf("Outlet %d or material %d does not exist", o, t.MaterialID))
				}
				return e
			}
		}
		lock := []int64{t.FromOutletID, t.ToOutletID}
		sort.Slice(lock, func(i, j int) bool { return lock[i] < lock[j] })
		if _, e := tx.Exec(ctx, `SELECT 1 FROM stock_balances WHERE material_id = $1 AND outlet_id = ANY($2) ORDER BY outlet_id FOR UPDATE`, t.MaterialID, lock); e != nil {
			return e
		}
		ref := uuid.NewString()
		var e error
		out, _, e = applyMove(ctx, tx, moveSpec{
			Outlet: t.FromOutletID, Material: t.MaterialID, Kind: domain.KindTransferOut, RefType: domain.RefTransfer, RefID: ref,
			Plan: func(b domain.StockState, std *big.Rat, unit string) (domain.Plan, error) {
				return domain.PlanOutManual(b, t.Qty, std, unit)
			},
			Note: t.Note, By: by, Key: t.Key,
		})
		if e != nil {
			return e
		}
		in, _, e = applyMove(ctx, tx, moveSpec{
			Outlet: t.ToOutletID, Material: t.MaterialID, Kind: domain.KindTransferIn, RefType: domain.RefTransfer, RefID: ref,
			Plan: func(_ domain.StockState, _ *big.Rat, _ string) (domain.Plan, error) {
				return domain.Plan{QtyDelta: new(big.Rat).Set(t.Qty), ValueDelta: -out.ValueDelta}, nil
			},
			Note: t.Note, By: by, Key: t.Key,
		})
		created = true
		return e
	})
	return out, in, created, wrapStock(err, "Could not record the transfer")
}

// ConsumeOrder takes an order's materials out of its outlet's stock: one consumption per material, from the quantities the recipes
// of the lines add up to (see domain.OrderUse). Idempotent: a material already consumed for the order is skipped.
func (s *Store) ConsumeOrder(ctx context.Context, o domain.StockOrder, by string) (int, error) {
	contexts := map[int64]domain.CostingContext{}
	for _, l := range o.Lines {
		if _, done := contexts[l.ServiceID]; done {
			continue
		}
		c, err := s.LoadCostingContext(ctx, l.ServiceID)
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Kind == apperr.KindNotFound {
			continue // a service that no longer exists consumes nothing
		}
		if err != nil {
			return 0, err
		}
		contexts[l.ServiceID] = c
	}
	use, err := domain.OrderUse(contexts, o)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, 0, len(use))
	for id := range use {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	n := 0
	err = postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		for _, id := range ids {
			qty := use[id]
			_, created, err := applyMove(ctx, tx, moveSpec{
				Outlet: o.OutletID, Material: id, Kind: domain.KindConsumption, RefType: domain.RefOrder, RefID: o.OrderID, RefCode: o.Code,
				// A sale is never blocked: the balance may go negative, valued at the standard cost for what is not there.
				Plan: func(b domain.StockState, std *big.Rat, _ string) (domain.Plan, error) {
					return domain.PlanOut(b, qty, std), nil
				},
				By: by, Key: fmt.Sprintf("order:%s:consume:%d", o.OrderID, id),
			})
			if err != nil {
				return err
			}
			if created {
				n++
			}
		}
		return nil
	})
	return n, wrapStock(err, "Could not record the consumption")
}

// ReverseOrder puts back what ConsumeOrder took: one reversal per consumption movement of the order, restoring exactly the
// quantity and the value that consumption moved. An order that was never consumed has nothing to reverse.
func (s *Store) ReverseOrder(ctx context.Context, o domain.StockOrder, by string) (int, error) {
	n := 0
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT material_id, qty_delta::text, value_delta FROM stock_movements
			WHERE outlet_id = $1 AND kind = 'consumption' AND ref_type = 'order' AND ref_id = $2 ORDER BY material_id`, o.OutletID, o.OrderID)
		if err != nil {
			return err
		}
		type taken struct {
			material int64
			qty      *big.Rat
			value    int64
		}
		var list []taken
		for rows.Next() {
			var t taken
			var q string
			if err := rows.Scan(&t.material, &q, &t.value); err != nil {
				rows.Close()
				return err
			}
			if t.qty, err = ratFromText(q); err != nil {
				rows.Close()
				return err
			}
			list = append(list, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, t := range list {
			_, created, err := applyMove(ctx, tx, moveSpec{
				Outlet: o.OutletID, Material: t.material, Kind: domain.KindReversal, RefType: domain.RefOrder, RefID: o.OrderID, RefCode: o.Code,
				Plan: func(_ domain.StockState, _ *big.Rat, _ string) (domain.Plan, error) {
					return domain.Plan{QtyDelta: new(big.Rat).Neg(t.qty), ValueDelta: -t.value}, nil
				},
				By: by, Key: fmt.Sprintf("order:%s:reverse:%d", o.OrderID, t.material),
			})
			if err != nil {
				return err
			}
			if created {
				n++
			}
		}
		return nil
	})
	return n, wrapStock(err, "Could not reverse the consumption")
}

// SetMinStock sets the reorder level of a material at an outlet, creating an empty balance row if there is none yet.
func (s *Store) SetMinStock(ctx context.Context, outlet, material int64, min *big.Rat) (domain.StockRow, error) {
	var row domain.StockRow
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_balances (outlet_id, material_id, min_qty) VALUES ($1, $2, $3::numeric)
			ON CONFLICT (outlet_id, material_id) DO UPDATE SET min_qty = EXCLUDED.min_qty, updated_at = now()`, outlet, material, domain.FormatRat(min, 4)); err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.Code == "23503" {
				return apperr.Invalid("The outlet or the material does not exist")
			}
			return err
		}
		rows, _, err := listStock(ctx, tx, domain.StockQuery{OutletID: outlet, Limit: 1}, material)
		if err != nil {
			return err
		}
		if len(rows) == 1 {
			row = rows[0]
		}
		return nil
	})
	return row, wrapStock(err, "Could not set the minimum")
}

// ---- reading ----

var stockOrder = map[string]string{"sku": "lower(m.sku)", "name": "lower(m.name)", "qty": "COALESCE(b.qty, 0)", "value": "COALESCE(b.value, 0)"}

func (s *Store) ListStock(ctx context.Context, q domain.StockQuery) ([]domain.StockRow, int64, error) {
	return listStock(ctx, s.pool, q, 0)
}

// listStock lists balances. With an outlet every active material appears (with 0 when it has no balance yet, so receiving can
// start from the list), and an inactive one only when it has a balance row; without one, only the balance rows of every outlet.
// onlyMaterial, when set, narrows the list to that material.
func listStock(ctx context.Context, q queryer, f domain.StockQuery, onlyMaterial int64) ([]domain.StockRow, int64, error) {
	from := `FROM stock_balances b JOIN materials m ON m.id = b.material_id`
	scope := ``
	args := []any{f.Q, f.Low, onlyMaterial}
	outletSel := `b.outlet_id`
	if f.OutletID > 0 {
		args = append(args, f.OutletID)
		from = `FROM materials m LEFT JOIN stock_balances b ON b.material_id = m.id AND b.outlet_id = $4`
		scope = ` AND (m.active OR b.material_id IS NOT NULL)`
		outletSel = `$4::bigint`
	}
	where := `($1 = '' OR strpos(lower(m.sku), lower($1)) > 0 OR strpos(lower(m.name), lower($1)) > 0)
		AND (NOT $2 OR (COALESCE(b.min_qty, 0) > 0 AND COALESCE(b.qty, 0) <= COALESCE(b.min_qty, 0)))
		AND ($3 = 0 OR m.id = $3)` + scope
	var total int64
	if err := q.QueryRow(ctx, `SELECT count(*) `+from+` WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, internal("Could not list stock", err)
	}
	dir := "ASC"
	if f.SortDir == "desc" {
		dir = "DESC"
	}
	by, ok := stockOrder[f.SortBy]
	if !ok {
		by = stockOrder["sku"]
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 10
	}
	args = append(args, limit, f.Offset)
	rows, err := q.Query(ctx, fmt.Sprintf(`
		SELECT %s, m.id, m.sku, m.name, m.unit, COALESCE(b.qty, 0)::text, COALESCE(b.value, 0), COALESCE(b.min_qty, 0)::text, m.purchase_price, m.purchase_qty::text
		%s WHERE %s ORDER BY %s %s, %s ASC, m.id ASC LIMIT $%d OFFSET $%d`,
		outletSel, from, where, by, dir, outletSel, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, internal("Could not list stock", err)
	}
	defer rows.Close()
	out := []domain.StockRow{}
	for rows.Next() {
		var r domain.StockRow
		var qty, min, buyQty string
		var price int64
		if err := rows.Scan(&r.OutletID, &r.MaterialID, &r.SKU, &r.Name, &r.Unit, &qty, &r.Value, &min, &price, &buyQty); err != nil {
			return nil, 0, internal("Could not list stock", err)
		}
		var e1, e2, e3 error
		var bq *big.Rat
		r.Qty, e1 = ratFromText(qty)
		r.MinQty, e2 = ratFromText(min)
		bq, e3 = ratFromText(buyQty)
		for _, e := range []error{e1, e2, e3} {
			if e != nil {
				return nil, 0, internal("Could not list stock", e)
			}
		}
		r.StdUnitCost = domain.Material{PurchasePrice: price, PurchaseQty: bq}.UnitCost()
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *Store) ListMovements(ctx context.Context, f domain.MovementQuery) ([]domain.StockMovement, int64, error) {
	// Days are Asia/Jakarta calendar days, both ends included.
	where := `($1 = 0 OR mv.outlet_id = $1) AND ($2 = 0 OR mv.material_id = $2) AND ($3 = '' OR mv.kind = $3)
		AND ($4 = '' OR strpos(lower(mv.note), lower($4)) > 0 OR strpos(lower(mv.doc_no), lower($4)) > 0 OR strpos(lower(mv.supplier), lower($4)) > 0
		     OR strpos(lower(COALESCE(mv.ref_code, '')), lower($4)) > 0)
		AND ($5 = '' OR mv.created_at >= ($5::date)::timestamp AT TIME ZONE 'Asia/Jakarta')
		AND ($6 = '' OR mv.created_at < (($6::date) + 1)::timestamp AT TIME ZONE 'Asia/Jakarta')`
	args := []any{f.OutletID, f.MaterialID, f.Kind, f.Q, f.From, f.To}
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stock_movements mv WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, internal("Could not list movements", err)
	}
	dir := "DESC"
	if f.SortDir == "asc" {
		dir = "ASC"
	}
	by := "mv.created_at"
	if f.SortBy == "id" {
		by = "mv.id"
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM stock_movements mv JOIN materials m ON m.id = mv.material_id WHERE %s ORDER BY %s %s, mv.id %s LIMIT $7 OFFSET $8`,
		movementCols, where, by, dir, dir), args...)
	if err != nil {
		return nil, 0, internal("Could not list movements", err)
	}
	defer rows.Close()
	out := []domain.StockMovement{}
	for rows.Next() {
		mv, err := scanMovement(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, mv)
	}
	return out, total, rows.Err()
}
