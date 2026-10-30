package postgres

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// purgeStock removes the stock rows of test materials. The ledger refuses deletes, so for these few statements the trigger is
// switched off; only tests do this, on materials they made.
func purgeStock(s *Store, materialIDs ...int64) {
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{
		`ALTER TABLE stock_movements DISABLE TRIGGER stock_movements_immutable`,
		`DELETE FROM stock_movements WHERE material_id = ANY($1)`,
		`ALTER TABLE stock_movements ENABLE TRIGGER stock_movements_immutable`,
		`DELETE FROM stock_balances WHERE material_id = ANY($1)`,
	} {
		if strings.Contains(q, "$1") {
			_, _ = tx.Exec(ctx, q, materialIDs)
		} else {
			_, _ = tx.Exec(ctx, q)
		}
	}
	_ = tx.Commit(ctx)
}

const outletA, outletB = 1, 2

func receive(t *testing.T, s *Store, outlet, material int64, qty string, cost int64, key string) (domain.StockMovement, bool) {
	t.Helper()
	r, err := (domain.StockReceiptInput{OutletID: outlet, MaterialID: material, Qty: qty, TotalCost: cost, Key: key}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	mv, created, err := s.ReceiveStock(context.Background(), r, "tester")
	if err != nil {
		t.Fatal(err)
	}
	return mv, created
}

func waste(t *testing.T, s *Store, outlet, material int64, qty string) (domain.StockMovement, error) {
	t.Helper()
	w, err := (domain.StockWasteInput{OutletID: outlet, MaterialID: material, Qty: qty, Reason: "spilled"}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	mv, _, err := s.WasteStock(context.Background(), w, "tester")
	return mv, err
}

func balance(t *testing.T, s *Store, outlet, material int64) (qty string, value int64) {
	t.Helper()
	rows, _, err := s.ListStock(context.Background(), domain.StockQuery{OutletID: outlet, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.MaterialID == material {
			return domain.FormatExact(r.Qty, 4), r.Value
		}
	}
	t.Fatalf("no row for material %d at outlet %d", material, outlet)
	return
}

func TestStockMovingAverageExactInTheDatabase(t *testing.T) {
	s := testStore(t)
	tag := strings.ReplaceAll(unique("SA"), " ", "")
	m := mustMaterial(t, s, tag, "ml", 100000, "3000") // standard cost 100/3
	// 3 ml worth Rp 100, then one out at a time: 33, 34 (33.5 half away), 33: the three add up to exactly 100.
	receive(t, s, outletA, m.ID, "3", 100, "")
	for i, want := range []int64{-33, -34, -33} {
		mv, err := waste(t, s, outletA, m.ID, "1")
		if err != nil || mv.ValueDelta != want {
			t.Fatalf("step %d: %+v, %v; want value_delta %d", i+1, mv, err, want)
		}
	}
	if qty, value := balance(t, s, outletA, m.ID); qty != "0" || value != 0 {
		t.Errorf("balance = %s / %d; want 0 / 0", qty, value)
	}
	// The ledger carries the balance after each movement and it adds up.
	list, total, err := s.ListMovements(context.Background(), mustMovementQuery(t, domain.MovementQuery{OutletID: outletA, MaterialID: m.ID, SortBy: "id", SortDir: "asc"}))
	if err != nil || total != 4 {
		t.Fatalf("movements = %d (total %d), %v", len(list), total, err)
	}
	var sum int64
	for _, mv := range list {
		sum += mv.ValueDelta
		if mv.BalanceValue != sum {
			t.Errorf("movement %d: balance_value %d but the movements so far add up to %d", mv.ID, mv.BalanceValue, sum)
		}
	}
	// Nothing left to write off, and the refusal says how much there is.
	if _, err := waste(t, s, outletA, m.ID, "1"); kindOf(err) != apperr.KindInvalid || err.Error() != "Only 0 ml in stock" {
		t.Errorf("waste from nothing: %v", err)
	}
}

func mustMovementQuery(t *testing.T, q domain.MovementQuery) domain.MovementQuery {
	t.Helper()
	q.Limit = 100
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestStockReceiptIdempotency(t *testing.T) {
	s := testStore(t)
	tag := strings.ReplaceAll(unique("SI"), " ", "")
	m := mustMaterial(t, s, tag, "g", 1000, "100")
	first, created := receive(t, s, outletA, m.ID, "250", 5000, "key-"+tag)
	again, createdAgain := receive(t, s, outletA, m.ID, "250", 5000, "key-"+tag)
	if !created || createdAgain || first.ID != again.ID {
		t.Errorf("created %v/%v, ids %d/%d; want a retry to return the original", created, createdAgain, first.ID, again.ID)
	}
	if qty, value := balance(t, s, outletA, m.ID); qty != "250" || value != 5000 {
		t.Errorf("a retry booked it twice: %s / %d", qty, value)
	}
	// The same key at another outlet is another request.
	if _, c := receive(t, s, outletB, m.ID, "10", 100, "key-"+tag); !c {
		t.Error("a key is per outlet")
	}
	// Waste and transfers are idempotent too.
	w, _ := (domain.StockWasteInput{OutletID: outletA, MaterialID: m.ID, Qty: "50", Reason: "expired", Key: "w-" + tag}).Parse()
	w1, c1, _ := s.WasteStock(context.Background(), w, "tester")
	w2, c2, _ := s.WasteStock(context.Background(), w, "tester")
	if !c1 || c2 || w1.ID != w2.ID {
		t.Errorf("waste retry: %v %v %d %d", c1, c2, w1.ID, w2.ID)
	}
	if qty, value := balance(t, s, outletA, m.ID); qty != "200" || value != 4000 {
		t.Errorf("after the waste: %s / %d; want 200 / 4000", qty, value)
	}
}

func TestStockConcurrentMovementsDoNotInterleave(t *testing.T) {
	s := testStore(t)
	tag := strings.ReplaceAll(unique("SC"), " ", "")
	m := mustMaterial(t, s, tag, "pcs", 7, "1")
	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := (domain.StockReceiptInput{OutletID: outletA, MaterialID: m.ID, Qty: "1", TotalCost: 7}).Parse()
			if _, _, err := s.ReceiveStock(context.Background(), r, "tester"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if qty, value := balance(t, s, outletA, m.ID); qty != fmt.Sprint(workers) || value != 7*workers {
		t.Errorf("balance = %s / %d; want %d / %d", qty, value, workers, 7*workers)
	}
	// Each movement saw the balance the one before it left: 1, 2, 3 ... with no repeats and no gaps.
	list, _, err := s.ListMovements(context.Background(), mustMovementQuery(t, domain.MovementQuery{OutletID: outletA, MaterialID: m.ID, SortBy: "id", SortDir: "asc"}))
	if err != nil || len(list) != workers {
		t.Fatalf("%d movements, %v", len(list), err)
	}
	for i, mv := range list {
		if mv.BalanceQty.RatString() != fmt.Sprint(i+1) || mv.BalanceValue != int64(7*(i+1)) {
			t.Fatalf("movement %d: balance %s / %d; want %d / %d (interleaved)", i+1, mv.BalanceQty.RatString(), mv.BalanceValue, i+1, 7*(i+1))
		}
	}
	// And a crowd taking it out again, ending on exactly zero value.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = waste(t, s, outletA, m.ID, "1")
		}()
	}
	wg.Wait()
	if qty, value := balance(t, s, outletA, m.ID); qty != "0" || value != 0 {
		t.Errorf("after the waste crowd: %s / %d; want 0 / 0", qty, value)
	}
}

func TestStockTransferMovesTheValue(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("ST"), " ", "")
	m := mustMaterial(t, s, tag, "ml", 100000, "3000")
	receive(t, s, outletA, m.ID, "3", 100, "")
	in := domain.StockTransferInput{FromOutletID: outletA, ToOutletID: outletB, MaterialID: m.ID, Qty: "1", Key: "t-" + tag}
	tr, _ := in.Parse()
	out, inn, created, err := s.TransferStock(ctx, tr, "tester")
	if err != nil || !created || out.Kind != domain.KindTransferOut || inn.Kind != domain.KindTransferIn || out.RefID == "" || out.RefID != inn.RefID || out.RefType != "transfer" {
		t.Fatalf("transfer = %+v / %+v, created %v, %v", out, inn, created, err)
	}
	if out.ValueDelta != -33 || inn.ValueDelta != 33 || inn.QtyDelta.RatString() != "1" {
		t.Errorf("moved %d out and %d in; want -33 and 33", out.ValueDelta, inn.ValueDelta)
	}
	if qty, value := balance(t, s, outletA, m.ID); qty != "2" || value != 67 {
		t.Errorf("source = %s / %d; want 2 / 67", qty, value)
	}
	if qty, value := balance(t, s, outletB, m.ID); qty != "1" || value != 33 {
		t.Errorf("destination = %s / %d; want 1 / 33", qty, value)
	}
	// A retry returns the same pair and moves nothing.
	out2, in2, created2, err := s.TransferStock(ctx, tr, "tester")
	if err != nil || created2 || out2.ID != out.ID || in2.ID != inn.ID {
		t.Errorf("retry = %d/%d created %v, %v", out2.ID, in2.ID, created2, err)
	}
	if qty, _ := balance(t, s, outletA, m.ID); qty != "2" {
		t.Errorf("a retry moved it again: %s", qty)
	}
	// More than the source has is refused, and nothing moves.
	tooMuch := domain.StockTransfer{FromOutletID: outletA, ToOutletID: outletB, MaterialID: m.ID, Qty: new(big.Rat).SetInt64(5)}
	if _, _, _, err := s.TransferStock(ctx, tooMuch, "tester"); kindOf(err) != apperr.KindInvalid || err.Error() != "Only 2 ml in stock" {
		t.Errorf("too much: %v", err)
	}
	if qty, _ := balance(t, s, outletB, m.ID); qty != "1" {
		t.Errorf("the destination changed on a refused transfer: %s", qty)
	}
	// An outlet that does not exist.
	missing := domain.StockTransfer{FromOutletID: outletA, ToOutletID: 987654, MaterialID: m.ID, Qty: new(big.Rat).SetInt64(1)}
	if _, _, _, err := s.TransferStock(ctx, missing, "tester"); kindOf(err) != apperr.KindInvalid {
		t.Errorf("unknown outlet: %v", err)
	}
	// Two opposite transfers at once must not deadlock.
	receive(t, s, outletB, m.ID, "10", 500, "")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, b := int64(outletA), int64(outletB)
			if i%2 == 1 {
				a, b = b, a
			}
			_, _, _, _ = s.TransferStock(ctx, domain.StockTransfer{FromOutletID: a, ToOutletID: b, MaterialID: m.ID, Qty: new(big.Rat).SetInt64(1)}, "tester")
		}(i)
	}
	wg.Wait()
}

