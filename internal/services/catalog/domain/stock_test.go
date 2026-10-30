package domain

import (
	"math/big"
	"math/rand"
	"strings"
	"testing"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// q reads a test number exactly (a sign is allowed here, unlike on the wire).
func q(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad test number " + s)
	}
	return r
}

func state(qty string, value int64) StockState { return StockState{Qty: q(qty), Value: value} }

var std100over3 = big.NewRat(100, 3) // a material bought at Rp 100.000 for 3.000 ml

func TestMovingAverageWithThirds(t *testing.T) {
	// 3 units worth Rp 100: a unit is 100/3. Take one at a time: 33.33 -> 33, then 67/2 = 33.5 -> 34 (half away), then the last
	// one takes everything that is left, so the three add up to exactly 100.
	b := state("3", 100)
	var taken int64
	for i, want := range []int64{-33, -34, -33} {
		p := PlanOut(b, q("1"), std100over3)
		if p.ValueDelta != want {
			t.Fatalf("step %d: value_delta = %d; want %d (balance %s / %d)", i+1, p.ValueDelta, want, b.Qty.RatString(), b.Value)
		}
		taken += p.ValueDelta
		b = b.After(p)
	}
	if b.Qty.Sign() != 0 || b.Value != 0 || taken != -100 {
		t.Errorf("after the last: qty %s value %d, taken %d; want 0, 0, -100", b.Qty.RatString(), b.Value, taken)
	}
}

func TestReceiptAndAverageHandChecked(t *testing.T) {
	// Receive 1.000 ml for Rp 17.000 (Rp 17 a ml), then 3.000 ml for Rp 100.000 (100/3 a ml): 4.000 ml worth Rp 117.000 = 29.25 a ml.
	b := state("0", 0)
	b = b.After(PlanReceipt(b, q("1000"), 17000))
	b = b.After(PlanReceipt(b, q("3000"), 100000))
	if b.Qty.RatString() != "4000" || b.Value != 117000 {
		t.Fatalf("balance = %s / %d", b.Qty.RatString(), b.Value)
	}
	row := StockRow{Qty: b.Qty, Value: b.Value, MinQty: q("0"), StdUnitCost: std100over3}
	if got := FormatRat(row.AvgCost(), 6); got != "29.250000" {
		t.Errorf("average = %s; want 29.250000", got)
	}
	// 400 ml out: 400 x 117000 / 4000 = 11700 exactly.
	if p := PlanOut(b, q("400"), std100over3); p.ValueDelta != -11700 {
		t.Errorf("400 ml = %d; want -11700", p.ValueDelta)
	}
	// With nothing on hand the shown average is the standard cost.
	empty := StockRow{Qty: q("0"), MinQty: q("0"), StdUnitCost: std100over3}
	if FormatRat(empty.AvgCost(), 6) != "33.333333" {
		t.Errorf("empty average = %s", FormatRat(empty.AvgCost(), 6))
	}
}

func TestHalfAwayWhenTakingOut(t *testing.T) {
	// 1 of 2 units worth Rp 1 is 0.5 -> 1, and the rest takes the remaining 0.
	b := state("2", 1)
	p := PlanOut(b, q("1"), std100over3)
	if p.ValueDelta != -1 {
		t.Errorf("0.5 rounds away from zero: %d", p.ValueDelta)
	}
	if q := PlanOut(b.After(p), q("1"), std100over3); q.ValueDelta != 0 {
		t.Errorf("the last unit takes what is left (0): %d", q.ValueDelta)
	}
}

