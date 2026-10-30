package postgres

import (
	"context"
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

// outletViewCols reads an outlet as the customer app browses it. The hours are today's row of outlet_hours, on the outlet's own clock
// (weekday 0 is Monday; isodow is 1 for Monday). A day the outlet is closed gives empty times, which the app shows as no hours; a day
// it is open 24 hours gives 00:00 to 23:59, the closest single range the app can show.
const outletViewCols = `id, name, address, category, latitude, longitude, images,
	current_status, delivery_types,
	COALESCE((SELECT CASE WHEN h.open_24h THEN '00:00' ELSE h.open_time END FROM outlet_hours h WHERE h.outlet_id = outlets.id AND NOT h.closed
	          AND h.weekday = extract(isodow FROM (now() AT TIME ZONE outlets.timezone))::int - 1), ''),
	COALESCE((SELECT CASE WHEN h.open_24h THEN '23:59' ELSE h.close_time END FROM outlet_hours h WHERE h.outlet_id = outlets.id AND NOT h.closed
	          AND h.weekday = extract(isodow FROM (now() AT TIME ZONE outlets.timezone))::int - 1), '')`

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
	min_kg, max_kg, discount_kind, discount_value, icon, image, tax_type`

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
		var kind string
		var value int64
		if err := rows.Scan(&v.ID, &v.Name, &v.Description, &v.Price, &v.Unit, &v.ServiceType, &cat,
			&v.MinKg, &v.MaxKg, &kind, &value, &v.Icon, &v.Image, &v.TaxType); err != nil {
			return nil, apperr.Internal("Could not read services").WithCause(err)
		}
		if cat != nil {
			v.ServiceCategory = *cat
		}
		v.DiscountAmount = domain.DiscountAmountOf(v.Price, kind, value)
		v.Discount, v.DiscountType = appDiscount(kind, value)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("Could not read services").WithCause(err)
	}
	rows.Close()
	rates, err := loadTaxRates(ctx, d.pool)
	if err != nil {
		return nil, err
	}
	for i := range out {
		priceView(&out[i], rates)
	}
	d.menu.set(key, out)
	return append([]domain.ServiceView(nil), out...), nil
}

// ServiceDetail is the service with its variants and add-ons as the customer app has always read them. The service's first
// variant group is its "tiers" (each option priced as the service's price plus the option's extra), and its modifier groups
// are its "sections".
func (d *Discovery) ServiceDetail(ctx context.Context, id int64) (domain.ServiceDetail, error) {
	if cached, ok := d.detail.get(id); ok {
		return cached, nil
	}
	var cat *string
	var v domain.ServiceView
	var kind string
	var value int64
	err := d.pool.QueryRow(ctx, `
		SELECT `+serviceViewCols+` FROM services
		WHERE id = $1 AND active AND service_category IS NOT NULL`, id).
		Scan(&v.ID, &v.Name, &v.Description, &v.Price, &v.Unit, &v.ServiceType, &cat,
			&v.MinKg, &v.MaxKg, &kind, &value, &v.Icon, &v.Image, &v.TaxType)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ServiceDetail{}, apperr.NotFound("Service not found")
	}
	if err != nil {
		return domain.ServiceDetail{}, apperr.Internal("Could not load the service").WithCause(err)
	}
	if cat != nil {
		v.ServiceCategory = *cat
	}
	rates, err := loadTaxRates(ctx, d.pool)
	if err != nil {
		return domain.ServiceDetail{}, err
	}
	v.DiscountAmount = domain.DiscountAmountOf(v.Price, kind, value)
	v.Discount, v.DiscountType = appDiscount(kind, value)
	priceView(&v, rates)
	// A tier is the service's price after its discount plus the option's extra; the extra itself is not discounted.
	base := v.PriceAfterDiscount

	items := []domain.ServiceItem{{ID: id}}
	if err := attachGroups(ctx, d.pool, items); err != nil {
		return domain.ServiceDetail{}, err
	}
	detail := domain.ServiceDetail{ServiceView: v}
	for _, g := range items[0].VariantGroups {
		if tiers := tiersOf(v, base, g); len(tiers) > 0 {
			detail.Tiers = tiers
			break
		}
	}
	for _, g := range items[0].ModifierGroups {
		if section, ok := sectionOf(g, v.TaxRateBps); ok {
			detail.Sections = append(detail.Sections, section)
		}
	}
	d.detail.set(id, detail)
	return detail, nil
}

// tiersOf turns a variant group's active options into the app's tiers.
// Each tier is priced as the service's price after its discount (and before tax) plus the option's extra, and tax is added to that sum once.
func tiersOf(v domain.ServiceView, base int64, g domain.VariantGroup) []domain.ServiceTier {
	var out []domain.ServiceTier
	for _, o := range g.Options {
		if !o.Active {
			continue
		}
		code := slug(o.Name)
		out = append(out, domain.ServiceTier{
			ID: o.ID, Code: code, Name: o.Name, SortOrder: int32(len(out) + 1),
			Pricing: &domain.ServiceTierPricing{
				ServiceID: v.ID, TierID: o.ID, SKU: "WZ-" + strconv.FormatInt(v.ID, 10) + "-" + strings.ToUpper(code),
				EstimatedTime: o.EstimatedTime, EstimatedTimeUnit: o.EstimatedTimeUnit,
				Price: domain.AddTax(base+o.PriceDelta, v.TaxRateBps), Unit: v.Unit,
			},
		})
	}
	return out
}

// sectionOf turns a modifier group's active options into the app's section; a group with none is left out.
// An add-on is taxed like the service it is added to.
func sectionOf(g domain.ModifierGroup, bps int32) (domain.ServiceSection, bool) {
	section := domain.ServiceSection{Title: g.Name, Type: "multiple"}
	if g.Max != nil && *g.Max == 1 {
		section.Type = "single"
	}
	for _, o := range g.Options {
		if o.Active {
			section.Addons = append(section.Addons, domain.ServiceAddon{ID: o.ID, Name: o.Name, Description: o.Description, Price: domain.AddTax(o.Price, bps)})
		}
	}
	return section, len(section.Addons) > 0
}

// slug is a tier's code: lower-case letters and digits, anything else a dash.
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// Ping is for the readiness probe.
func (d *Discovery) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// priceView fills the prices of a service the customer app browses, from its list price (Price on the way in) and the
// DiscountAmount already set: the discount comes off first, tax is added to what is left, and Price becomes what the customer pays.
func priceView(v *domain.ServiceView, rates domain.TaxRates) {
	v.PriceBeforeTax = v.Price
	v.PriceAfterDiscount = v.PriceBeforeTax - v.DiscountAmount
	v.TaxRateBps = rates.Rate(v.TaxType)
	v.PriceAfterTax = domain.AddTax(v.PriceAfterDiscount, v.TaxRateBps)
	v.Price = v.PriceAfterTax // what the customer pays
}

// appDiscount is a discount as the customer app has always read it: a percentage as a number of percent (12.5) with the type
// "percentage", whole rupiah with "fixed", and nothing with "".
func appDiscount(kind string, value int64) (float64, string) {
	switch kind {
	case domain.DiscountPercent:
		return float64(value) / 100, "percentage"
	case domain.DiscountValue:
		return float64(value), "fixed"
	}
	return 0, ""
}