func TestStockCountAdjustment(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("SJ"), " ", "")
	m := mustMaterial(t, s, tag, "ml", 100000, "3000")
	receive(t, s, outletA, m.ID, "4", 100, "")
	count := func(counted string) (*domain.StockMovement, error) {
		a, err := (domain.StockAdjustInput{OutletID: outletA, MaterialID: m.ID, CountedQty: counted, Note: "monthly count"}).Parse()
		if err != nil {
			t.Fatal(err)
		}
		mv, _, err := s.AdjustStock(ctx, a, "tester")
		return mv, err
	}
	if mv, err := count("4"); err != nil || mv != nil {
		t.Errorf("a count equal to the book books nothing: %+v, %v", mv, err)
	}
	up, err := count("6") // found 2 more, at the average 25 each
	if err != nil || up == nil || up.Kind != domain.KindAdjustment || up.Reason != "count_correction" || up.QtyDelta.RatString() != "2" || up.ValueDelta != 50 {
		t.Fatalf("count up = %+v, %v", up, err)
	}
	down, err := count("1.5")
	// 6 ml worth 150 (25 each): 4.5 ml out is 112.5 -> 113.
	if err != nil || down.QtyDelta.RatString() != "-9/2" || down.ValueDelta != -113 {
		t.Fatalf("count down = %+v, %v", down, err)
	}
	if mv, err := count("0"); err != nil || mv == nil || mv.BalanceValue != 0 {
		t.Errorf("counting zero clears the value: %+v, %v", mv, err)
	}
}