func TestManualMovesRefuseMoreThanOnHand(t *testing.T) {
	_, err := PlanOutManual(state("2.5", 250), q("2.5001"), std100over3, "ml")
	ae, ok := err.(*apperr.Error)
	if !ok || ae.Kind != apperr.KindInvalid || ae.Message != "Only 2.5 ml in stock" {
		t.Errorf("err = %v; want \"Only 2.5 ml in stock\"", err)
	}
	if _, err := PlanOutManual(state("2.5", 250), q("2.5"), std100over3, "ml"); err != nil {
		t.Errorf("exactly what is on hand is allowed: %v", err)
	}
	// Negative stock has nothing to give: "Only 0 g".
	if _, err := PlanOutManual(state("-3", -100), q("1"), std100over3, "g"); err == nil || !strings.Contains(err.Error(), "Only 0 g in stock") {
		t.Errorf("negative balance: %v", err)
	}
}

func TestNegativeStockIsValuedAtStandardCost(t *testing.T) {
	// Nothing on hand: 3 ml out is worth 3 x 100/3 = 100 at the standard cost, and the balance goes negative.
	b := state("0", 0)
	p := PlanOut(b, q("3"), std100over3)
	b = b.After(p)
	if b.Qty.RatString() != "-3" || b.Value != -100 {
		t.Fatalf("balance = %s / %d; want -3 / -100", b.Qty.RatString(), b.Value)
	}
	// Still empty: another 3 ml, another -100.
	b = b.After(PlanOut(b, q("3"), std100over3))
	if b.Qty.RatString() != "-6" || b.Value != -200 {
		t.Fatalf("balance = %s / %d; want -6 / -200", b.Qty.RatString(), b.Value)
	}
	// A count that finds 6 more brings it back to exactly 0 and clears the carried value.
	b = b.After(PlanAdjustUp(b, q("6"), std100over3))
	if b.Qty.Sign() != 0 || b.Value != 0 {
		t.Errorf("back to zero: %s / %d", b.Qty.RatString(), b.Value)
	}
	// Taking more than is on hand: all of the value on hand plus the missing part at the standard cost.
	b = state("2", 500)
	p = PlanOut(b, q("5"), std100over3) // 3 missing x 100/3 = 100
	if p.ValueDelta != -600 || b.After(p).Qty.RatString() != "-3" || b.After(p).Value != -100 {
		t.Errorf("crossing zero: delta %d, after %s / %d", p.ValueDelta, b.After(p).Qty.RatString(), b.After(p).Value)
	}
}

func TestAdjustUpValuation(t *testing.T) {
	if p := PlanAdjustUp(state("4", 100), q("2"), std100over3); p.ValueDelta != 50 {
		t.Errorf("at the average: %d; want 50", p.ValueDelta)
	}
	if p := PlanAdjustUp(state("0", 0), q("3"), std100over3); p.ValueDelta != 100 {
		t.Errorf("with nothing on hand, at the standard cost: %d; want 100", p.ValueDelta)
	}
	// 1/3 rounds down, 2/3 up.
	if p := PlanAdjustUp(state("3", 100), q("1"), std100over3); p.ValueDelta != 33 {
		t.Errorf("1 of 3 at 100: %d; want 33", p.ValueDelta)
	}
}

func TestLowStockFlag(t *testing.T) {
	r := StockRow{Qty: q("100"), MinQty: q("100"), StdUnitCost: std100over3}
	if !r.Low() {
		t.Error("at the reorder level is low")
	}
	r.Qty = q("100.0001")
	if r.Low() {
		t.Error("above it is not")
	}
	r.MinQty, r.Qty = q("0"), q("0")
	if r.Low() {
		t.Error("no reorder level, never low")
	}
}

// movement records what a property run did, to check the ledger against the balance.
type ledger struct {
	state    StockState
	sumValue int64
	sumQty   *big.Rat
}

