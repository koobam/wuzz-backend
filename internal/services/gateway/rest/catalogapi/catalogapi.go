// Package catalogapi is the gateway's REST surface over the catalog service:
// public reads for the customer app, the POS's catalog bundle, and the admin's
// outlet / service management.
package catalogapi

import (
	"math"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

type Handler struct {
	Catalog catalogv1.CatalogServiceClient
}

type OutletDTO struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Address   string  `json:"address"`
	Phone     string  `json:"phone"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Active    bool    `json:"active"`
}

type ServiceDTO struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Unit   string `json:"unit"`
	Price  int64  `json:"price"`
	Active bool   `json:"active"`
}

func outletDTO(o *catalogv1.Outlet) OutletDTO {
	return OutletDTO{ID: o.GetId(), Name: o.GetName(), Address: o.GetAddress(), Phone: o.GetPhone(),
		Latitude: o.GetLatitude(), Longitude: o.GetLongitude(), Active: o.GetActive()}
}

func serviceDTO(s *catalogv1.ServiceItem) ServiceDTO {
	return ServiceDTO{ID: s.GetId(), Name: s.GetName(), Unit: s.GetUnit(), Price: s.GetPrice(), Active: s.GetActive()}
}

func outletDTOs(in []*catalogv1.Outlet) []OutletDTO {
	out := make([]OutletDTO, 0, len(in))
	for _, o := range in {
		out = append(out, outletDTO(o))
	}
	return out
}

func serviceDTOs(in []*catalogv1.ServiceItem) []ServiceDTO {
	out := make([]ServiceDTO, 0, len(in))
	for _, s := range in {
		out = append(out, serviceDTO(s))
	}
	return out
}

// ---- public (customer app) ----

func (h *Handler) PublicOutlets(c *gin.Context) {
	res, err := h.Catalog.ListOutlets(c.Request.Context(), &catalogv1.ListOutletsRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, outletDTOs(res.GetOutlets()))
}

// ---- POS ----

// POSCatalog bundles what the POS caches for offline use: the services, and the
// outlets this staff member works at (every outlet, for an admin).
func (h *Handler) POSCatalog(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := authn.FromContext(ctx)

	req := &catalogv1.ListOutletsRequest{}
	if !id.HasRole("admin") {
		req.Ids = outletIDs(id.Roles)
	}
	outlets, err := h.Catalog.ListOutlets(ctx, req)
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	services, err := h.Catalog.ListServices(ctx, &catalogv1.ListServicesRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"outlets": outletDTOs(outlets.GetOutlets()), "services": serviceDTOs(services.GetServices())})
}

func outletIDs(roles []string) []int64 {
	var ids []int64
	for _, r := range roles {
		if len(r) > 7 && r[:7] == "outlet:" {
			if n, err := strconv.ParseInt(r[7:], 10, 64); err == nil {
				ids = append(ids, n)
			}
		}
	}
	// A staff member with no parseable outlet must not fall through to "all".
	if len(ids) == 0 {
		ids = []int64{-1}
	}
	return ids
}

// ---- back-office ----

type outletBody struct {
	Name      string  `json:"name" binding:"required"`
	Address   string  `json:"address"`
	Phone     string  `json:"phone"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Active    *bool   `json:"active"`
}

type serviceBody struct {
	Name   string `json:"name" binding:"required"`
	Unit   string `json:"unit" binding:"required"`
	Price  int64  `json:"price"`
	Active *bool  `json:"active"`
}

func (h *Handler) BOListOutlets(c *gin.Context) {
	res, err := h.Catalog.ListOutlets(c.Request.Context(), &catalogv1.ListOutletsRequest{IncludeInactive: true})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, outletDTOs(res.GetOutlets()))
}

