package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// mustMaterial creates a material through the same parsing the API uses; it and its recipe lines are removed afterwards.
func mustMaterial(t *testing.T, s *Store, sku, unit string, price int64, qty string) domain.Material {
	t.Helper()
	m, err := domain.ParseMaterial(domain.MaterialInput{SKU: sku, Name: "Test " + sku, Category: "chemical", Unit: unit, PurchasePrice: price, PurchaseQty: qty, Active: true, Notes: "dev test"})
	if err != nil {
		t.Fatal(err)
	}
	made, err := s.CreateMaterial(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		purgeStock(s, made.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM bom_lines WHERE material_id = $1`, made.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM materials WHERE id = $1`, made.ID)
	})
	return made
}

func setBOM(t *testing.T, s *Store, ownerType string, id int64, in ...domain.BOMLineInput) ([]domain.BOMLine, error) {
	t.Helper()
	lines, err := domain.ParseBOM(in)
	if err != nil {
		t.Fatal(err)
	}
	return s.SetBOM(context.Background(), ownerType, id, lines)
}

func TestMaterialCRUDAndRefusals(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("MT"), " ", "")
	a := mustMaterial(t, s, tag+"-A", "ml", 100000, "3000")
	if a.UnitCost().RatString() != "100/3" || a.UsedIn != 0 || a.PurchaseQty.RatString() != "3000" {
		t.Fatalf("created = %+v (unit cost %s)", a, a.UnitCost().RatString())
	}
	// A SKU is unique ignoring case.
	dup, _ := domain.ParseMaterial(domain.MaterialInput{SKU: strings.ToLower(a.SKU), Name: "x", Category: "other", Unit: "pcs", PurchaseQty: "1", Active: true})
	if _, err := s.CreateMaterial(ctx, dup); kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), "SKU already exists") {
		t.Errorf("duplicate sku: %v; want a conflict", err)
	}

	// Update, keeping the exactness of a four-place quantity.
	upd, _ := domain.ParseMaterial(domain.MaterialInput{SKU: a.SKU, Name: "Renamed", Category: "consumable", Unit: "ml", PurchasePrice: 100, PurchaseQty: "0.3333", Active: false, Notes: "n"})
	upd.ID = a.ID
	got, err := s.UpdateMaterial(ctx, upd)
	if err != nil || got.PurchaseQty.RatString() != "3333/10000" || got.Active || got.Name != "Renamed" {
		t.Fatalf("updated = %+v, %v", got, err)
	}
	upd.PurchaseQty, _ = domain.ParseDecimal("q", "3000", 4)
	upd.PurchasePrice, upd.Active, upd.Name = 100000, true, "Test "+a.SKU
	if _, err := s.UpdateMaterial(ctx, upd); err != nil {
		t.Fatal(err)
	}

	// List: search over sku, name and notes, ignoring case; filter; sort; page.
	b := mustMaterial(t, s, tag+"-B", "g", 5000, "1000")
	list := func(f domain.MaterialQuery) ([]domain.Material, int64) {
		t.Helper()
		if err := f.Validate(); err != nil {
			t.Fatal(err)
		}
		out, total, err := s.ListMaterials(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return out, total
	}
	if out, total := list(domain.MaterialQuery{Q: strings.ToLower(tag)}); total != 2 || len(out) != 2 || out[0].ID != a.ID {
		t.Errorf("search by sku: %d items, total %d", len(out), total)
	}
	if _, total := list(domain.MaterialQuery{Q: "DEV TEST", Category: "chemical"}); total < 1 {
		t.Errorf("search by notes: total %d", total)
	}
	if _, total := list(domain.MaterialQuery{Q: tag + "%"}); total != 0 {
		t.Errorf("a wildcard matched %d", total)
	}
	if out, total := list(domain.MaterialQuery{Q: tag, SortBy: "sku", SortDir: "desc", Limit: 1}); total != 2 || out[0].ID != b.ID {
		t.Errorf("sorted page: %+v total %d", out, total)
	}
	if out, _ := list(domain.MaterialQuery{Q: tag, SortBy: "sku", SortDir: "desc", Limit: 1, Offset: 1}); len(out) != 1 || out[0].ID != a.ID {
		t.Errorf("second page: %+v", out)
	}

	// Used in a recipe: the unit is fixed and the material cannot be deleted; the refusal names the owner.
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Test svc"), Unit: "kg", Price: 1000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM bom_lines WHERE owner_type = 'service' AND owner_id = $1`, svc.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID)
	})
	if _, err := setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: a.ID, Qty: "30", Per: "unit"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMaterial(ctx, a.ID); kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), "Still used by: "+svc.Name) {
		t.Errorf("delete in use: %v", err)
	}
	upd.Unit = "g"
	if _, err := s.UpdateMaterial(ctx, upd); kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), "unit cannot change") {
		t.Errorf("unit change in use: %v", err)
	}
	if m, _ := s.GetMaterial(ctx, a.ID); m.UsedIn != 1 || m.Unit != "ml" {
		t.Errorf("after the refusals: %+v", m)
	}
	if _, err := setBOM(t, s, "service", svc.ID); err != nil { // an empty recipe frees it
		t.Fatal(err)
	}
	if err := s.DeleteMaterial(ctx, a.ID); err != nil {
		t.Errorf("delete unused: %v", err)
	}
	if err := s.DeleteMaterial(ctx, a.ID); kindOf(err) != apperr.KindNotFound {
		t.Errorf("delete again: %v", err)
	}
}