func (l *ledger) apply(t *testing.T, label string, p Plan) {
	t.Helper()
	l.state = l.state.After(p)
	l.sumValue += p.ValueDelta
	l.sumQty.Add(l.sumQty, p.QtyDelta)
	if l.sumValue != l.state.Value {
		t.Fatalf("%s: value drifted: balance %d but the movements add up to %d", label, l.state.Value, l.sumValue)
	}
	if l.sumQty.Cmp(l.state.Qty) != 0 {
		t.Fatalf("%s: qty drifted: balance %s but the movements add up to %s", label, l.state.Qty.RatString(), l.sumQty.RatString())
	}
	// Quantities stay exact at 4 places: times 10.000 they are whole numbers.
	if !new(big.Rat).Mul(l.state.Qty, big.NewRat(10000, 1)).IsInt() {
		t.Fatalf("%s: qty %s is not a 4-place decimal", label, l.state.Qty.RatString())
	}
}

func randQty(r *rand.Rand, max int64) *big.Rat { return big.NewRat(1+r.Int63n(max), 10000) }

// TestValueNeverDrifts runs a long seeded random sequence of receipts, consumption, waste and counts. The value of the balance must
// always equal the sum of the movements' value_deltas, quantities must stay exact, manual moves must never go negative, and
// whenever the quantity is exactly zero the value must be zero.
func TestValueNeverDrifts(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 2026} {
		r := rand.New(rand.NewSource(seed))
		std := big.NewRat(r.Int63n(5000)+1, r.Int63n(900)+3) // an awkward exact fraction
		l := &ledger{state: StockState{Qty: new(big.Rat)}, sumQty: new(big.Rat)}
		for i := 0; i < 5000; i++ {
			label := "seed " + big.NewInt(seed).String() + " step " + big.NewInt(int64(i)).String()
			switch r.Intn(5) {
			case 0: // receipt
				l.apply(t, label+" receipt", PlanReceipt(l.state, randQty(r, 5_000_000), r.Int63n(300000)))
			case 1, 2: // automatic consumption, kept within what is on hand in this run
				if l.state.Qty.Sign() > 0 {
					qty := randQty(r, 4_000_000)
					if qty.Cmp(l.state.Qty) > 0 {
						qty = new(big.Rat).Set(l.state.Qty) // sometimes exactly everything
					}
					l.apply(t, label+" consumption", PlanOut(l.state, qty, std))
				}
			case 3: // waste, manual
				qty := randQty(r, 2_000_000)
				p, err := PlanOutManual(l.state, qty, std, "ml")
				if err != nil {
					if qty.Cmp(l.state.OnHand()) <= 0 {
						t.Fatalf("%s: refused though it fits: %v", label, err)
					}
					continue
				}
				l.apply(t, label+" waste", p)
			case 4: // a count
				counted := big.NewRat(r.Int63n(60_000_000), 10000)
				delta := new(big.Rat).Sub(counted, l.state.Qty)
				switch delta.Sign() {
				case 1:
					l.apply(t, label+" count up", PlanAdjustUp(l.state, delta, std))
				case -1:
					l.apply(t, label+" count down", PlanOut(l.state, new(big.Rat).Neg(delta), std))
				}
			}
			if l.state.Qty.Sign() < 0 {
				t.Fatalf("%s: went negative without a sale to blame: %s", label, l.state.Qty.RatString())
			}
			if l.state.Qty.Sign() == 0 && l.state.Value != 0 {
				t.Fatalf("%s: zero quantity but value %d", label, l.state.Value)
			}
		}
		// Drain what is left: the last movement takes every remaining rupiah.
		if l.state.Qty.Sign() > 0 {
			l.apply(t, "drain", PlanOut(l.state, l.state.Qty, std))
		}
		if l.state.Value != 0 {
			t.Errorf("seed %d: drained but worth %d", seed, l.state.Value)
		}
	}
}

