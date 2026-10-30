package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	pgplat "github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Like the discovery tests, these need CATALOG_TEST_DSN (a catalog database with the migrations applied).
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("CATALOG_TEST_DSN")
	if dsn == "" {
		t.Skip("CATALOG_TEST_DSN not set")
	}
	pool, err := pgplat.New(context.Background(), config.Postgres{DSN: dsn, MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool)
}

func unique(base string) string { return base + " " + time.Now().Format("150405.000000") }

func kindOf(err error) apperr.Kind {
	if ae, ok := err.(*apperr.Error); ok {
		return ae.Kind
	}
	return ""
}

func TestSeededGroupsAreLinked(t *testing.T) {
	s := testStore(t)
	items, err := s.ListServices(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var kiloan *domain.ServiceItem
	for i := range items {
		if items[i].ID == 8 {
			kiloan = &items[i]
		}
	}
	if kiloan == nil || len(kiloan.VariantGroups) != 1 || len(kiloan.ModifierGroups) != 3 {
		t.Fatalf("service 8 = %+v; want one variant group and three modifier groups", kiloan)
	}
	v := kiloan.VariantGroups[0]
	if v.Name != "Kecepatan" || len(v.Options) != 2 || !v.Options[0].IsDefault || v.Options[0].PriceDelta != 0 || v.Options[1].PriceDelta != 3500 {
		t.Errorf("variant group = %+v", v)
	}
	if m := kiloan.ModifierGroups[0]; m.Name != "Penyelesaian" || m.Min != 1 || m.Max == nil || *m.Max != 1 {
		t.Errorf("first modifier group = %+v; want a required pick-one", m)
	}
	if m := kiloan.ModifierGroups[2]; m.Name != "Layanan Tambahan" || m.Min != 0 || m.Max != nil {
		t.Errorf("last modifier group = %+v; want optional and unbounded", m)
	}
}

func TestVariantGroupLifecycle(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Test svc"), Unit: "kg", Price: 10000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID) })

	g := domain.VariantGroup{Name: "Test speed", Options: []domain.VariantOption{
		{Name: "Slow", Active: true, IsDefault: true}, {Name: "Fast", PriceDelta: 5000, Active: true}, {Name: "Rocket", PriceDelta: 9000, Active: true}}}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateVariantGroup(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM variant_groups WHERE id = $1`, created.ID) })
	if len(created.Options) != 3 || created.Options[1].PriceDelta != 5000 {
		t.Fatalf("created = %+v", created)
	}

	// Link it; a service that is saved without the ids keeps its links.
	svc.VariantGroupIDs = []int64{created.ID}
	if svc, err = s.UpdateService(ctx, svc); err != nil || len(svc.VariantGroups) != 1 {
		t.Fatalf("link = %+v, %v", svc.VariantGroups, err)
	}
	svc.VariantGroupIDs = nil
	if svc, err = s.UpdateService(ctx, svc); err != nil || len(svc.VariantGroups) != 1 {
		t.Fatalf("an update without ids lost the links: %+v, %v", svc.VariantGroups, err)
	}
	svc.VariantGroupIDs = []int64{created.ID, 999999}
	if _, err = s.UpdateService(ctx, svc); kindOf(err) != apperr.KindInvalid {
		t.Errorf("an unknown group id: err = %v; want invalid", err)
	}

	// In use: refused, and the message names the service.
	err = s.DeleteVariantGroup(ctx, created.ID)
	if kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), svc.Name) {
		t.Fatalf("delete in use: %v; want a conflict naming %q", err, svc.Name)
	}

	// Replace: swap two names and the default, drop one option, add one; the kept options keep their ids.
	fast, slow := created.Options[1], created.Options[0]
	upd := domain.VariantGroup{ID: created.ID, Name: "Test speed 2", InternalNotes: " note ", Options: []domain.VariantOption{
		{ID: fast.ID, Name: "Slow", Active: true, IsDefault: true},
		{ID: slow.ID, Name: "Fast", PriceDelta: 1000, Active: true},
		{Name: "Turbo", PriceDelta: 2000, Active: true}}}
	if err := upd.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := s.UpdateVariantGroup(ctx, upd)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test speed 2" || got.InternalNotes != "note" || len(got.Options) != 3 || got.Options[0].ID != fast.ID || got.Options[0].Name != "Slow" ||
		!got.Options[0].IsDefault || got.Options[1].ID != slow.ID || got.Options[1].IsDefault || len(got.Services) != 1 {
		t.Errorf("updated = %+v", got)
	}
	if err := s.pool.QueryRow(ctx, `SELECT 1 FROM variant_options WHERE group_id=$1 AND name='Rocket'`, created.ID).Scan(new(int)); err == nil {
		t.Error("the option the form dropped is still there")
	}
	if _, err := s.UpdateVariantGroup(ctx, domain.VariantGroup{ID: created.ID, Name: "x", Options: []domain.VariantOption{{ID: 424242, Name: "A", Active: true, IsDefault: true}}}); kindOf(err) != apperr.KindInvalid {
		t.Errorf("an option of another group: %v; want invalid", err)
	}

	// Unlinked, it can go.
	svc.VariantGroupIDs = []int64{}
	if svc, err = s.UpdateService(ctx, svc); err != nil || len(svc.VariantGroups) != 0 {
		t.Fatalf("clearing the links: %+v, %v", svc.VariantGroups, err)
	}
	if err := s.DeleteVariantGroup(ctx, created.ID); err != nil {
		t.Fatalf("delete unused: %v", err)
	}
	if _, err := s.GetVariantGroup(ctx, created.ID); kindOf(err) != apperr.KindNotFound {
		t.Errorf("after delete: %v; want not found", err)
	}
}

func TestModifierGroupLifecycle(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Test svc"), Unit: "pcs", Price: 5000, Active: true,
		ModifierGroupIDs: []int64{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID) })

	name := unique("Test toppings")
	max := int32(2)
	g := domain.ModifierGroup{Name: name, Min: 1, Max: &max, Options: []domain.ModifierOption{
		{Name: "Cheese", Price: 3000, Active: true}, {Name: "Sauce", Active: true}, {Name: "Egg", Price: 4000, Active: false}}}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateModifierGroup(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM modifier_groups WHERE id = $1`, created.ID) })
	if created.Min != 1 || created.Max == nil || *created.Max != 2 || len(created.Options) != 3 {
		t.Fatalf("created = %+v", created)
	}
	if _, err := s.CreateModifierGroup(ctx, domain.ModifierGroup{Name: strings.ToUpper(name), Options: g.Options}); kindOf(err) != apperr.KindConflict {
		t.Errorf("a second group with the same name: %v; want a conflict", err)
	}

	svc.ModifierGroupIDs = []int64{created.ID}
	if svc, err = s.UpdateService(ctx, svc); err != nil || len(svc.ModifierGroups) != 1 || svc.ModifierGroups[0].Options[2].Active {
		t.Fatalf("link = %+v, %v", svc.ModifierGroups, err)
	}
	if err := s.DeleteModifierGroup(ctx, created.ID); kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), svc.Name) {
		t.Fatalf("delete in use: %v", err)
	}

	// The ceiling can be lifted and options replaced.
	upd := domain.ModifierGroup{ID: created.ID, Name: name, Min: 0, Options: []domain.ModifierOption{
		{ID: created.Options[0].ID, Name: "Cheese", Price: 3500, Active: true}, {Name: "Bacon", Price: 6000, Active: true}}}
	if err := upd.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := s.UpdateModifierGroup(ctx, upd)
	if err != nil || got.Max != nil || len(got.Options) != 2 || got.Options[0].ID != created.Options[0].ID || got.Options[0].Price != 3500 {
		t.Fatalf("updated = %+v, %v", got, err)
	}
	if err := s.DeleteModifierGroup(ctx, 424242); kindOf(err) != apperr.KindNotFound {
		t.Errorf("delete unknown: %v; want not found", err)
	}
}

