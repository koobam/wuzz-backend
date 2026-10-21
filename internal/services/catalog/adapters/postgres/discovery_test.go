package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
	pgplat "github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// These need a catalog database that has been migrated and seeded with
// scripts/seed-local.sql. Set CATALOG_TEST_DSN to run them; they are skipped
// otherwise, so a plain `go test ./...` needs no database.
func testDiscovery(t *testing.T) *Discovery {
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
	return NewDiscovery(pool)
}

var bloKM = &domain.Point{Latitude: -6.2440, Longitude: 106.7994}

func TestNearbyNearestFirst(t *testing.T) {
	d := testDiscovery(t)
	out, next, err := d.Nearby(context.Background(), domain.NearbyQuery{Near: bloKM, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || next != "3" {
		t.Fatalf("got %d outlets, next %q; want 3, \"3\"", len(out), next)
	}
	if out[0].Name != "Wuzz Station Blok M" || !out[0].HasDistance || out[0].DistanceKm > 0.1 {
		t.Errorf("nearest = %+v; want Blok M at ~0 km", out[0])
	}
	for i := 1; i < len(out); i++ {
		if out[i].DistanceKm < out[i-1].DistanceKm {
			t.Errorf("not sorted by distance: %v then %v", out[i-1].DistanceKm, out[i].DistanceKm)
		}
	}
}

func TestNearbyPagesWithoutRepeats(t *testing.T) {
	d := testDiscovery(t)
	seen := map[int64]bool{}
	cursor := ""
	for page := 0; page < 10; page++ {
		out, next, err := d.Nearby(context.Background(), domain.NearbyQuery{Near: bloKM, Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range out {
			if seen[o.ID] {
				t.Fatalf("outlet %d returned twice", o.ID)
			}
			seen[o.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) < 7 {
		t.Errorf("paged through %d outlets, want at least the 7 seeded", len(seen))
	}
}

func TestNearbyTextAndNoPosition(t *testing.T) {
	d := testDiscovery(t)
	out, _, err := d.Nearby(context.Background(), domain.NearbyQuery{Text: "kemang", Limit: 10})
	if err != nil || len(out) != 1 || out[0].Name != "Wuzz Kemang" || out[0].HasDistance {
		t.Fatalf("text search = %+v, %v; want just Wuzz Kemang without distance", out, err)
	}
	// "%" must be literal, not a wildcard.
	if out, _, _ := d.Nearby(context.Background(), domain.NearbyQuery{Text: "%", Limit: 10}); len(out) != 0 {
		t.Errorf("a lone %% matched %d outlets", len(out))
	}
	if _, _, err := d.Nearby(context.Background(), domain.NearbyQuery{Limit: 3, Cursor: "x"}); err == nil {
		t.Error("a bad cursor should be rejected")
	}
}

func TestByIDAndCoverage(t *testing.T) {
	d := testDiscovery(t)
	o, err := d.ByID(context.Background(), 2, bloKM)
	if err != nil || o.Name != "Wuzz Station Blok M" || !o.HasDistance {
		t.Fatalf("ByID = %+v, %v", o, err)
	}
	if _, err := d.ByID(context.Background(), 9999, nil); err == nil {
		t.Error("an unknown outlet should be not found")
	}
	area, ok, err := d.Coverage(context.Background(), domain.Point{Latitude: -6.2088, Longitude: 106.8456})
	if err != nil || !ok || area.Name != "Jakarta Pusat" {
		t.Fatalf("Coverage = %+v, %v, %v; want Jakarta Pusat", area, ok, err)
	}
	if _, ok, _ := d.Coverage(context.Background(), domain.Point{Latitude: 35, Longitude: 139}); ok {
		t.Error("Tokyo should be outside every area")
	}
}

func TestServicesAndDetail(t *testing.T) {
	d := testDiscovery(t)
	all, err := d.Services(context.Background(), "", 0)
	if err != nil || len(all) != 14 {
		t.Fatalf("menu = %d services, %v; want the 14 seeded", len(all), err)
	}
	if all[0].Name != "Mesin Cuci" && all[0].Name != "Cuci Pakaian Kiloan" {
		t.Errorf("first = %q; menu should be in sort order", all[0].Name)
	}
	regular, _ := d.Services(context.Background(), domain.ServiceCategoryRegular, 0)
	alacarte, _ := d.Services(context.Background(), domain.ServiceCategoryAlacarte, 0)
	if len(regular)+len(alacarte) != len(all) || len(alacarte) != 2 {
		t.Errorf("categories: %d regular + %d alacarte of %d", len(regular), len(alacarte), len(all))
	}
	if limited, _ := d.Services(context.Background(), "", 3); len(limited) != 3 {
		t.Errorf("limit 3 returned %d", len(limited))
	}

	detail, err := d.ServiceDetail(context.Background(), 8)
	if err != nil || detail.Name != "Cuci Pakaian Kiloan" {
		t.Fatalf("detail = %+v, %v", detail.ServiceView, err)
	}
	if len(detail.Tiers) != 2 || detail.Tiers[1].Pricing == nil || detail.Tiers[1].Pricing.Price != 10500 {
		t.Errorf("tiers = %+v; want Reguler and Express at 1.5x", detail.Tiers)
	}
	if len(detail.Sections) != 3 || detail.Sections[0].Title != "Penyelesaian" || len(detail.Sections[0].Addons) != 2 {
		t.Errorf("sections = %+v", detail.Sections)
	}
	if _, err := d.ServiceDetail(context.Background(), 1); err == nil {
		t.Error("a retired service should be not found")
	}
}
