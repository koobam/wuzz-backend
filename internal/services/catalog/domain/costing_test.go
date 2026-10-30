package domain

import (
	"math/big"
	"strings"
	"testing"
)

func mat(price int64, qty string) Material {
	q, err := ParseDecimal("q", qty, 4)
	if err != nil {
		panic(err)
	}
	return Material{PurchasePrice: price, PurchaseQty: q}
}

func line(id int64, qty, per, loss string, price int64, buyQty string) BOMLine {
	q, _ := ParseDecimal("q", qty, 4)
	l := new(big.Rat)
	if loss != "" {
		l, _ = ParseDecimal("l", loss, 2)
	}
	return BOMLine{MaterialID: id, SKU: "S", Name: "M", Unit: "ml", Qty: q, Per: per, LossPercent: l, UnitCost: mat(price, buyQty).UnitCost()}
}

func TestUnitCostStaysExact(t *testing.T) {
	// Rp 100.000 for 3.000 ml is 100/3 per ml: not 33.33 and not 33.333333.
	c := mat(100000, "3000").UnitCost()
	if c.RatString() != "100/3" {
		t.Fatalf("unit cost = %s; want 100/3", c.RatString())
	}
	// 30 ml of it costs exactly 1000: a rounded unit cost would give 999.9999 or 1000.0000x.
	if got := new(big.Rat).Mul(c, big.NewRat(30, 1)); got.RatString() != "1000" {
		t.Errorf("30 ml = %s; want exactly 1000", got.RatString())
	}
}

func TestRoundOnceNotPerLine(t *testing.T) {
	// Three lines of 1/3 rupiah each. Round each and add: 0 + 0 + 0 = 0. Add and round once: 1.
	l := line(1, "1", PerUnit, "", 1, "3")
	svc := ServiceItem{ID: 1, Name: "S", Unit: UnitKg, Price: 1000}
	ctx := CostingContext{Service: svc, Base: []BOMLine{l, {MaterialID: 2, Qty: l.Qty, Per: PerUnit, LossPercent: new(big.Rat), UnitCost: l.UnitCost}, {MaterialID: 3, Qty: l.Qty, Per: PerUnit, LossPercent: new(big.Rat), UnitCost: l.UnitCost}}}
	res, err := Calc(ctx, CalcInput{Qty: "1"})
	if err != nil {
		t.Fatal(err)
	}
	perLine := int64(0)
	for _, ln := range res.Lines {
		perLine += RoundRupiah(ln.Cost)
	}
	if perLine != 0 || res.Cost != 1 {
		t.Errorf("per-line rounding gives %d, rounding once gives %d; want 0 and 1", perLine, res.Cost)
	}
	if res.GrossProfit != 999 || res.MarginPercent != "99.90" {
		t.Errorf("profit %d margin %s; want 999 and 99.90", res.GrossProfit, res.MarginPercent)
	}
}

func TestLossAndScaling(t *testing.T) {
	// 25 ml per kg with 5% loss is 26.25 ml per kg; 2.5 kg uses 65.625 ml. A per-line material is used once.
	detergent := line(1, "25", PerUnit, "5", 17000, "1000") // Rp 17 per ml
	packing := line(2, "1", PerLine, "", 70, "1")           // Rp 70 once
	ctx := CostingContext{Service: ServiceItem{ID: 1, Name: "Kiloan", Unit: UnitKg, Price: 7000}, Base: []BOMLine{detergent, packing}}
	res, err := Calc(ctx, CalcInput{Qty: "2.5"})
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatRat(res.Lines[0].QtyUsed, 6); got != "65.625000" {
		t.Errorf("detergent used %s; want 65.625000", got)
	}
	if got := FormatRat(res.Lines[1].QtyUsed, 6); got != "1.000000" {
		t.Errorf("packing used %s; want 1.000000 whatever the quantity", got)
	}
	// 65.625 x 17 = 1115.625; + 70 = 1185.625 -> 1186. Revenue 7000 x 2.5 = 17500.
	if res.Cost != 1186 || res.Revenue != 17500 || res.GrossProfit != 16314 || res.MarginPercent != "93.22" {
		t.Errorf("cost %d revenue %d profit %d margin %s; want 1186 17500 16314 93.22", res.Cost, res.Revenue, res.GrossProfit, res.MarginPercent)
	}
}

