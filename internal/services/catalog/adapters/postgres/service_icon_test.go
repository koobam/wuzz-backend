package postgres

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// An icon picture is written on create, kept by an update that does not set it, replaced or removed by one that does, and shown on
// the customer menu. (Needs CATALOG_TEST_DSN, like the other store tests.)
func TestServiceIconPicture(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	const id1, url1 = "6f9619ff-8b86-d011-b42d-00c04fc964ff", "https://assets.test/services/2026/11/a.webp"
	const id2, url2 = "0b0b0b0b-0000-4000-8000-000000000000", "https://assets.test/services/2026/11/b.webp"

	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("ZZ icon"), Unit: domain.UnitKg, Price: 7000, Active: true,
		TaxType: domain.TaxNone, DiscountType: domain.DiscountNone, IconMediaID: id1, IconURL: url1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID) })
	if svc.IconMediaID != id1 || svc.IconURL != url1 {
		t.Fatalf("create: got %q %q", svc.IconMediaID, svc.IconURL)
	}

	update := func(set bool, id, url string) domain.ServiceItem {
		t.Helper()
		it := svc
		it.SetIcon, it.IconMediaID, it.IconURL = set, id, url
		got, err := s.UpdateService(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := update(false, "", ""); got.IconMediaID != id1 || got.IconURL != url1 {
		t.Fatalf("an update that does not set the icon must keep it, got %q %q", got.IconMediaID, got.IconURL)
	}
	if got := update(true, id2, url2); got.IconMediaID != id2 || got.IconURL != url2 {
		t.Fatalf("replace: got %q %q", got.IconMediaID, got.IconURL)
	}

	// The customer menu shows it once the service is listed there.
	if _, err := s.pool.Exec(ctx, `UPDATE services SET service_category = 'regular' WHERE id = $1`, svc.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := NewDiscovery(s.pool).ServiceDetail(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.IconURL != url2 {
		t.Fatalf("the menu must carry the icon picture, got %q", detail.IconURL)
	}

	if got := update(true, "", ""); got.IconMediaID != "" || got.IconURL != "" {
		t.Fatalf("remove: got %q %q", got.IconMediaID, got.IconURL)
	}
	// Half a picture is refused by the table itself, whatever writes it.
	if _, err := s.pool.Exec(ctx, `UPDATE services SET icon_media_id = $2 WHERE id = $1`, svc.ID, id1); err == nil {
		t.Fatal("an id without an address must be refused by the table")
	}
}