func TestGroupListFilterSortAndPage(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("zq"), " ", "")
	var ids []int64
	for _, name := range []string{"B", "A", "C"} {
		g := domain.VariantGroup{Name: tag + name, InternalNotes: "note-" + name, Options: []domain.VariantOption{{Name: "Opt" + tag + name, Active: true}}}
		if err := g.Validate(); err != nil {
			t.Fatal(err)
		}
		made, err := s.CreateVariantGroup(ctx, g)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, made.ID)
		if made.CreatedAt.IsZero() || made.UpdatedAt.IsZero() {
			t.Errorf("timestamps missing: %+v", made)
		}
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM variant_groups WHERE id = ANY($1)`, ids) })
	q := func(f domain.GroupQuery) ([]domain.VariantGroup, int64) {
		t.Helper()
		if err := f.Validate(false); err != nil {
			t.Fatal(err)
		}
		out, total, err := s.ListVariantGroups(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return out, total
	}
	names := func(gs []domain.VariantGroup) string {
		var n []string
		for _, g := range gs {
			n = append(n, strings.TrimPrefix(g.Name, tag))
		}
		return strings.Join(n, ",")
	}

	all, total := q(domain.GroupQuery{Q: tag})
	if total != 3 || names(all) != "B,A,C" { // default order: id asc, the order they were created in
		t.Errorf("by id: %s (total %d); want B,A,C (3)", names(all), total)
	}
	if byName, _ := q(domain.GroupQuery{Q: strings.ToUpper(tag), SortBy: "name"}); names(byName) != "A,B,C" {
		t.Errorf("by name, search in another case: %s", names(byName))
	}
	if desc, _ := q(domain.GroupQuery{Q: tag, SortBy: "name", SortDir: "desc"}); names(desc) != "C,B,A" {
		t.Errorf("by name desc: %s", names(desc))
	}
	if byTime, _ := q(domain.GroupQuery{Q: tag, SortBy: "created_at", SortDir: "desc"}); names(byTime) != "C,A,B" {
		t.Errorf("by created_at desc: %s", names(byTime))
	}
	// A search reaches the note and the option names, and a % is only a character.
	if byNote, total := q(domain.GroupQuery{Q: "note-A"}); total < 1 || !strings.Contains(names(byNote), "A") {
		t.Errorf("by note: %s (total %d)", names(byNote), total)
	}
	if byOption, total := q(domain.GroupQuery{Q: "opt" + tag + "c"}); total != 1 || names(byOption) != "C" {
		t.Errorf("by option name: %s (total %d)", names(byOption), total)
	}
	if none, total := q(domain.GroupQuery{Q: tag + "%"}); total != 0 || len(none) != 0 {
		t.Errorf("a wildcard matched %d groups", total)
	}

	// Pages: the total is the whole match, and the pages do not overlap.
	first, total := q(domain.GroupQuery{Q: tag, SortBy: "name", Limit: 2})
	second, total2 := q(domain.GroupQuery{Q: tag, SortBy: "name", Limit: 2, Offset: 2})
	if total != 3 || total2 != 3 || names(first) != "A,B" || names(second) != "C" {
		t.Errorf("pages = %s | %s, totals %d %d; want A,B | C, 3 3", names(first), names(second), total, total2)
	}
	if past, total := q(domain.GroupQuery{Q: tag, Limit: 2, Offset: 10}); len(past) != 0 || total != 3 {
		t.Errorf("past the end: %d items, total %d", len(past), total)
	}
}

func TestModifierGroupListRequirement(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("zr"), " ", "")
	var ids []int64
	for _, c := range []struct {
		name string
		min  int32
	}{{"Req", 1}, {"Opt", 0}} {
		g := domain.ModifierGroup{Name: tag + c.name, Min: c.min, Options: []domain.ModifierOption{{Name: "x", Active: true}}}
		if err := g.Validate(); err != nil {
			t.Fatal(err)
		}
		made, err := s.CreateModifierGroup(ctx, g)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, made.ID)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM modifier_groups WHERE id = ANY($1)`, ids) })
	count := func(req string) (int64, string) {
		f := domain.GroupQuery{Q: tag, Requirement: req}
		if err := f.Validate(true); err != nil {
			t.Fatal(err)
		}
		out, total, err := s.ListModifierGroups(ctx, f)
		if err != nil || len(out) == 0 {
			return total, ""
		}
		return total, strings.TrimPrefix(out[0].Name, tag)
	}
	if total, first := count(""); total != 2 || first != "Req" {
		t.Errorf("all: %d, first %q", total, first)
	}
	if total, first := count("required"); total != 1 || first != "Req" {
		t.Errorf("required: %d, first %q", total, first)
	}
	if total, first := count("optional"); total != 1 || first != "Opt" {
		t.Errorf("optional: %d, first %q", total, first)
	}
}