func (h *Handler) BOCreateOutlet(c *gin.Context) {
	b, ok := httpx.Bind[outletBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.CreateOutlet(c.Request.Context(), &catalogv1.CreateOutletRequest{
		Name: b.Name, Address: b.Address, Phone: b.Phone, Latitude: b.Latitude, Longitude: b.Longitude})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, outletDTO(res.GetOutlet()))
}

func (h *Handler) BOUpdateOutlet(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Fail(c, apperr.Invalid("The outlet id is invalid"))
		return
	}
	b, ok := httpx.Bind[outletBody](c)
	if !ok {
		return
	}
	// The update RPC replaces every field, so an omitted "active" must keep the
	// stored value rather than silently deactivating the outlet.
	active := true
	if b.Active != nil {
		active = *b.Active
	} else if cur, err := h.Catalog.GetOutlet(c.Request.Context(), &catalogv1.GetOutletRequest{Id: id}); err == nil {
		active = cur.GetOutlet().GetActive()
	}
	res, err := h.Catalog.UpdateOutlet(c.Request.Context(), &catalogv1.UpdateOutletRequest{
		Id: id, Name: b.Name, Address: b.Address, Phone: b.Phone, Latitude: b.Latitude, Longitude: b.Longitude, Active: active})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, outletDTO(res.GetOutlet()))
}

func (h *Handler) BOListServices(c *gin.Context) {
	res, err := h.Catalog.ListServices(c.Request.Context(), &catalogv1.ListServicesRequest{IncludeInactive: true})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, serviceDTOs(res.GetServices()))
}

func (h *Handler) BOCreateService(c *gin.Context) {
	b, ok := httpx.Bind[serviceBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.CreateService(c.Request.Context(), &catalogv1.CreateServiceRequest{Name: b.Name, Unit: b.Unit, Price: b.Price})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, serviceDTO(res.GetService()))
}

func (h *Handler) BOUpdateService(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Fail(c, apperr.Invalid("The service id is invalid"))
		return
	}
	b, ok := httpx.Bind[serviceBody](c)
	if !ok {
		return
	}
	active := true
	if b.Active != nil {
		active = *b.Active
	}
	res, err := h.Catalog.UpdateService(c.Request.Context(), &catalogv1.UpdateServiceRequest{
		Id: id, Name: b.Name, Unit: b.Unit, Price: b.Price, Active: active})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, serviceDTO(res.GetService()))
}

// ---- public discovery (customer app, served from MongoDB) ----

// OutletViewDTO is the shape the customer app reads for an outlet. Field names
// are the shipped client's contract (`long`, `distance_in_km`, `hours`, ...).
type OutletViewDTO struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Address       string   `json:"address"`
	Category      string   `json:"category"`
	Lat           float64  `json:"lat"`
	Long          float64  `json:"long"`
	Images        []string `json:"images"`
	CurrentStatus string   `json:"current_status"`
	DistanceInKm  *float64 `json:"distance_in_km,omitempty"`
	DeliveryTypes []string `json:"delivery_types"`
	// [open, close] as ISO wall-clock times, e.g. "0000-01-01T08:00:00Z".
	Hours []string `json:"hours"`
}