func TestHalfAwayAtPointFive(t *testing.T) {
	// 1/2 rupiah: costs round up to 1; and a revenue of 2.5 x 1 rounds to 3.
	l := line(1, "1", PerLine, "", 1, "2")
	res, err := Calc(CostingContext{Service: ServiceItem{ID: 1, Name: "S", Unit: UnitKg, Price: 1}, Base: []BOMLine{l}}, CalcInput{Qty: "2.5"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cost != 1 || res.Revenue != 3 {
		t.Errorf("cost %d revenue %d; want 1 and 3 (half away from zero)", res.Cost, res.Revenue)
	}
}

func TestMarginAndZeroRevenue(t *testing.T) {
	// Revenue 3, cost 2: profit 1, margin 33.33. A loss: revenue 3, cost 5 -> -66.67.
	for _, c := range []struct {
		cost int64
		want string
	}{{2, "33.33"}, {5, "-66.67"}, {3, "0.00"}} {
		l := line(1, "1", PerLine, "", c.cost, "1")
		res, _ := Calc(CostingContext{Service: ServiceItem{ID: 1, Name: "S", Unit: UnitKg, Price: 3}, Base: []BOMLine{l}}, CalcInput{Qty: "1"})
		if res.MarginPercent != c.want {
			t.Errorf("cost %d: margin %s; want %s", c.cost, res.MarginPercent, c.want)
		}
	}
	free, _ := Calc(CostingContext{Service: ServiceItem{ID: 1, Name: "S", Unit: UnitKg, Price: 0}}, CalcInput{Qty: "1"})
	if free.MarginPercent != "0.00" || free.Revenue != 0 {
		t.Errorf("no revenue: %+v", free)
	}
}

func opt(id int64, active bool, def bool, delta int64) VariantOption {
	return VariantOption{ID: id, Name: "o", Active: active, IsDefault: def, PriceDelta: delta}
}

func TestResolveVariantsAndModifiers(t *testing.T) {
	one, two := int32(1), int32(2)
	svc := ServiceItem{ID: 1, Name: "S", Unit: UnitKg, Price: 10000,
		VariantGroups: []VariantGroup{
			{ID: 1, Name: "Speed", Options: []VariantOption{opt(1, true, true, 0), opt(2, true, false, 5000), opt(3, false, false, 9)}},
			{ID: 2, Name: "Size", Options: []VariantOption{opt(4, true, false, 0), opt(5, true, true, 2000)}},
		},
		ModifierGroups: []ModifierGroup{
			{ID: 1, Name: "Scent", Min: 1, Max: &one, Options: []ModifierOption{{ID: 1, Name: "a", Price: 0, Active: true}, {ID: 2, Name: "b", Price: 2000, Active: true}}},
			{ID: 2, Name: "Extras", Min: 0, Max: &two, Options: []ModifierOption{{ID: 3, Name: "c", Price: 3000, Active: true}, {ID: 4, Name: "d", Price: 4000, Active: true}, {ID: 5, Name: "e", Price: 1, Active: true}, {ID: 6, Name: "f", Price: 1, Active: false}}},
		}}
	// Unchosen variant groups take their default; the chosen Express adds its delta.
	c, err := Resolve(svc, []int64{2}, []int64{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	sum := int64(0)
	for _, o := range append(c.Variant, c.Modifier...) {
		sum += o.Delta
	}
	if len(c.Variant) != 2 || c.Variant[1].ID != 5 || sum != 5000+2000+0+3000 {
		t.Errorf("resolved %+v, delta %d", c, sum)
	}
	bad := map[string]struct{ v, m []int64 }{
		"two of one variant group": {[]int64{1, 2}, []int64{1}},
		"inactive variant":         {[]int64{3}, []int64{1}},
		"unknown variant":          {[]int64{99}, []int64{1}},
		"duplicate variant":        {[]int64{2, 2}, []int64{1}},
		"required group empty":     {nil, nil},
		"over the max":             {nil, []int64{1, 3, 4, 5}},
		"two of a pick-one":        {nil, []int64{1, 2}},
		"inactive modifier":        {nil, []int64{1, 6}},
		"unknown modifier":         {nil, []int64{1, 77}},
		"duplicate modifier":       {nil, []int64{1, 3, 3}},
		"non-positive id":          {[]int64{0}, []int64{1}},
	}
	for name, in := range bad {
		if _, err := Resolve(svc, in.v, in.m); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestParseBOM(t *testing.T) {
	good, err := ParseBOM([]BOMLineInput{{MaterialID: 1, Qty: "12.3400", Per: "unit", LossPercent: "5.25"}, {MaterialID: 2, Qty: "1", Per: "line"}})
	if err != nil || good[0].LossPercent.RatString() != "21/4" || good[1].LossPercent.Sign() != 0 {
		t.Fatalf("good = %+v, %v", good, err)
	}
	for name, in := range map[string][]BOMLineInput{
		"duplicate material": {{MaterialID: 1, Qty: "1", Per: "unit"}, {MaterialID: 1, Qty: "2", Per: "line"}},
		"zero qty":           {{MaterialID: 1, Qty: "0", Per: "unit"}},
		"5 decimals":         {{MaterialID: 1, Qty: "0.00001", Per: "unit"}},
		"bad per":            {{MaterialID: 1, Qty: "1", Per: "each"}},
		"loss 100.01":        {{MaterialID: 1, Qty: "1", Per: "unit", LossPercent: "100.01"}},
		"loss 3 decimals":    {{MaterialID: 1, Qty: "1", Per: "unit", LossPercent: "1.234"}},
		"exponent":           {{MaterialID: 1, Qty: "1e3", Per: "unit"}},
		"no material":        {{Qty: "1", Per: "unit"}},
		"too many":           make([]BOMLineInput, MaxBOMLines+1),
	} {
		if _, err := ParseBOM(in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := ParseBOM([]BOMLineInput{{MaterialID: 1, Qty: "1", Per: "unit", LossPercent: "100"}}); err != nil {
		t.Errorf("loss 100 is allowed: %v", err)
	}
}

func TestParseMaterial(t *testing.T) {
	ok := MaterialInput{SKU: " CHM-DET.01/a_b ", Name: " Deterjen ", Category: "chemical", Unit: "ml", PurchasePrice: 85000, PurchaseQty: "5000", Active: true}
	m, err := ParseMaterial(ok)
	if err != nil || m.SKU != "CHM-DET.01/a_b" || m.Name != "Deterjen" || m.UnitCost().RatString() != "17" {
		t.Fatalf("material = %+v, %v", m, err)
	}
	mutate := func(f func(*MaterialInput)) MaterialInput { c := ok; f(&c); return c }
	for name, in := range map[string]MaterialInput{
		"empty sku":      mutate(func(m *MaterialInput) { m.SKU = " " }),
		"sku with space": mutate(func(m *MaterialInput) { m.SKU = "A B" }),
		"sku too long":   mutate(func(m *MaterialInput) { m.SKU = strings.Repeat("A", 41) }),
		"sku symbol":     mutate(func(m *MaterialInput) { m.SKU = "A#1" }),
		"no name":        mutate(func(m *MaterialInput) { m.Name = "" }),
		"bad category":   mutate(func(m *MaterialInput) { m.Category = "food" }),
		"bad unit":       mutate(func(m *MaterialInput) { m.Unit = "kg" }),
		"negative price": mutate(func(m *MaterialInput) { m.PurchasePrice = -1 }),
		"zero qty":       mutate(func(m *MaterialInput) { m.PurchaseQty = "0" }),
		"qty exponent":   mutate(func(m *MaterialInput) { m.PurchaseQty = "5e3" }),
		"qty 5 places":   mutate(func(m *MaterialInput) { m.PurchaseQty = "1.00001" }),
	} {
		if _, err := ParseMaterial(in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestMaterialQuery(t *testing.T) {
	q := MaterialQuery{Q: " x "}
	if err := q.Validate(); err != nil || q.SortBy != "id" || q.SortDir != "asc" || q.Limit != 10 || q.Q != "x" {
		t.Errorf("defaults = %+v, %v", q, err)
	}
	for name, bad := range map[string]MaterialQuery{"category": {Category: "food"}, "active": {Active: "yes"}, "sort": {SortBy: "price"}, "dir": {SortDir: "up"}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