func TestBOMReplaceAndRules(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("BM"), " ", "")
	m1 := mustMaterial(t, s, tag+"-1", "ml", 17000, "1000")
	m2 := mustMaterial(t, s, tag+"-2", "pcs", 70, "1")
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Test svc"), Unit: "kg", Price: 1000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM bom_lines WHERE owner_type = 'service' AND owner_id = $1`, svc.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID)
	})

	lines, err := setBOM(t, s, "service", svc.ID,
		domain.BOMLineInput{MaterialID: m1.ID, Qty: "25", Per: "unit", LossPercent: "5"}, domain.BOMLineInput{MaterialID: m2.ID, Qty: "1", Per: "line"})
	if err != nil || len(lines) != 2 || lines[0].EffectiveQty().RatString() != "105/4" { // 25 x 1.05 = 26.25
		t.Fatalf("set = %+v, %v", lines, err)
	}
	if lines[0].UnitCost.RatString() != "17" {
		t.Errorf("unit cost = %s", lines[0].UnitCost.RatString())
	}
	// Replacing swaps everything.
	if lines, err = setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: m2.ID, Qty: "2.5", Per: "unit"}); err != nil || len(lines) != 1 || lines[0].Qty.RatString() != "5/2" {
		t.Fatalf("replaced = %+v, %v", lines, err)
	}
	// Unknown material, unknown owner.
	if _, err := setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: 987654321, Qty: "1", Per: "unit"}); kindOf(err) != apperr.KindInvalid {
		t.Errorf("unknown material: %v", err)
	}
	if _, err := setBOM(t, s, "service", 987654321, domain.BOMLineInput{MaterialID: m1.ID, Qty: "1", Per: "unit"}); kindOf(err) != apperr.KindNotFound {
		t.Errorf("unknown owner: %v", err)
	}
	if _, err := s.GetBOM(ctx, "variant_option", 987654321); kindOf(err) != apperr.KindNotFound {
		t.Errorf("unknown option: %v", err)
	}
	// An inactive material cannot be newly added, but may stay on a line it is already on.
	if _, err := setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: m2.ID, Qty: "2.5", Per: "unit"}, domain.BOMLineInput{MaterialID: m1.ID, Qty: "1", Per: "unit"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []domain.Material{m1, m2} {
		off := m
		off.Active = false
		if _, err := s.UpdateMaterial(ctx, off); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: m1.ID, Qty: "3", Per: "unit"}); err != nil {
		t.Errorf("an inactive material may stay: %v", err)
	}
	if _, err := setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: m1.ID, Qty: "3", Per: "unit"}, domain.BOMLineInput{MaterialID: m2.ID, Qty: "1", Per: "line"}); err != nil {
		// m2 was on the recipe before the replacement above removed it, so it counts as new now.
		if kindOf(err) != apperr.KindInvalid || !strings.Contains(err.Error(), "inactive") {
			t.Errorf("adding an inactive material: %v", err)
		}
	} else {
		t.Error("an inactive material was newly added")
	}
}