// TestValueNeverDriftsThroughNegativeStock lets sales drive the balance negative and checks the books still add up.
func TestValueNeverDriftsThroughNegativeStock(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	std := big.NewRat(100, 3)
	l := &ledger{state: StockState{Qty: new(big.Rat)}, sumQty: new(big.Rat)}
	wentNegative := false
	for i := 0; i < 5000; i++ {
		switch r.Intn(3) {
		case 0:
			l.apply(t, "receipt", PlanReceipt(l.state, randQty(r, 300_000), r.Int63n(20000)))
		default:
			l.apply(t, "sale", PlanOut(l.state, randQty(r, 400_000), std)) // never blocked
		}
		if l.state.Qty.Sign() < 0 {
			wentNegative = true
		}
	}
	if !wentNegative {
		t.Error("the run never went negative; it proves nothing")
	}
}

func TestOrderUseRoundsOnceInTotal(t *testing.T) {
	a := BOMLine{MaterialID: 1, Qty: q("10"), Per: PerUnit, LossPercent: q("10")} // 11 per unit with 10% loss
	b := BOMLine{MaterialID: 2, Qty: q("1"), Per: PerLine, LossPercent: new(big.Rat)}
	varA := BOMLine{MaterialID: 1, Qty: q("2"), Per: PerUnit, LossPercent: new(big.Rat)}
	modC := BOMLine{MaterialID: 3, Qty: q("1"), Per: PerLine, LossPercent: new(big.Rat)}
	c := CostingContext{Base: []BOMLine{a, b}, Variant: map[int64][]BOMLine{5: {varA}}, Modifier: map[int64][]BOMLine{7: {modC}}}
	use, err := OrderUse(map[int64]CostingContext{8: c}, StockOrder{OrderID: "o", OutletID: 1, Lines: []OrderLineUse{
		{ServiceID: 8, Qty: "2.5", VariantOptionIDs: []int64{5}, ModifierOptionIDs: []int64{7}},
		{ServiceID: 8, Qty: "1"},   // no option ids: the base recipe only
		{ServiceID: 999, Qty: "3"}, // not a catalog service: nothing
	}})
	if err != nil {
		t.Fatal(err)
	}
	// A: line 1 uses 11 x 2.5 + 2 x 2.5 = 32.5; line 2 uses 11 -> 43.5. B: once per line, twice. C: once.
	if use[1].RatString() != "87/2" || use[2].RatString() != "2" || use[3].RatString() != "1" || len(use) != 3 {
		t.Errorf("use = %v", use)
	}
}

func TestOrderUseRoundsTheTotalNotTheLines(t *testing.T) {
	tiny := BOMLine{MaterialID: 1, Qty: q("0.0001"), Per: PerUnit, LossPercent: new(big.Rat)}
	c := CostingContext{Base: []BOMLine{tiny}}
	// Each line uses 0.00004 (0.4 x 0.0001), which alone rounds to nothing. Two lines use 0.00008 together, which is 0.0001.
	two, _ := OrderUse(map[int64]CostingContext{1: c}, StockOrder{Lines: []OrderLineUse{{ServiceID: 1, Qty: "0.4"}, {ServiceID: 1, Qty: "0.4"}}})
	one, _ := OrderUse(map[int64]CostingContext{1: c}, StockOrder{Lines: []OrderLineUse{{ServiceID: 1, Qty: "0.4"}}})
	if two[1] == nil || two[1].RatString() != "1/10000" || len(one) != 0 {
		t.Errorf("two lines: %v; one line: %v; want 0.0001 and nothing", two, one)
	}
	// Half away from zero at 4 places: 0.00005 -> 0.0001.
	half, _ := OrderUse(map[int64]CostingContext{1: c}, StockOrder{Lines: []OrderLineUse{{ServiceID: 1, Qty: "0.5"}}})
	if half[1] == nil || half[1].RatString() != "1/10000" {
		t.Errorf("0.00005 should round up to 0.0001: %v", half)
	}
	// Quantities from the order service are float64s printed as their shortest decimal: 0.1 + 0.2 style values are tolerated.
	if _, err := OrderUse(map[int64]CostingContext{1: c}, StockOrder{Lines: []OrderLineUse{{ServiceID: 1, Qty: "0.30000000000000004"}}}); err != nil {
		t.Errorf("a float's shortest decimal: %v", err)
	}
	if _, err := OrderUse(map[int64]CostingContext{1: c}, StockOrder{Lines: []OrderLineUse{{ServiceID: 1, Qty: "abc"}}}); err == nil {
		t.Error("garbage is refused")
	}
}

