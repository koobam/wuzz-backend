package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// How long the menu and the service areas are served from memory.
const menuTTL = 30 * time.Second

// Discovery answers the customer app's browsing queries from Postgres: nearby
// outlets, the service menu, a service's detail and which area a point is in.
type Discovery struct {
	pool *postgres.Pool

	menu   *ttlCache[menuKey, []domain.ServiceView]
	detail *ttlCache[int64, domain.ServiceDetail]
	areas  *ttlCache[struct{}, []area]
}

type menuKey struct {
	category string
	limit    int
}

type area struct {
	domain.CoverageArea
	geometry geometry
}

func NewDiscovery(pool *postgres.Pool) *Discovery {
	return &Discovery{
		pool:   pool,
		menu:   newTTLCache[menuKey, []domain.ServiceView](menuTTL),
		detail: newTTLCache[int64, domain.ServiceDetail](menuTTL),
		areas:  newTTLCache[struct{}, []area](menuTTL),
	}
}

const outletViewCols = `id, name, address, category, latitude, longitude, images,
	current_status, delivery_types, open_time, close_time`

func scanOutletView(r pgx.Row, near *domain.Point, withDistance bool) (domain.OutletView, error) {
	var v domain.OutletView
	var dist *float64
	dest := []any{&v.ID, &v.Name, &v.Address, &v.Category, &v.Latitude, &v.Longitude, &v.Images,
		&v.CurrentStatus, &v.DeliveryTypes, &v.OpenTime, &v.CloseTime}
	if withDistance {
		dest = append(dest, &dist)
	}
	if err := r.Scan(dest...); err != nil {
		return v, err
	}
	switch {
	case dist != nil:
		v.HasDistance, v.DistanceKm = true, round2(*dist)
	case near != nil:
		v.HasDistance = true
		v.DistanceKm = round2(haversineKm(*near, domain.Point{Latitude: v.Latitude, Longitude: v.Longitude}))
	}
	return v, nil
}

