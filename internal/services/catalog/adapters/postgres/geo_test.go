package postgres

import (
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

func box(w, s, e, n float64) geometry {
	return geometry{Type: "Polygon", Coordinates: [][][2]float64{{{w, s}, {e, s}, {e, n}, {w, n}, {w, s}}}}
}

func TestGeometryContains(t *testing.T) {
	g := box(106.80, -6.215, 106.88, -6.18) // Jakarta Pusat, as seeded
	cases := []struct {
		name string
		p    domain.Point
		want bool
	}{
		{"inside", domain.Point{Latitude: -6.2088, Longitude: 106.8456}, true},
		{"east of it", domain.Point{Latitude: -6.2088, Longitude: 106.95}, false},
		{"north of it", domain.Point{Latitude: -6.10, Longitude: 106.84}, false},
	}
	for _, c := range cases {
		if got := g.contains(c.p); got != c.want {
			t.Errorf("%s: contains = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGeometryHoleIsOutside(t *testing.T) {
	g := box(0, 0, 10, 10)
	g.Coordinates = append(g.Coordinates, box(4, 4, 6, 6).Coordinates[0])
	if g.contains(domain.Point{Latitude: 5, Longitude: 5}) {
		t.Error("a point in the hole should be outside")
	}
	if !g.contains(domain.Point{Latitude: 1, Longitude: 1}) {
		t.Error("a point in the ring but outside the hole should be inside")
	}
}

func TestParseGeometry(t *testing.T) {
	g, err := parseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}`))
	if err != nil || !g.contains(domain.Point{Latitude: 1, Longitude: 1}) {
		t.Fatalf("parse/contains failed: %v", err)
	}
	if _, err := parseGeometry([]byte(`not json`)); err == nil {
		t.Error("invalid JSON should fail")
	}
}

func TestHaversineKm(t *testing.T) {
	// Monas to Blok M is roughly 7.5 km as the crow flies.
	d := haversineKm(domain.Point{Latitude: -6.1754, Longitude: 106.8272}, domain.Point{Latitude: -6.2440, Longitude: 106.7994})
	if d < 7 || d > 8.5 {
		t.Errorf("distance = %.2f km, want about 7.5", d)
	}
}

func TestTTLCache(t *testing.T) {
	now := time.Unix(0, 0)
	c := newTTLCache[string, int](10 * time.Second)
	c.now = func() time.Time { return now }

	if _, ok := c.get("a"); ok {
		t.Fatal("empty cache should miss")
	}
	c.set("a", 1)
	if v, ok := c.get("a"); !ok || v != 1 {
		t.Fatalf("get = %v, %v; want 1, true", v, ok)
	}
	now = now.Add(9 * time.Second)
	if _, ok := c.get("a"); !ok {
		t.Fatal("entry should still be fresh at 9s")
	}
	now = now.Add(2 * time.Second)
	if _, ok := c.get("a"); ok {
		t.Fatal("entry should have expired at 11s")
	}
}