func TestStockLedgerIsImmutable(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("SM"), " ", "")
	m := mustMaterial(t, s, tag, "ml", 100, "1")
	mv, _ := receive(t, s, outletA, m.ID, "5", 50, "")
	if _, err := s.pool.Exec(ctx, `UPDATE stock_movements SET note = 'edited' WHERE id = $1`, mv.ID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("update: %v; want it refused", err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM stock_movements WHERE id = $1`, mv.ID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("delete: %v; want it refused", err)
	}
	// A material with a ledger cannot be deleted or change its unit.
	if err := s.DeleteMaterial(ctx, m.ID); kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), "stock history") {
		t.Errorf("delete a material with stock history: %v", err)
	}
	upd := m
	upd.Unit = "g"
	if _, err := s.UpdateMaterial(ctx, upd); kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), "stock movements") {
		t.Errorf("change its unit: %v", err)
	}
}

func TestStockListLowAndNoBalanceRows(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("SL"), " ", "")
	a := mustMaterial(t, s, tag+"-A", "ml", 100, "1")
	b := mustMaterial(t, s, tag+"-B", "ml", 100, "1")
	receive(t, s, outletA, a.ID, "10", 100, "")
	if _, err := s.SetMinStock(ctx, outletA, a.ID, big.NewRat(10, 1)); err != nil {
		t.Fatal(err)
	}
	list := func(f domain.StockQuery) ([]domain.StockRow, int64) {
		t.Helper()
		f.Q = tag
		if err := f.Validate(); err != nil {
			t.Fatal(err)
		}
		rows, total, err := s.ListStock(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return rows, total
	}
	// With an outlet, a material with no balance yet is listed with 0 so receiving can start from the list.
	rows, total := list(domain.StockQuery{OutletID: outletA, SortBy: "sku"})
	if total != 2 || len(rows) != 2 || rows[1].MaterialID != b.ID || rows[1].Qty.Sign() != 0 || rows[1].OutletID != outletA {
		t.Fatalf("with an outlet: %+v (total %d)", rows, total)
	}
	if !rows[0].Low() || rows[1].Low() {
		t.Errorf("low flags: %v %v; want true false (10 <= 10, and no level set on B)", rows[0].Low(), rows[1].Low())
	}
	// Without one, only the balances that exist, each with its outlet.
	rows, total = list(domain.StockQuery{})
	if total != 1 || rows[0].MaterialID != a.ID || rows[0].OutletID != outletA {
		t.Errorf("without an outlet: %+v (total %d)", rows, total)
	}
	if rows, total = list(domain.StockQuery{OutletID: outletA, Low: true}); total != 1 || rows[0].MaterialID != a.ID {
		t.Errorf("low only: %+v (total %d)", rows, total)
	}
	if rows, total = list(domain.StockQuery{OutletID: outletA, SortBy: "qty", SortDir: "desc", Limit: 1, Offset: 1}); total != 2 || len(rows) != 1 || rows[0].MaterialID != b.ID {
		t.Errorf("second page by qty: %+v (total %d)", rows, total)
	}
	// The reorder level can be cleared again and an unknown outlet is refused.
	if row, err := s.SetMinStock(ctx, outletA, a.ID, new(big.Rat)); err != nil || row.Low() {
		t.Errorf("clearing the level: %+v, %v", row, err)
	}
	if _, err := s.SetMinStock(ctx, 987654, a.ID, new(big.Rat)); kindOf(err) != apperr.KindInvalid {
		t.Errorf("unknown outlet: %v", err)
	}
}

func TestStockMovementListFilters(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("SF"), " ", "")
	m := mustMaterial(t, s, tag, "ml", 100, "1")
	r, _ := (domain.StockReceiptInput{OutletID: outletA, MaterialID: m.ID, Qty: "10", TotalCost: 100, Supplier: "Toko Sumber Jaya", DocNo: "INV-" + tag, Note: "restock"}).Parse()
	if _, _, err := s.ReceiveStock(ctx, r, "tester"); err != nil {
		t.Fatal(err)
	}
	waste(t, s, outletA, m.ID, "2")
	q := func(f domain.MovementQuery) ([]domain.StockMovement, int64) {
		t.Helper()
		f.MaterialID = m.ID
		out, total, err := s.ListMovements(ctx, mustMovementQuery(t, f))
		if err != nil {
			t.Fatal(err)
		}
		return out, total
	}
	if out, total := q(domain.MovementQuery{}); total != 2 || out[0].Kind != domain.KindWaste { // newest first
		t.Errorf("default order: %d movements, first %v", total, out)
	}
	if out, total := q(domain.MovementQuery{SortDir: "asc"}); total != 2 || out[0].Kind != domain.KindReceipt {
		t.Errorf("oldest first: %v", out)
	}
	if _, total := q(domain.MovementQuery{Kind: domain.KindReceipt}); total != 1 {
		t.Errorf("by kind: %d", total)
	}
	for _, text := range []string{"sumber jaya", "INV-" + strings.ToLower(tag), "RESTOCK"} {
		if _, total := q(domain.MovementQuery{Q: text}); total != 1 {
			t.Errorf("search %q: %d; want 1", text, total)
		}
	}
	if _, total := q(domain.MovementQuery{Q: "%"}); total != 0 {
		t.Errorf("a wildcard matched %d", total)
	}
	if out, total := q(domain.MovementQuery{Limit: 1, Offset: 1, SortDir: "asc"}); total != 2 || len(out) != 1 || out[0].Kind != domain.KindWaste {
		t.Errorf("second page: %v (total %d)", out, total)
	}
	// Days are Jakarta days: today is inside, a day long ago and a day far ahead are not.
	var today string
	_ = s.pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Asia/Jakarta')::date::text`).Scan(&today)
	if _, total := q(domain.MovementQuery{From: today, To: today}); total != 2 {
		t.Errorf("today: %d", total)
	}
	if _, total := q(domain.MovementQuery{From: "2000-01-01", To: "2000-12-31"}); total != 0 {
		t.Errorf("long ago: %d", total)
	}
	if _, total := q(domain.MovementQuery{From: "2999-01-01"}); total != 0 {
		t.Errorf("far ahead: %d", total)
	}
}