// likeEscaper makes user text literal inside ILIKE.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Nearby pages through active outlets: nearest first when a position is given,
// by name otherwise. The cursor is an offset.
func (d *Discovery) Nearby(ctx context.Context, q domain.NearbyQuery) ([]domain.OutletView, string, error) {
	offset := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil || n < 0 {
			return nil, "", apperr.Invalid("The cursor is invalid")
		}
		offset = n
	}

	var lat, lng *float64
	if q.Near != nil {
		lat, lng = &q.Near.Latitude, &q.Near.Longitude
	}
	pattern := ""
	if q.Text != "" {
		pattern = "%" + likeEscaper.Replace(q.Text) + "%"
	}

	// Great-circle distance in km, worked out in SQL so the database does the
	// ordering. NULL when no position was given, which sorts by name instead.
	rows, err := d.pool.Query(ctx, `
		SELECT `+outletViewCols+`,
			CASE WHEN $1::float8 IS NULL THEN NULL ELSE
				2 * 6371.0088 * asin(sqrt(least(1,
					power(sin(radians(latitude - $1::float8) / 2), 2) +
					cos(radians($1::float8)) * cos(radians(latitude)) *
					power(sin(radians(longitude - $2::float8) / 2), 2))))
			END AS distance_km
		FROM outlets
		WHERE active
		  AND ($3 = '' OR name ILIKE $3 OR address ILIKE $3)
		ORDER BY distance_km NULLS LAST, name, id
		OFFSET $4 LIMIT $5`,
		lat, lng, pattern, offset, q.Limit+1) // one extra row says whether another page exists
	if err != nil {
		return nil, "", apperr.Internal("Could not search outlets").WithCause(err)
	}
	defer rows.Close()

	out := make([]domain.OutletView, 0, q.Limit+1)
	for rows.Next() {
		v, err := scanOutletView(rows, q.Near, true)
		if err != nil {
			return nil, "", apperr.Internal("Could not read outlets").WithCause(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, "", apperr.Internal("Could not read outlets").WithCause(err)
	}

	next := ""
	if len(out) > q.Limit {
		out = out[:q.Limit]
		next = strconv.Itoa(offset + q.Limit)
	}
	return out, next, nil
}

func (d *Discovery) ByID(ctx context.Context, id int64, near *domain.Point) (domain.OutletView, error) {
	v, err := scanOutletView(d.pool.QueryRow(ctx,
		`SELECT `+outletViewCols+` FROM outlets WHERE id = $1 AND active`, id), near, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OutletView{}, apperr.NotFound("Outlet not found")
	}
	if err != nil {
		return domain.OutletView{}, apperr.Internal("Could not load the outlet").WithCause(err)
	}
	return v, nil
}

// Coverage finds the active service area whose polygon contains p. The areas are
// few, so they are read once per ttl and tested here.
func (d *Discovery) Coverage(ctx context.Context, p domain.Point) (domain.CoverageArea, bool, error) {
	areas, err := d.loadAreas(ctx)
	if err != nil {
		return domain.CoverageArea{}, false, err
	}
	for _, a := range areas {
		if a.geometry.contains(p) {
			return a.CoverageArea, true, nil
		}
	}
	return domain.CoverageArea{}, false, nil
}

func (d *Discovery) loadAreas(ctx context.Context) ([]area, error) {
	if cached, ok := d.areas.get(struct{}{}); ok {
		return cached, nil
	}
	rows, err := d.pool.Query(ctx, `SELECT id, name, geometry FROM coverage_areas WHERE active ORDER BY id`)
	if err != nil {
		return nil, apperr.Internal("Could not resolve the service area").WithCause(err)
	}
	defer rows.Close()
	var out []area
	for rows.Next() {
		var a area
		var raw []byte
		if err := rows.Scan(&a.ID, &a.Name, &raw); err != nil {
			return nil, apperr.Internal("Could not read the service areas").WithCause(err)
		}
		if a.geometry, err = parseGeometry(raw); err != nil {
			return nil, apperr.Internal("A service area has invalid geometry").WithCause(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("Could not read the service areas").WithCause(err)
	}
	d.areas.set(struct{}{}, out)
	return out, nil
}

const serviceViewCols = `id, name, description, price, unit, service_type, service_category,
	min_kg, max_kg, discount, discount_type, icon, image`

// Services lists the active menu, in display order. category "" means all.
func (d *Discovery) Services(ctx context.Context, category string, limit int) ([]domain.ServiceView, error) {
	key := menuKey{category, limit}
	if cached, ok := d.menu.get(key); ok {
		return append([]domain.ServiceView(nil), cached...), nil
	}
	rows, err := d.pool.Query(ctx, `
		SELECT `+serviceViewCols+` FROM services
		WHERE active AND service_category IS NOT NULL AND ($1 = '' OR service_category = $1)
		ORDER BY sort_order, id
		LIMIT NULLIF($2, 0)`, category, limit)
	if err != nil {
		return nil, apperr.Internal("Could not list services").WithCause(err)
	}
	defer rows.Close()
	out := []domain.ServiceView{}
	for rows.Next() {
		var cat *string
		var v domain.ServiceView
		if err := rows.Scan(&v.ID, &v.Name, &v.Description, &v.Price, &v.Unit, &v.ServiceType, &cat,
			&v.MinKg, &v.MaxKg, &v.Discount, &v.DiscountType, &v.Icon, &v.Image); err != nil {
			return nil, apperr.Internal("Could not read services").WithCause(err)
		}
		if cat != nil {
			v.ServiceCategory = *cat
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("Could not read services").WithCause(err)
	}
	d.menu.set(key, out)
	return append([]domain.ServiceView(nil), out...), nil
}

// The shapes tiers and sections are stored in (JSON on the service row).
type tierJSON struct {
	ID        int64        `json:"id"`
	Code      string       `json:"code"`
	Name      string       `json:"name"`
	SortOrder int32        `json:"sort_order"`
	Pricing   *pricingJSON `json:"pricing"`
}

type pricingJSON struct {
	ID                int64   `json:"id"`
	ServiceID         int64   `json:"service_id"`
	TierID            int64   `json:"tier_id"`
	SKU               string  `json:"sku"`
	EstimatedTime     int32   `json:"estimated_time"`
	EstimatedTimeUnit string  `json:"estimated_time_unit"`
	Price             int64   `json:"price"`
	Discount          float64 `json:"discount"`
	DiscountType      string  `json:"discount_type"`
	Unit              string  `json:"unit"`
}

type addonJSON struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Price        int64   `json:"price"`
	Unit         string  `json:"unit"`
	Discount     float64 `json:"discount"`
	DiscountType string  `json:"discount_type"`
	Icon         string  `json:"icon"`
}

type sectionJSON struct {
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Type        string      `json:"type"`
	Addons      []addonJSON `json:"addons"`
}

func (d *Discovery) ServiceDetail(ctx context.Context, id int64) (domain.ServiceDetail, error) {
	if cached, ok := d.detail.get(id); ok {
		return cached, nil
	}
	var tiersRaw, sectionsRaw []byte
	var cat *string
	var v domain.ServiceView
	err := d.pool.QueryRow(ctx, `
		SELECT `+serviceViewCols+`, tiers, sections FROM services
		WHERE id = $1 AND active AND service_category IS NOT NULL`, id).
		Scan(&v.ID, &v.Name, &v.Description, &v.Price, &v.Unit, &v.ServiceType, &cat,
			&v.MinKg, &v.MaxKg, &v.Discount, &v.DiscountType, &v.Icon, &v.Image, &tiersRaw, &sectionsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ServiceDetail{}, apperr.NotFound("Service not found")
	}
	if err != nil {
		return domain.ServiceDetail{}, apperr.Internal("Could not load the service").WithCause(err)
	}
	if cat != nil {
		v.ServiceCategory = *cat
	}

	var tiers []tierJSON
	var sections []sectionJSON
	if err := json.Unmarshal(tiersRaw, &tiers); err != nil {
		return domain.ServiceDetail{}, apperr.Internal("The service tiers are invalid").WithCause(err)
	}
	if err := json.Unmarshal(sectionsRaw, &sections); err != nil {
		return domain.ServiceDetail{}, apperr.Internal("The service sections are invalid").WithCause(err)
	}

	detail := domain.ServiceDetail{ServiceView: v}
	for _, t := range tiers {
		tier := domain.ServiceTier{ID: t.ID, Code: t.Code, Name: t.Name, SortOrder: t.SortOrder}
		if p := t.Pricing; p != nil {
			tier.Pricing = &domain.ServiceTierPricing{
				ID: p.ID, ServiceID: p.ServiceID, TierID: p.TierID, SKU: p.SKU,
				EstimatedTime: p.EstimatedTime, EstimatedTimeUnit: p.EstimatedTimeUnit,
				Price: p.Price, Discount: p.Discount, DiscountType: p.DiscountType, Unit: p.Unit,
			}
		}
		detail.Tiers = append(detail.Tiers, tier)
	}
	for _, sec := range sections {
		section := domain.ServiceSection{Title: sec.Title, Description: sec.Description, Type: sec.Type}
		for _, a := range sec.Addons {
			section.Addons = append(section.Addons, domain.ServiceAddon{
				ID: a.ID, Name: a.Name, Description: a.Description, Price: a.Price, Unit: a.Unit,
				Discount: a.Discount, DiscountType: a.DiscountType, Icon: a.Icon,
			})
		}
		detail.Sections = append(detail.Sections, section)
	}
	d.detail.set(id, detail)
	return detail, nil
}

// Ping is for the readiness probe.
func (d *Discovery) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }
