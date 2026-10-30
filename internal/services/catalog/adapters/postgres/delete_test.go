package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Deleting a service takes its group links and its own recipe with it, and nothing else: the groups, their options' recipes and the
// material stay. An unknown id is a 404. (Needs CATALOG_TEST_DSN, like the other store tests.)
func TestDeleteServiceTakesItsLinksAndRecipe(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	tag := strings.ReplaceAll(unique("DS"), " ", "")
	mat := mustMaterial(t, s, tag+"-M", "ml", 1000, "100")

	vg, err := s.CreateVariantGroup(ctx, domain.VariantGroup{Name: unique("ZZ del v"), Options: []domain.VariantOption{{Name: "Reguler", IsDefault: true, Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM bom_lines WHERE owner_type = 'variant_option' AND owner_id = ANY(SELECT id FROM variant_options WHERE group_id = $1)`, vg.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM variant_groups WHERE id = $1`, vg.ID)
	})
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("ZZ del"), Unit: domain.UnitKg, Price: 5000, Active: true, VariantGroupIDs: []int64{vg.ID}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID) })
	// The service's own recipe, and the recipe of the shared group's option (which must survive).
	if _, err := setBOM(t, s, "service", svc.ID, domain.BOMLineInput{MaterialID: mat.ID, Qty: "1", Per: "unit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := setBOM(t, s, "variant_option", vg.Options[0].ID, domain.BOMLineInput{MaterialID: mat.ID, Qty: "2", Per: "unit"}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteService(ctx, svc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := s.pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM services WHERE id = $1`, svc.ID); n != 0 {
		t.Errorf("the service is still there (%d)", n)
	}
	if n := count(`SELECT count(*) FROM service_variant_groups WHERE service_id = $1`, svc.ID); n != 0 {
		t.Errorf("%d group links left behind", n)
	}
	if n := count(`SELECT count(*) FROM bom_lines WHERE owner_type = 'service' AND owner_id = $1`, svc.ID); n != 0 {
		t.Errorf("%d recipe lines of the service left behind", n)
	}
	if n := count(`SELECT count(*) FROM variant_groups WHERE id = $1`, vg.ID); n != 1 {
		t.Errorf("the shared group went with the service")
	}
	if n := count(`SELECT count(*) FROM bom_lines WHERE owner_type = 'variant_option' AND owner_id = $1`, vg.Options[0].ID); n != 1 {
		t.Errorf("the shared group's option recipe went with the service")
	}
	if err := s.DeleteService(ctx, svc.ID); kindOf(err) != apperr.KindNotFound {
		t.Errorf("deleting it again: %v; want not found", err)
	}
}

// An outlet nothing points at is deleted with its opening hours; one with stock records is refused with a conflict that says to switch
// it off, and is left as it was; an unknown id is a 404.
func TestDeleteOutlet(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	free, err := s.CreateOutlet(ctx, domain.Outlet{Name: unique("ZZ del free"), Latitude: -6.2, Longitude: 106.8, Active: true, Category: "all", DeliveryTypes: []string{"pickup"}, Timezone: "Asia/Jakarta", Hours: domain.SameEveryDay("08:00", "22:00")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM outlets WHERE id = $1`, free.ID) })
	if err := s.DeleteOutlet(ctx, free.ID); err != nil {
		t.Fatalf("delete a free outlet: %v", err)
	}
	var left int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM outlets WHERE id = $1) + (SELECT count(*) FROM outlet_hours WHERE outlet_id = $1)`, free.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("rows left after the delete: %d, %v", left, err)
	}
	if err := s.DeleteOutlet(ctx, free.ID); kindOf(err) != apperr.KindNotFound {
		t.Errorf("deleting it again: %v; want not found", err)
	}

	// With stock on its books it stays.
	used, err := s.CreateOutlet(ctx, domain.Outlet{Name: unique("ZZ del used"), Latitude: -6.2, Longitude: 106.8, Active: true, Category: "all", DeliveryTypes: []string{"pickup"}, Timezone: "Asia/Jakarta", Hours: domain.SameEveryDay("08:00", "22:00")})
	if err != nil {
		t.Fatal(err)
	}
	// Registered before the material, so it runs after the material's cleanup has purged the stock that points at the outlet.
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM outlets WHERE id = $1`, used.ID) })
	mat := mustMaterial(t, s, strings.ReplaceAll(unique("DO"), " ", ""), "ml", 1000, "100")
	receive(t, s, used.ID, mat.ID, "10", 1000, "del-outlet-"+tag(used.ID))
	err = s.DeleteOutlet(ctx, used.ID)
	if kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), "switch it off") {
		t.Fatalf("delete an outlet with stock: %v; want a conflict that says to switch it off", err)
	}
	if _, err := s.GetOutlet(ctx, used.ID); err != nil {
		t.Errorf("the refused outlet is gone: %v", err)
	}
}

func tag(id int64) string { return strings.TrimSpace(strings.ReplaceAll(unique(itoa(id)), " ", "-")) }