func outletViewDTO(o *catalogv1.OutletView) OutletViewDTO {
	dto := OutletViewDTO{
		ID: o.GetId(), Name: o.GetName(), Address: o.GetAddress(), Category: o.GetCategory(),
		Lat: o.GetLatitude(), Long: o.GetLongitude(),
		Images: nonNil(o.GetImages()), CurrentStatus: o.GetCurrentStatus(), DeliveryTypes: nonNil(o.GetDeliveryTypes()),
		Hours: []string{wallClock(o.GetOpenTime()), wallClock(o.GetCloseTime())},
	}
	if o.GetHasDistance() {
		d := o.GetDistanceKm()
		dto.DistanceInKm = &d
	}
	return dto
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// wallClock turns "08:00" into "0000-01-01T08:00:00Z".
func wallClock(hhmm string) string { return "0000-01-01T" + hhmm + ":00Z" }

// location reads lat/long from the query; both or neither must be present.
func location(c *gin.Context) (lat, lng float64, ok bool, err error) {
	latS, lngS := c.Query("lat"), c.Query("long")
	if latS == "" && lngS == "" {
		return 0, 0, false, nil
	}
	lat, errLat := strconv.ParseFloat(latS, 64)
	lng, errLng := strconv.ParseFloat(lngS, 64)
	if errLat != nil || errLng != nil || math.IsNaN(lat) || math.IsNaN(lng) {
		return 0, 0, false, apperr.Invalid("lat and long must both be valid numbers")
	}
	return lat, lng, true, nil
}

// NearbyOutlets: GET /app/outlets/nearby?lat&long&num&cursor&selected_outlet_id&q
// Nearest first when lat/long are given, by name otherwise.
func (h *Handler) NearbyOutlets(c *gin.Context) {
	lat, lng, hasLoc, err := location(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	limit := 0
	if v := c.Query("num"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			httpx.Fail(c, apperr.Invalid("num must be a positive number"))
			return
		}
		limit = n
	}
	var selected int64
	if v := c.Query("selected_outlet_id"); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			httpx.Fail(c, apperr.Invalid("selected_outlet_id is invalid"))
			return
		}
		selected = n
	}
	text := c.Query("q")
	if text == "" {
		text = c.Query("search")
	}
	res, err := h.Catalog.NearbyOutlets(c.Request.Context(), &catalogv1.NearbyOutletsRequest{
		HasLocation: hasLoc, Latitude: lat, Longitude: lng, Limit: int32(limit),
		Cursor: c.Query("cursor"), SelectedOutletId: selected, Query: strings.TrimSpace(text),
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	outlets := make([]OutletViewDTO, 0, len(res.GetOutlets()))
	for _, o := range res.GetOutlets() {
		outlets = append(outlets, outletViewDTO(o))
	}
	httpx.OK(c, gin.H{"next": res.GetNext(), "outlets": outlets})
}

// CoverageDTO says which service area a position is in.
type CoverageDTO struct {
	Covered bool          `json:"covered"`
	Area    *CoverageArea `json:"area"`
}

type CoverageArea struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Coverage: GET /app/v1/coverage?lat&long
func (h *Handler) Coverage(c *gin.Context) {
	lat, lng, hasLoc, err := location(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if !hasLoc {
		httpx.Fail(c, apperr.Invalid("lat and long are required"))
		return
	}
	res, err := h.Catalog.ResolveCoverage(c.Request.Context(), &catalogv1.ResolveCoverageRequest{Latitude: lat, Longitude: lng})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	out := CoverageDTO{Covered: res.GetCovered()}
	if a := res.GetArea(); a != nil {
		out.Area = &CoverageArea{ID: a.GetId(), Name: a.GetName()}
	}
	httpx.OK(c, out)
}

// ServiceViewDTO is the shape the customer app reads for a service (the shipped
// client's contract).
type ServiceViewDTO struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	Price           int64   `json:"price"`
	Unit            string  `json:"unit"`
	ServiceType     string  `json:"service_type"`
	ServiceCategory string  `json:"service_category"`
	MinKg           *int32  `json:"min_kg,omitempty"`
	MaxKg           *int32  `json:"max_kg,omitempty"`
	Discount        float64 `json:"discount"`
	DiscountType    string  `json:"discount_type"`
	Icon            string  `json:"icon"`
	Image           string  `json:"image,omitempty"`
}

func serviceViewDTO(s *catalogv1.ServiceView) ServiceViewDTO {
	dto := ServiceViewDTO{
		ID: s.GetId(), Name: s.GetName(), Description: s.GetDescription(), Price: s.GetPrice(), Unit: s.GetUnit(),
		ServiceType: s.GetServiceType(), ServiceCategory: s.GetServiceCategory(), Discount: s.GetDiscount(),
		DiscountType: s.GetDiscountType(), Icon: s.GetIcon(), Image: s.GetImage(),
	}
	if s.GetMaxKg() > 0 {
		minKg, maxKg := s.GetMinKg(), s.GetMaxKg()
		dto.MinKg, dto.MaxKg = &minKg, &maxKg
	}
	return dto
}

// BrowseServices: GET /app/v1/services?service_category=regular|alacarte&num
func (h *Handler) BrowseServices(c *gin.Context) {
	limit := 0
	if v := c.Query("num"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			httpx.Fail(c, apperr.Invalid("num must be a positive number"))
			return
		}
		limit = n
	}
	res, err := h.Catalog.BrowseServices(c.Request.Context(), &catalogv1.BrowseServicesRequest{
		ServiceCategory: c.Query("service_category"), Limit: int32(limit)})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	services := make([]ServiceViewDTO, 0, len(res.GetServices()))
	for _, s := range res.GetServices() {
		services = append(services, serviceViewDTO(s))
	}
	httpx.OK(c, gin.H{"services": services})
}

