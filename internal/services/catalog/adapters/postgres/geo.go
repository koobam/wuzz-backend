package postgres

import (
	"encoding/json"
	"math"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// geometry is the part of a GeoJSON Polygon the service needs: an outer ring
// followed by any holes, each a list of [longitude, latitude] pairs.
type geometry struct {
	Type        string         `json:"type"`
	Coordinates [][][2]float64 `json:"coordinates"`
}

func parseGeometry(raw []byte) (geometry, error) {
	var g geometry
	err := json.Unmarshal(raw, &g)
	return g, err
}

// contains reports whether p lies inside the polygon: inside the outer ring and
// outside every hole.
func (g geometry) contains(p domain.Point) bool {
	if len(g.Coordinates) == 0 || !ringContains(g.Coordinates[0], p) {
		return false
	}
	for _, hole := range g.Coordinates[1:] {
		if ringContains(hole, p) {
			return false
		}
	}
	return true
}

// ringContains is the even-odd rule: cast a ray east from p and count crossings.
func ringContains(ring [][2]float64, p domain.Point) bool {
	inside := false
	for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
		xi, yi := ring[i][0], ring[i][1]
		xj, yj := ring[j][0], ring[j][1]
		if (yi > p.Latitude) != (yj > p.Latitude) &&
			p.Longitude < (xj-xi)*(p.Latitude-yi)/(yj-yi)+xi {
			inside = !inside
		}
	}
	return inside
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

func haversineKm(a, b domain.Point) float64 {
	const earthKm = 6371.0088
	rad := math.Pi / 180
	dLat := (b.Latitude - a.Latitude) * rad
	dLng := (b.Longitude - a.Longitude) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a.Latitude*rad)*math.Cos(b.Latitude*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthKm * math.Asin(math.Sqrt(h))
}