func TestStockRequestsParse(t *testing.T) {
	if _, err := (StockReceiptInput{OutletID: 1, MaterialID: 2, Qty: "1.5", TotalCost: 0}).Parse(); err != nil {
		t.Errorf("a free receipt is fine: %v", err)
	}
	bad := map[string]func() error{
		"receipt no qty": func() error { _, e := (StockReceiptInput{OutletID: 1, MaterialID: 2}).Parse(); return e },
		"receipt zero":   func() error { _, e := (StockReceiptInput{OutletID: 1, MaterialID: 2, Qty: "0"}).Parse(); return e },
		"receipt 5 places": func() error {
			_, e := (StockReceiptInput{OutletID: 1, MaterialID: 2, Qty: "0.00001"}).Parse()
			return e
		},
		"receipt negative": func() error {
			_, e := (StockReceiptInput{OutletID: 1, MaterialID: 2, Qty: "1", TotalCost: -1}).Parse()
			return e
		},
		"receipt no outlet": func() error { _, e := (StockReceiptInput{MaterialID: 2, Qty: "1"}).Parse(); return e },
		"waste bad reason": func() error {
			_, e := (StockWasteInput{OutletID: 1, MaterialID: 2, Qty: "1", Reason: "stolen"}).Parse()
			return e
		},
		"count no note": func() error {
			_, e := (StockAdjustInput{OutletID: 1, MaterialID: 2, CountedQty: "0"}).Parse()
			return e
		},
		"count negative": func() error {
			_, e := (StockAdjustInput{OutletID: 1, MaterialID: 2, CountedQty: "-1", Note: "x"}).Parse()
			return e
		},
		"transfer same outlet": func() error {
			_, e := (StockTransferInput{FromOutletID: 1, ToOutletID: 1, MaterialID: 2, Qty: "1"}).Parse()
			return e
		},
		"long key": func() error {
			_, e := (StockReceiptInput{OutletID: 1, MaterialID: 2, Qty: "1", Key: strings.Repeat("k", 101)}).Parse()
			return e
		},
	}
	for name, f := range bad {
		if f() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if c, err := (StockAdjustInput{OutletID: 1, MaterialID: 2, CountedQty: "0", Note: "count"}).Parse(); err != nil || c.CountedQty.Sign() != 0 {
		t.Errorf("counting zero is allowed: %v", err)
	}
}

func TestStockQueries(t *testing.T) {
	s := StockQuery{}
	if err := s.Validate(); err != nil || s.SortBy != "sku" || s.SortDir != "asc" || s.Limit != 10 {
		t.Errorf("stock defaults = %+v, %v", s, err)
	}
	m := MovementQuery{}
	if err := m.Validate(); err != nil || m.SortBy != "created_at" || m.SortDir != "desc" || m.Limit != 10 {
		t.Errorf("movement defaults = %+v, %v", m, err)
	}
	for name, bad := range map[string]MovementQuery{
		"bad kind":   {Kind: "gift"},
		"bad day":    {From: "2026-13-01"},
		"bad format": {To: "25/10/2026"},
		"reversed":   {From: "2026-10-25", To: "2026-10-24"},
		"bad sort":   {SortBy: "value"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	for name, bad := range map[string]StockQuery{"bad sort": {SortBy: "price"}, "negative outlet": {OutletID: -1}, "long search": {Q: strings.Repeat("x", 65)}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	ok := MovementQuery{From: "2026-10-24", To: "2026-10-25", Kind: KindWaste}
	if err := ok.Validate(); err != nil {
		t.Errorf("a valid range: %v", err)
	}
}