type tierPricingDTO struct {
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

type tierDTO struct {
	ID        int64           `json:"id"`
	Code      string          `json:"code"`
	Name      string          `json:"name"`
	SortOrder int32           `json:"sort_order"`
	Pricing   *tierPricingDTO `json:"pricing,omitempty"`
}

type addonDTO struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Price        int64   `json:"price"`
	Unit         string  `json:"unit,omitempty"`
	Discount     float64 `json:"discount"`
	DiscountType string  `json:"discount_type"`
	Icon         string  `json:"icon,omitempty"`
}

type sectionDTO struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Type        string     `json:"type"`
	Addons      []addonDTO `json:"addons"`
}

// ServiceDetailDTO is a service plus its tiers and add-on sections.
type ServiceDetailDTO struct {
	ServiceViewDTO
	Tiers    []tierDTO    `json:"tiers,omitempty"`
	Sections []sectionDTO `json:"sections,omitempty"`
}

// ServiceDetail: GET /app/v1/services/:id
func (h *Handler) ServiceDetail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Fail(c, apperr.Invalid("The service id is invalid"))
		return
	}
	res, err := h.Catalog.GetServiceDetail(c.Request.Context(), &catalogv1.GetServiceDetailRequest{Id: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	d := res.GetDetail()
	out := ServiceDetailDTO{ServiceViewDTO: serviceViewDTO(d.GetService())}
	for _, t := range d.GetTiers() {
		tier := tierDTO{ID: t.GetId(), Code: t.GetCode(), Name: t.GetName(), SortOrder: t.GetSortOrder()}
		if p := t.GetPricing(); p != nil {
			tier.Pricing = &tierPricingDTO{
				ID: p.GetId(), ServiceID: p.GetServiceId(), TierID: p.GetTierId(), SKU: p.GetSku(),
				EstimatedTime: p.GetEstimatedTime(), EstimatedTimeUnit: p.GetEstimatedTimeUnit(),
				Price: p.GetPrice(), Discount: p.GetDiscount(), DiscountType: p.GetDiscountType(), Unit: p.GetUnit(),
			}
		}
		out.Tiers = append(out.Tiers, tier)
	}
	for _, sec := range d.GetSections() {
		section := sectionDTO{Title: sec.GetTitle(), Description: sec.GetDescription(), Type: sec.GetType(), Addons: []addonDTO{}}
		for _, a := range sec.GetAddons() {
			section.Addons = append(section.Addons, addonDTO{
				ID: a.GetId(), Name: a.GetName(), Description: a.GetDescription(), Price: a.GetPrice(), Unit: a.GetUnit(),
				Discount: a.GetDiscount(), DiscountType: a.GetDiscountType(), Icon: a.GetIcon(),
			})
		}
		out.Sections = append(out.Sections, section)
	}
	httpx.OK(c, out)
}