func TestOptionDeletionCleansRecipes(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("OD"), " ", "")
	mat := mustMaterial(t, s, tag, "ml", 1000, "10")
	vg, err := s.CreateVariantGroup(ctx, domain.VariantGroup{Name: tag, Options: []domain.VariantOption{{Name: "Keep", Active: true, IsDefault: true}, {Name: "Drop", Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	mg, err := s.CreateModifierGroup(ctx, domain.ModifierGroup{Name: tag, Options: []domain.ModifierOption{{Name: "Keep", Active: true}, {Name: "Drop", Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM bom_lines WHERE material_id = $1`, mat.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM variant_groups WHERE id = $1`, vg.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM modifier_groups WHERE id = $1`, mg.ID)
	})
	for _, o := range append(vg.Options, domain.VariantOption{}) {
		if o.ID != 0 {
			if _, err := setBOM(t, s, "variant_option", o.ID, domain.BOMLineInput{MaterialID: mat.ID, Qty: "1", Per: "unit"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, o := range mg.Options {
		if _, err := setBOM(t, s, "modifier_option", o.ID, domain.BOMLineInput{MaterialID: mat.ID, Qty: "1", Per: "line"}); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		var n int
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM bom_lines WHERE material_id = $1`, mat.ID).Scan(&n)
		return n
	}
	if count() != 4 {
		t.Fatalf("recipe lines = %d; want 4", count())
	}
	// Dropping an option in a group update takes its recipe lines with it.
	vg.Options = vg.Options[:1]
	if _, err := s.UpdateVariantGroup(ctx, vg); err != nil {
		t.Fatal(err)
	}
	mg.Options = mg.Options[:1]
	if _, err := s.UpdateModifierGroup(ctx, mg); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Errorf("after dropping options: %d lines; want 2", count())
	}
	// Deleting a group takes its options' lines too.
	if err := s.DeleteVariantGroup(ctx, vg.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteModifierGroup(ctx, mg.ID); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Errorf("after deleting the groups: %d lines; want 0", count())
	}
}

// TestCostingByHand checks a sale against numbers worked out on paper.
//
//	Service: Rp 10.000 per kg. Material A: Rp 100.000 buys 3.000 ml = 100/3 per ml. Material B: Rp 70 per pcs.
//	Service recipe: 30 ml of A per kg                       -> Rp 1.000 per kg exactly.
//	Variant Express (+Rp 5.000): 3 ml of A per kg           -> Rp 100 per kg. Reguler (default, +0): nothing.
//	Modifier Lavender (+Rp 2.000, required group of one): 1 pcs of B per order line -> Rp 70 once.
func TestCostingByHand(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("CO"), " ", "")
	a := mustMaterial(t, s, tag+"-A", "ml", 100000, "3000")
	b := mustMaterial(t, s, tag+"-B", "pcs", 70, "1")
	one := int32(1)
	vg, err := s.CreateVariantGroup(ctx, domain.VariantGroup{Name: tag, Options: []domain.VariantOption{{Name: "Reguler", Active: true, IsDefault: true}, {Name: "Express", PriceDelta: 5000, Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	mg, err := s.CreateModifierGroup(ctx, domain.ModifierGroup{Name: tag, Min: 1, Max: &one, Options: []domain.ModifierOption{{Name: "Lavender", Price: 2000, Active: true}, {Name: "Plain", Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Test svc"), Unit: "kg", Price: 10000, Active: true,
		VariantGroupIDs: []int64{vg.ID}, ModifierGroupIDs: []int64{mg.ID}})
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
	if _, err := setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: a.ID, Qty: "30", Per: "unit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := setBOM(t, s, "variant_option", express, domain.BOMLineInput{MaterialID: a.ID, Qty: "3", Per: "unit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := setBOM(t, s, "modifier_option", lavender, domain.BOMLineInput{MaterialID: b.ID, Qty: "1", Per: "line"}); err != nil {
		t.Fatal(err)
	}

	cc, err := s.LoadCostingContext(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The cost table.
	perUnit, perLine := domain.SumByPer(cc.Base)
	if domain.FormatRat(perUnit, 6) != "1000.000000" || perLine.Sign() != 0 {
		t.Errorf("base = %s / %s", domain.FormatRat(perUnit, 6), domain.FormatRat(perLine, 6))
	}
	if u, _ := domain.SumByPer(cc.Variant[express]); domain.FormatRat(u, 6) != "100.000000" {
		t.Errorf("express per unit = %s", domain.FormatRat(u, 6))
	}
	if _, l := domain.SumByPer(cc.Modifier[lavender]); domain.FormatRat(l, 6) != "70.000000" {
		t.Errorf("lavender per line = %s", domain.FormatRat(l, 6))
	}

	// 2 kg, Express + Lavender: price 10000 + 5000 + 2000 = 17000, revenue 34000. Cost: 60 ml x 100/3 = 2000, 6 ml x 100/3 = 200,
	// 1 pcs x 70 = 70 -> 2270. Profit 31730, margin 31730/34000 = 93.32%.
	res, err := domain.Calc(cc, domain.CalcInput{VariantOptionIDs: []int64{express}, ModifierOptionIDs: []int64{lavender}, Qty: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if res.UnitPrice != 17000 || res.Revenue != 34000 || res.Cost != 2270 || res.GrossProfit != 31730 || res.MarginPercent != "93.32" || len(res.Lines) != 3 {
		t.Errorf("express + lavender: %+v", res)
	}
	// No variant chosen: the default (Reguler) is taken. 1.5 kg, Lavender: price 12000, revenue 18000, cost 45 ml x 100/3 = 1500 + 70 = 1570,
	// profit 16430, margin 91.2777..% -> 91.28.
	res, err = domain.Calc(cc, domain.CalcInput{ModifierOptionIDs: []int64{lavender}, Qty: "1.5"})
	if err != nil || res.UnitPrice != 12000 || res.Revenue != 18000 || res.Cost != 1570 || res.MarginPercent != "91.28" || len(res.Lines) != 2 {
		t.Errorf("defaults: %+v, %v", res, err)
	}
	// The required modifier group left empty, and two options of a pick-one group, are refused.
	if _, err := domain.Calc(cc, domain.CalcInput{Qty: "1"}); kindOf(err) != apperr.KindInvalid || !strings.Contains(err.Error(), "at least 1") {
		t.Errorf("required group empty: %v", err)
	}
	if _, err := domain.Calc(cc, domain.CalcInput{ModifierOptionIDs: []int64{lavender, mg.Options[1].ID}, Qty: "1"}); kindOf(err) != apperr.KindInvalid || !strings.Contains(err.Error(), "at most 1") {
		t.Errorf("too many: %v", err)
	}
	if _, err := domain.Calc(cc, domain.CalcInput{VariantOptionIDs: []int64{express, vg.Options[0].ID}, ModifierOptionIDs: []int64{lavender}, Qty: "1"}); kindOf(err) != apperr.KindInvalid {
		t.Errorf("two variants of one group: %v", err)
	}
	if _, err := s.LoadCostingContext(ctx, 987654321); kindOf(err) != apperr.KindNotFound {
		t.Errorf("unknown service: %v", err)
	}
}