// TestOrderConsumptionAndReversal books an order against stock worked out on paper.
//
//	Material A: Rp 100.000 buys 3.000 ml (standard cost 100/3). 6.000 ml are on hand, worth Rp 200.000 (average 100/3).
//	Material B: Rp 70 a pcs. 10 pcs are on hand, worth Rp 700.
//	Service: 30 ml of A per kg. Express: 3 ml of A per kg. Lavender: 1 pcs of B once per order line.
//	An order of 2 kg with Express and Lavender uses 66 ml of A (value 66 x 100/3 = 2.200) and 1 pcs of B (Rp 70).
func TestOrderConsumptionAndReversal(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("OC"), " ", "")
	a := mustMaterial(t, s, tag+"-A", "ml", 100000, "3000")
	b := mustMaterial(t, s, tag+"-B", "pcs", 70, "1")
	vg, err := s.CreateVariantGroup(ctx, domain.VariantGroup{Name: tag, Options: []domain.VariantOption{{Name: "Reguler", Active: true, IsDefault: true}, {Name: "Express", PriceDelta: 5000, Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	mg, err := s.CreateModifierGroup(ctx, domain.ModifierGroup{Name: tag, Options: []domain.ModifierOption{{Name: "Lavender", Price: 2000, Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Test svc"), Unit: "kg", Price: 10000, Active: true, VariantGroupIDs: []int64{vg.ID}, ModifierGroupIDs: []int64{mg.ID}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM bom_lines WHERE material_id = ANY($1)`, []int64{a.ID, b.ID})
		_, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM variant_groups WHERE id = $1`, vg.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM modifier_groups WHERE id = $1`, mg.ID)
	})
	express, lavender := vg.Options[1].ID, mg.Options[0].ID
	setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: a.ID, Qty: "30", Per: "unit"})
	setBOM(t, s, "variant_option", express, domain.BOMLineInput{MaterialID: a.ID, Qty: "3", Per: "unit"})
	setBOM(t, s, "modifier_option", lavender, domain.BOMLineInput{MaterialID: b.ID, Qty: "1", Per: "line"})
	receive(t, s, outletA, a.ID, "6000", 200000, "")
	receive(t, s, outletA, b.ID, "10", 700, "")

	order := domain.StockOrder{OrderID: "11111111-2222-3333-4444-" + fmt.Sprintf("%012d", svc.ID), OutletID: outletA, Lines: []domain.OrderLineUse{
		{ServiceID: svc.ID, Qty: "2", VariantOptionIDs: []int64{express}, ModifierOptionIDs: []int64{lavender}}}}
	n, err := s.ConsumeOrder(ctx, order, "kasir")
	if err != nil || n != 2 {
		t.Fatalf("consume = %d, %v; want 2 movements", n, err)
	}
	if qty, value := balance(t, s, outletA, a.ID); qty != "5934" || value != 197800 {
		t.Errorf("A = %s / %d; want 5934 / 197800 (66 ml out, Rp 2.200)", qty, value)
	}
	if qty, value := balance(t, s, outletA, b.ID); qty != "9" || value != 630 {
		t.Errorf("B = %s / %d; want 9 / 630", qty, value)
	}
	moves, _, _ := s.ListMovements(ctx, mustMovementQuery(t, domain.MovementQuery{OutletID: outletA, Kind: domain.KindConsumption, Q: "", MaterialID: a.ID}))
	if len(moves) != 1 || moves[0].RefType != "order" || moves[0].RefID != order.OrderID || moves[0].CreatedBy != "kasir" || moves[0].QtyDelta.RatString() != "-66" || moves[0].ValueDelta != -2200 {
		t.Errorf("consumption movement = %+v", moves)
	}

	// Redelivery changes nothing.
	if n, err := s.ConsumeOrder(ctx, order, "kasir"); err != nil || n != 0 {
		t.Errorf("consume again = %d, %v; want 0", n, err)
	}
	if qty, _ := balance(t, s, outletA, a.ID); qty != "5934" {
		t.Errorf("consumed twice: %s", qty)
	}
	// The cancelled order puts back exactly what it took, once.
	if n, err := s.ReverseOrder(ctx, order, "kasir"); err != nil || n != 2 {
		t.Fatalf("reverse = %d, %v; want 2", n, err)
	}
	if qty, value := balance(t, s, outletA, a.ID); qty != "6000" || value != 200000 {
		t.Errorf("A after the reversal = %s / %d; want 6000 / 200000", qty, value)
	}
	if qty, value := balance(t, s, outletA, b.ID); qty != "10" || value != 700 {
		t.Errorf("B after the reversal = %s / %d; want 10 / 700", qty, value)
	}
	if n, err := s.ReverseOrder(ctx, order, "kasir"); err != nil || n != 0 {
		t.Errorf("reverse again = %d, %v; want 0", n, err)
	}
	// An order that was never consumed has nothing to reverse.
	if n, err := s.ReverseOrder(ctx, domain.StockOrder{OrderID: "never", OutletID: outletA}, "kasir"); err != nil || n != 0 {
		t.Errorf("reverse of nothing = %d, %v", n, err)
	}

	// An order from before option ids existed uses the service's own recipe only: 1 kg is 30 ml of A.
	legacy := domain.StockOrder{OrderID: "legacy-" + tag, OutletID: outletA, Lines: []domain.OrderLineUse{{ServiceID: svc.ID, Qty: "1"}}}
	if n, err := s.ConsumeOrder(ctx, legacy, "kasir"); err != nil || n != 1 {
		t.Errorf("legacy consume = %d, %v; want 1", n, err)
	}
	if qty, value := balance(t, s, outletA, a.ID); qty != "5970" || value != 199000 { // 30 x 200000/6000 = 1000
		t.Errorf("A after the legacy order = %s / %d; want 5970 / 199000", qty, value)
	}
	// A service that no longer exists consumes nothing, and does not fail the order.
	if n, err := s.ConsumeOrder(ctx, domain.StockOrder{OrderID: "ghost-" + tag, OutletID: outletA, Lines: []domain.OrderLineUse{{ServiceID: 987654321, Qty: "1"}}}, "kasir"); err != nil || n != 0 {
		t.Errorf("unknown service = %d, %v", n, err)
	}
}

// TestSalesMayDriveStockNegative: a sale is never blocked, and what is not there is valued at the standard cost.
func TestSalesMayDriveStockNegative(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("NG"), " ", "")
	a := mustMaterial(t, s, tag, "ml", 100000, "3000") // standard cost 100/3
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Test svc"), Unit: "kg", Price: 1000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM bom_lines WHERE material_id = $1`, a.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID)
	})
	setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: a.ID, Qty: "3", Per: "unit"})
	sale := func(id string) {
		t.Helper()
		if _, err := s.ConsumeOrder(ctx, domain.StockOrder{OrderID: id, OutletID: outletA, Lines: []domain.OrderLineUse{{ServiceID: svc.ID, Qty: "1"}}}, "kasir"); err != nil {
			t.Fatal(err)
		}
	}
	sale("neg-1-" + tag) // nothing on hand: 3 ml at 100/3 = Rp 100
	if qty, value := balance(t, s, outletA, a.ID); qty != "-3" || value != -100 {
		t.Errorf("after one sale: %s / %d; want -3 / -100", qty, value)
	}
	sale("neg-2-" + tag)
	if qty, value := balance(t, s, outletA, a.ID); qty != "-6" || value != -200 {
		t.Errorf("after two: %s / %d; want -6 / -200", qty, value)
	}
	// A manual write-off still cannot take what is not there.
	if _, err := waste(t, s, outletA, a.ID, "1"); err == nil || err.Error() != "Only 0 ml in stock" {
		t.Errorf("waste from negative stock: %v", err)
	}
	// Goods arrive: 10 ml for Rp 400. The balance is 4 ml; the value is -200 + 400 = 200 as booked, no write-off of the difference.
	receive(t, s, outletA, a.ID, "10", 400, "")
	if qty, value := balance(t, s, outletA, a.ID); qty != "4" || value != 200 {
		t.Errorf("after the receipt: %s / %d; want 4 / 200", qty, value)
	}
}