func TestGroupListByServiceAndUsage(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("zs"), " ", "")
	mkSvc := func(name string) domain.ServiceItem {
		svc, err := s.CreateService(ctx, domain.ServiceItem{Name: tag + name, Unit: "kg", Price: 1000, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID) })
		return svc
	}
	one, two := mkSvc("One"), mkSvc("Two")
	var vids, mids []int64
	for _, n := range []string{"A", "B", "C"} {
		v, err := s.CreateVariantGroup(ctx, domain.VariantGroup{Name: tag + n, Options: []domain.VariantOption{{Name: "o", Active: true, IsDefault: true}}})
		if err != nil {
			t.Fatal(err)
		}
		m, err := s.CreateModifierGroup(ctx, domain.ModifierGroup{Name: tag + n, Options: []domain.ModifierOption{{Name: "o", Active: true}}})
		if err != nil {
			t.Fatal(err)
		}
		vids, mids = append(vids, v.ID), append(mids, m.ID)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM variant_groups WHERE id = ANY($1)`, vids)
		_, _ = s.pool.Exec(ctx, `DELETE FROM modifier_groups WHERE id = ANY($1)`, mids)
	})
	// A is on both services, B only on the first, C on none.
	link := func(svc domain.ServiceItem, v, m []int64) {
		svc.VariantGroupIDs, svc.ModifierGroupIDs = v, m
		if _, err := s.UpdateService(ctx, svc); err != nil {
			t.Fatal(err)
		}
	}
	link(one, vids[:2], mids[:2])
	link(two, vids[:1], mids[:1])

	suffixes := func(f domain.GroupQuery, modifiers bool) (string, int64) {
		t.Helper()
		f.Q = tag
		if err := f.Validate(modifiers); err != nil {
			t.Fatal(err)
		}
		var names []string
		var total int64
		if modifiers {
			out, n, err := s.ListModifierGroups(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			total = n
			for _, g := range out {
				names = append(names, strings.TrimPrefix(g.Name, tag))
			}
		} else {
			out, n, err := s.ListVariantGroups(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			total = n
			for _, g := range out {
				names = append(names, strings.TrimPrefix(g.Name, tag))
			}
		}
		return strings.Join(names, ","), total
	}
	for _, modifiers := range []bool{false, true} {
		kind := map[bool]string{false: "variant", true: "modifier"}[modifiers]
		check := func(label string, f domain.GroupQuery, want string, wantTotal int64) {
			t.Helper()
			if got, total := suffixes(f, modifiers); got != want || total != wantTotal {
				t.Errorf("%s %s: got %q (total %d); want %q (%d)", kind, label, got, total, want, wantTotal)
			}
		}
		check("all", domain.GroupQuery{}, "A,B,C", 3)
		check("on the first service", domain.GroupQuery{ServiceID: one.ID}, "A,B", 2)
		check("on the second service", domain.GroupQuery{ServiceID: two.ID}, "A", 1)
		check("on an unknown service", domain.GroupQuery{ServiceID: 987654321}, "", 0)
		check("used", domain.GroupQuery{Usage: "used"}, "A,B", 2) // A is on two services but counts once
		check("unused", domain.GroupQuery{Usage: "unused"}, "C", 1)
		check("service and unused", domain.GroupQuery{ServiceID: one.ID, Usage: "unused"}, "", 0)
		check("service, sorted, second page", domain.GroupQuery{ServiceID: one.ID, SortBy: "name", SortDir: "desc", Limit: 1, Offset: 1}, "A", 2)
		check("used, first page", domain.GroupQuery{Usage: "used", Limit: 1}, "A", 2)
	}
	// Combined with the search text: only what also matches q.
	f := domain.GroupQuery{Q: tag + "B", ServiceID: one.ID}
	if err := f.Validate(false); err != nil {
		t.Fatal(err)
	}
	if out, total, err := s.ListVariantGroups(ctx, f); err != nil || total != 1 || len(out) != 1 || out[0].Name != tag+"B" {
		t.Errorf("q with service: %+v, total %d, %v", out, total, err)
	}
}

// A service's tax type is written on create and update, read back, and filterable in the paged list.
func TestServiceTaxTypeRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	created, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Tax ppn"), Unit: domain.UnitKg, Price: 1000, Active: true, TaxType: domain.TaxPPN})
	if err != nil {
		t.Fatal(err)
	}
	if created.TaxType != domain.TaxPPN {
		t.Fatalf("created tax_type = %q, want ppn", created.TaxType)
	}
	page, _, err := s.PageServices(ctx, domain.ServiceQuery{TaxType: domain.TaxPPN, Q: created.Name, SortBy: "id", Limit: 10})
	if err != nil || len(page) != 1 || page[0].ID != created.ID {
		t.Fatalf("filter ppn: %v %+v", err, page)
	}
	created.TaxType = domain.TaxNone
	updated, err := s.UpdateService(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.TaxType != domain.TaxNone {
		t.Fatalf("updated tax_type = %q, want none", updated.TaxType)
	}
	page, _, err = s.PageServices(ctx, domain.ServiceQuery{TaxType: domain.TaxPPN, Q: created.Name, SortBy: "id", Limit: 10})
	if err != nil || len(page) != 0 {
		t.Fatalf("after update, filter ppn should be empty: %v %+v", err, page)
	}
	// The database refuses an unknown type even if a caller skips the domain check.
	if _, err := s.pool.Exec(ctx, `UPDATE services SET tax_type = 'vat' WHERE id = $1`, created.ID); err == nil {
		t.Error("the database accepted tax_type 'vat'")
	}
}

// The price a service stores is the one before tax; reads carry the rate and the price after it, and the notes round-trip.
func TestServiceReadsPriceAfterTaxAndNotes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	created, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("Tax calc"), Unit: domain.UnitKg, Price: 10000, Active: true, TaxType: domain.TaxPPN, InternalNotes: "dikirim ke hotel"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Price != 10000 || created.TaxRateBps != 1100 || created.PriceAfterTax != 11100 || created.InternalNotes != "dikirim ke hotel" {
		t.Fatalf("created: %+v", created)
	}
	// The search finds a service by its note.
	page, _, err := s.PageServices(ctx, domain.ServiceQuery{Q: "ke hotel", SortBy: "id", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range page {
		if it.ID == created.ID {
			found = it.PriceAfterTax == 11100
		}
	}
	if !found {
		t.Errorf("a search for the note should find the service with its price after tax: %+v", page)
	}
	rates, err := s.TaxRates(ctx)
	if err != nil || rates[domain.TaxNone] != 0 || rates[domain.TaxPPN] != 1100 {
		t.Errorf("rates: %v %v", rates, err)
	}
	// The customer menu: the price is what the customer pays.
	d := NewDiscovery(s.pool)
	if _, err := s.pool.Exec(ctx, `UPDATE services SET service_category = 'regular' WHERE id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := d.ServiceDetail(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	v := detail.ServiceView
	if v.Price != 11100 || v.PriceBeforeTax != 10000 || v.PriceAfterTax != 11100 || v.TaxType != domain.TaxPPN || v.TaxRateBps != 1100 {
		t.Errorf("customer view: %+v", v)
	}
}

// A discount comes off the list price first and tax is added to what is left; the customer menu has it taken off, with the
// app's own `discount` and `discount_type` derived from it, and a tier is the discounted price plus the option's extra.
func TestServiceDiscountPipelineAndCustomerView(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	created, err := s.CreateService(ctx, domain.ServiceItem{
		Name: unique("Disc pct"), Unit: domain.UnitKg, Price: 10000, Active: true, TaxType: domain.TaxPPN,
		DiscountType: domain.DiscountPercent, DiscountValue: 1000, // 10%
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.DiscountType != domain.DiscountPercent || created.DiscountValue != 1000 || created.DiscountAmount != 1000 ||
		created.PriceAfterDiscount != 9000 || created.PriceAfterTax != 9990 || created.Price != 10000 {
		t.Fatalf("created: %+v", created)
	}
	// The discount filter finds it, and a value discount on the same row replaces the percentage.
	page, _, err := s.PageServices(ctx, domain.ServiceQuery{DiscountType: domain.DiscountPercent, Q: created.Name, SortBy: "id", Limit: 10})
	if err != nil || len(page) != 1 || page[0].ID != created.ID {
		t.Fatalf("filter percent: %v %+v", err, page)
	}
	created.DiscountType, created.DiscountValue = domain.DiscountValue, 2500
	updated, err := s.UpdateService(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DiscountAmount != 2500 || updated.PriceAfterDiscount != 7500 || updated.PriceAfterTax != 8325 {
		t.Fatalf("updated: %+v", updated)
	}
	page, _, err = s.PageServices(ctx, domain.ServiceQuery{DiscountType: domain.DiscountPercent, Q: created.Name, SortBy: "id", Limit: 10})
	if err != nil || len(page) != 0 {
		t.Fatalf("filter percent after the change: %v %+v", err, page)
	}
	// The database refuses a discount larger than the price (the domain refuses it first).
	updated.Price = 1000
	if _, err := s.UpdateService(ctx, updated); err == nil {
		t.Error("a 2.500 discount on a 1.000 price must be refused")
	}

	// The customer menu.
	if _, err := s.pool.Exec(ctx, `UPDATE services SET service_category = 'regular' WHERE id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := NewDiscovery(s.pool).ServiceDetail(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	v := detail.ServiceView
	if v.Price != 8325 || v.PriceBeforeTax != 10000 || v.DiscountAmount != 2500 || v.PriceAfterDiscount != 7500 || v.PriceAfterTax != 8325 ||
		v.Discount != 2500 || v.DiscountType != "fixed" {
		t.Errorf("customer view: %+v", v)
	}
}