// TestMovementsCarryTheOrdersCode: every movement made from an order records the order's TRX code beside its id, search finds it, and
// the ledger still refuses to change it.
func TestMovementsCarryTheOrdersCode(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("RC"), " ", "")
	a := mustMaterial(t, s, tag, "ml", 100000, "3000")
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Test svc"), Unit: "kg", Price: 1000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM bom_lines WHERE material_id = $1`, a.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID)
	})
	setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: a.ID, Qty: "3", Per: "unit"})
	receipt, _ := receive(t, s, outletA, a.ID, "30", 1000, "")
	if receipt.RefCode != "" {
		t.Errorf("a receipt has no order: %q", receipt.RefCode)
	}

	order := domain.StockOrder{OrderID: "rc-" + tag, Code: "TRX-25102026-" + "K7M2", OutletID: outletA,
		Lines: []domain.OrderLineUse{{ServiceID: svc.ID, Qty: "2"}}}
	if n, err := s.ConsumeOrder(ctx, order, "kasir"); err != nil || n != 1 {
		t.Fatalf("consume = %d, %v", n, err)
	}
	if n, err := s.ReverseOrder(ctx, order, "kasir"); err != nil || n != 1 {
		t.Fatalf("reverse = %d, %v", n, err)
	}
	list := func(q string) []domain.StockMovement {
		t.Helper()
		out, _, err := s.ListMovements(ctx, mustMovementQuery(t, domain.MovementQuery{MaterialID: a.ID, Q: q, SortBy: "id", SortDir: "asc"}))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	all := list("")
	if len(all) != 3 || all[0].Kind != domain.KindReceipt || all[0].RefCode != "" {
		t.Fatalf("movements = %+v", all)
	}
	for _, mv := range all[1:] {
		if mv.RefCode != "TRX-25102026-K7M2" || mv.RefID != order.OrderID || mv.RefType != "order" {
			t.Errorf("%s: ref_code %q, ref_id %q; want the order's code and id", mv.Kind, mv.RefCode, mv.RefID)
		}
	}
	// Search finds the movements of an order by its code, whatever the case, and only those.
	for _, q := range []string{"TRX-25102026-K7M2", "k7m2", "trx-2510"} {
		if got := list(q); len(got) != 2 {
			t.Errorf("q=%q found %d movements; want the order's 2", q, len(got))
		}
	}
	if got := list("zzzz"); len(got) != 0 {
		t.Errorf("q=zzzz found %d", len(got))
	}
	// Written once: the ledger refuses to touch the column afterwards, and refuses to clear it.
	for _, stmt := range []string{`UPDATE stock_movements SET ref_code = 'TRX-01012026-AAAA' WHERE id = $1`, `UPDATE stock_movements SET ref_code = NULL WHERE id = $1`, `DELETE FROM stock_movements WHERE id = $1`} {
		if _, err := s.pool.Exec(ctx, stmt, all[1].ID); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v; want it refused", stmt, err)
		}
	}
	// An order from before codes existed (no code on the call) leaves ref_code empty, and a movement that already has none stays none.
	legacy := domain.StockOrder{OrderID: "rc-old-" + tag, OutletID: outletA, Lines: []domain.OrderLineUse{{ServiceID: svc.ID, Qty: "1"}}}
	if _, err := s.ConsumeOrder(ctx, legacy, "kasir"); err != nil {
		t.Fatal(err)
	}
	for _, mv := range list("") {
		if mv.RefID == legacy.OrderID && mv.RefCode != "" {
			t.Errorf("a call without a code wrote %q", mv.RefCode)
		}
	}
	var nulls int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM stock_movements WHERE material_id = $1 AND ref_code IS NULL`, a.ID).Scan(&nulls)
	if nulls != 2 { // the receipt and the legacy consumption: stored as NULL, not as an empty string
		t.Errorf("%d movements with NULL ref_code; want 2", nulls)
	}
}
