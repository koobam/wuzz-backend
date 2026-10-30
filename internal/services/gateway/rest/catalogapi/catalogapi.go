// Package catalogapi is the gateway's REST surface over the catalog service:
// public reads for the customer app, the POS's catalog bundle, and the admin's
// outlet / service management.
package catalogapi

import (
	"google.golang.org/protobuf/proto"
	"math"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	transactionv1 "github.com/koobam/wuzz-backend/gen/wuzz/transaction/v1"
	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

type Handler struct {
	Catalog catalogv1.CatalogServiceClient
	// User and Tx are only read by DELETE /outlets/:id, which must know whether staff, admins or orders still point at the outlet
	// (their ids live in other databases, so no foreign key can say so). Without them that route refuses rather than guess.
	User userv1.UserServiceClient
	Tx   transactionv1.TransactionServiceClient
}

type OutletDTO struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Phone   string `json:"phone"`
	// Email is the outlet's contact address, lower case; "" when it has none.
	Email     string  `json:"email"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Active    bool    `json:"active"`
	// Images are the outlet's pictures (https); the first is its banner.
	Images []string `json:"images"`
	// Category is "laundry", "caffe" or "all". DeliveryTypes lists "pickup" and/or "delivery". OpenTime and CloseTime are the
	// opening hours, wall-clock "HH:MM" (24 hour) in Asia/Jakarta; a CloseTime before the OpenTime means the outlet closes after
	// midnight. IsOpenNow is whether those hours say it is open at the moment of the request.
	Category      string   `json:"category"`
	DeliveryTypes []string `json:"delivery_types"`
	OpenTime      string   `json:"open_time"`
	CloseTime     string   `json:"close_time"`
	IsOpenNow     bool     `json:"is_open_now"`
	// Timezone is the clock the hours are on: "Asia/Jakarta" (WIB), "Asia/Makassar" (WITA) or "Asia/Jayapura" (WIT), and
	// TimezoneLabel is that name. Hours is the week, always seven days from Monday (weekday 0) to Sunday (6): each day is closed
	// or has its own opening and closing time. OpenTime and CloseTime above are today's hours in that zone (empty when the outlet
	// is closed today) and are kept for older clients; IsOpenNow is worked out from the week, so it knows about days that differ and
	// about hours that run past midnight into the next day.
	Timezone      string         `json:"timezone"`
	TimezoneLabel string         `json:"timezone_label"`
	Hours         []OutletDayDTO `json:"hours"`
}

// OutletDayDTO is the opening hours of one weekday (0 = Monday ... 6 = Sunday). Open24h marks a day open round the clock: it has
// no times of its own and is read back with open_time and close_time "00:00" (closed and open_24h are never both true).
type OutletDayDTO struct {
	Weekday   int    `json:"weekday"`
	Closed    bool   `json:"closed"`
	Open24h   bool   `json:"open_24h"`
	OpenTime  string `json:"open_time"`
	CloseTime string `json:"close_time"`
}

var timezoneLabels = map[string]string{"Asia/Jakarta": "WIB", "Asia/Makassar": "WITA", "Asia/Jayapura": "WIT"}

func outletDays(in []*catalogv1.OutletDay) []OutletDayDTO {
	out := make([]OutletDayDTO, 0, len(in))
	for _, d := range in {
		out = append(out, OutletDayDTO{Weekday: int(d.GetWeekday()), Closed: d.GetClosed(), Open24h: d.GetOpen_24H(), OpenTime: d.GetOpenTime(), CloseTime: d.GetCloseTime()})
	}
	return out
}

type ServiceDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Unit string `json:"unit"`
	// Price is what the customer pays (the price after tax), as it has always been for the POS. PriceBeforeTax is what the staff
	// key in and what is stored; PriceAfterTax is the same number as Price. Both are whole rupiah: tax is added once, rounded
	// half away from zero. TaxRateBps is the rate of TaxType in basis points (1100 = 11%; 0 for "none").
	//
	// A discount comes off the list price (PriceBeforeTax) first and tax is added to what is left: DiscountAmount is what it
	// takes off, PriceAfterDiscount = PriceBeforeTax - DiscountAmount is what tax is added to, and PriceAfterTax is that plus the
	// tax. DiscountType is "none", "value" (DiscountValue is whole rupiah) or "percent" (DiscountValue is basis points, 1250 =
	// 12.5%). Every step rounds once, half away from zero.
	Price              int64  `json:"price"`
	PriceBeforeTax     int64  `json:"price_before_tax"`
	DiscountType       string `json:"discount_type"`
	DiscountValue      int64  `json:"discount_value"`
	DiscountAmount     int64  `json:"discount_amount"`
	PriceAfterDiscount int64  `json:"price_after_discount"`
	PriceAfterTax      int64  `json:"price_after_tax"`
	TaxRateBps         int32  `json:"tax_rate_bps"`
	Active             bool   `json:"active"`
	// InternalNotes is a note for staff, never shown to customers.
	InternalNotes string `json:"internal_notes"`
	// CategoryID is the category it is listed under (0 = none); Icon is the name of the icon it is drawn with.
	CategoryID  int64  `json:"category_id"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	// TaxType is how tax is added to the price: "none" or "ppn" (the rate is the checkout's configuration).
	TaxType string `json:"tax_type"`
	// The variant and modifier groups the service offers, in order, with all their options.
	VariantGroups  []VariantGroupDTO  `json:"variant_groups"`
	ModifierGroups []ModifierGroupDTO `json:"modifier_groups"`
}

// CategoryDTO is a group of services, as the POS lists them.
type CategoryDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}

func outletDTO(o *catalogv1.Outlet) OutletDTO {
	return OutletDTO{ID: o.GetId(), Name: o.GetName(), Address: o.GetAddress(), Phone: o.GetPhone(), Email: o.GetEmail(),
		Latitude: o.GetLatitude(), Longitude: o.GetLongitude(), Active: o.GetActive(), Images: orEmpty(o.GetImages()),
		Category: o.GetCategory(), DeliveryTypes: orEmpty(o.GetDeliveryTypes()), OpenTime: o.GetOpenTime(), CloseTime: o.GetCloseTime(), IsOpenNow: o.GetIsOpenNow(),
		Timezone: o.GetTimezone(), TimezoneLabel: timezoneLabels[o.GetTimezone()], Hours: outletDays(o.GetHours())}
}

func serviceDTO(s *catalogv1.ServiceItem) ServiceDTO {
	return ServiceDTO{ID: s.GetId(), Name: s.GetName(), Unit: s.GetUnit(), Price: s.GetPrice(), Active: s.GetActive(),
		PriceBeforeTax: s.GetPriceBeforeTax(), PriceAfterTax: s.GetPriceAfterTax(), TaxRateBps: s.GetTaxRateBps(), InternalNotes: s.GetInternalNotes(),
		DiscountType: s.GetDiscountType(), DiscountValue: s.GetDiscountValue(), DiscountAmount: s.GetDiscountAmount(), PriceAfterDiscount: s.GetPriceAfterDiscount(),
		CategoryID: s.GetCategoryId(), Icon: s.GetIcon(), Description: s.GetDescription(), TaxType: s.GetTaxType(),
		VariantGroups: variantGroupDTOs(s.GetVariantGroups()), ModifierGroups: modifierGroupDTOs(s.GetModifierGroups())}
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

// POSCatalog bundles what the POS needs to ring up an order: the services, and the
// outlet this staff member works at.
func (h *Handler) POSCatalog(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := authn.FromContext(ctx)

	// The POS belongs to outlet staff, so the outlets are always the ones the caller's roles name.
	req := &catalogv1.ListOutletsRequest{Ids: outletIDs(id.Roles)}
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
	categories, err := h.Catalog.ListServiceCategories(ctx, &catalogv1.ListServiceCategoriesRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{
		"outlets":    outletDTOs(outlets.GetOutlets()),
		"services":   withoutNotesAll(serviceDTOs(services.GetServices())),
		"categories": categoryDTOs(categories.GetCategories()),
	})
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
	Name    string `json:"name" binding:"required"`
	Address string `json:"address"`
	Phone   string `json:"phone"`
	// Email left out keeps the stored address on an update and is none on a create; "" removes it.
	Email     *string `json:"email"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Active    *bool   `json:"active"`
	// Images left out keeps the stored pictures; an empty list removes them.
	Images *[]string `json:"images"`
	// Category, DeliveryTypes, OpenTime and CloseTime (see OutletDTO): left out, a create takes the defaults ("all", pickup and
	// delivery, 08:00-22:00) and an update keeps the stored values.
	Category      *string   `json:"category"`
	DeliveryTypes *[]string `json:"delivery_types"`
	OpenTime      *string   `json:"open_time"`
	CloseTime     *string   `json:"close_time"`
	// Timezone and Hours (see OutletDTO). Hours, when sent, is exactly seven days; a closed day carries no times. Left out, a create
	// uses open_time-close_time on every day (08:00-22:00 if those are left out too) and an update keeps the stored week; open_time
	// and/or close_time sent WITHOUT hours set one range for all seven days (what clients did before the hours could differ by day).
	Timezone *string          `json:"timezone"`
	Hours    *[]outletDayBody `json:"hours"`
}

type outletDayBody struct {
	Weekday   int    `json:"weekday"`
	Closed    bool   `json:"closed"`
	Open24h   bool   `json:"open_24h"`
	OpenTime  string `json:"open_time"`
	CloseTime string `json:"close_time"`
}

func daysToProto(in *[]outletDayBody) []*catalogv1.OutletDay {
	if in == nil {
		return nil
	}
	out := make([]*catalogv1.OutletDay, 0, len(*in))
	for _, d := range *in {
		out = append(out, &catalogv1.OutletDay{Weekday: int32(d.Weekday), Closed: d.Closed, Open_24H: d.Open24h, OpenTime: d.OpenTime, CloseTime: d.CloseTime})
	}
	return out
}

// firstOpenRange is the hours of the first day of the week that is open, the range a client that knows no better means by "the hours".
func firstOpenRange(week []*catalogv1.OutletDay) (open, closeAt string) {
	for _, d := range week {
		if d.GetClosed() {
			continue
		}
		if d.GetOpen_24H() {
			return "00:00", "23:59" // a 24 hour day, as the single range older clients understand
		}
		return d.GetOpenTime(), d.GetCloseTime()
	}
	return "08:00", "22:00"
}

type serviceBody struct {
	Name string `json:"name" binding:"required"`
	Unit string `json:"unit" binding:"required"`
	// PriceBeforeTax is the price the staff key in, before tax. `price` is the older name for it and is accepted only for a
	// service without tax (where the two are the same number): a service with tax must say price_before_tax, so that a price
	// read back from the list (the one after tax) is never saved as if it were the one before.
	PriceBeforeTax *int64 `json:"price_before_tax"`
	Price          *int64 `json:"price"`
	Active         *bool  `json:"active"`
	// InternalNotes left out keeps the stored note on an update.
	InternalNotes *string `json:"internal_notes"`
	// CategoryID left out keeps the stored category; 0 removes it.
	CategoryID *int64 `json:"category_id"`
	// TaxType is "none" or "ppn". Left out, a create is "none" and an update keeps the stored one.
	TaxType *string `json:"tax_type"`
	// DiscountType is "none", "value" or "percent" and DiscountValue its size (whole rupiah, or basis points for a percentage).
	// Left out, a create has no discount and an update keeps the stored one; both must make sense against the price.
	DiscountType  *string `json:"discount_type"`
	DiscountValue *int64  `json:"discount_value"`
	// The groups the service offers, by id and in order: left out keeps the stored links, [] removes them all.
	VariantGroupIDs  *[]int64 `json:"variant_group_ids"`
	ModifierGroupIDs *[]int64 `json:"modifier_group_ids"`
}

// boundsFrom reads ?south=&west=&north=&east= (all four, or none: everywhere). Anything else is a mistake worth saying so.
func boundsFrom(c *gin.Context) (*catalogv1.Bounds, error) {
	keys := []string{"south", "west", "north", "east"}
	var v [4]float64
	given := 0
	for i, k := range keys {
		raw := c.Query(k)
		if raw == "" {
			continue
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, apperr.Invalid(k + " must be a number")
		}
		v[i] = f
		given++
	}
	switch given {
	case 0:
		return nil, nil
	case 4:
		return &catalogv1.Bounds{South: v[0], West: v[1], North: v[2], East: v[3]}, nil
	}
	return nil, apperr.Invalid("Give all of south, west, north and east, or none of them")
}

// BOListOutlets lists the outlets. Without `limit` it is the whole list as a bare array (the map and the pickers read it); with
// one it is a searchable page, {items, total}: q, active=true|false, sort_by (id, name, address, phone, active), sort_dir, limit, offset.
func (h *Handler) BOListOutlets(c *gin.Context) {
	if c.Query("limit") != "" {
		res, err := h.Catalog.ListOutlets(c.Request.Context(), &catalogv1.ListOutletsRequest{
			Q: c.Query("q"), Active: c.Query("active"), SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"),
			Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
		if err != nil {
			httpx.Fail(c, httpx.Upstream(err))
			return
		}
		httpx.OK(c, gin.H{"items": outletDTOs(res.GetOutlets()), "total": res.GetTotal()})
		return
	}
	bounds, err := boundsFrom(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	res, err := h.Catalog.ListOutlets(c.Request.Context(), &catalogv1.ListOutletsRequest{IncludeInactive: true, Bounds: bounds})
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
		Name: b.Name, Address: b.Address, Phone: b.Phone, Email: derefString(b.Email), Latitude: b.Latitude, Longitude: b.Longitude, Images: derefList(b.Images),
		Category: derefString(b.Category), DeliveryTypes: derefList(b.DeliveryTypes), OpenTime: derefString(b.OpenTime), CloseTime: derefString(b.CloseTime),
		Timezone: derefString(b.Timezone), Hours: daysToProto(b.Hours)})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, outletDTO(res.GetOutlet()))
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefList(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}

// orEmpty is a list that marshals as [] rather than null.
func orEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// mergeOutletUpdate is the update the RPC is asked for: the body's values, and for whatever the body left out, the outlet as it is.
// The week is the body's `hours`, else (open_time and/or close_time without hours) one range for all seven days, with a side left
// out taken from the first open day, else the stored week.
func mergeOutletUpdate(id int64, cur *catalogv1.Outlet, b outletBody) *catalogv1.UpdateOutletRequest {
	req := &catalogv1.UpdateOutletRequest{
		Id: id, Name: b.Name, Address: b.Address, Phone: b.Phone, Email: cur.GetEmail(), Latitude: b.Latitude, Longitude: b.Longitude,
		Active: cur.GetActive(), Images: cur.GetImages(),
		Category: cur.GetCategory(), DeliveryTypes: cur.GetDeliveryTypes(),
		Timezone: cur.GetTimezone(), Hours: cur.GetHours(),
	}
	if b.Email != nil {
		req.Email = *b.Email
	}
	if b.Active != nil {
		req.Active = *b.Active
	}
	if b.Images != nil {
		req.Images = *b.Images
	}
	if b.Category != nil {
		req.Category = *b.Category
	}
	if b.DeliveryTypes != nil {
		req.DeliveryTypes = *b.DeliveryTypes
	}
	if b.Timezone != nil {
		req.Timezone = *b.Timezone
	}
	switch {
	case b.Hours != nil:
		req.Hours = daysToProto(b.Hours)
	case b.OpenTime != nil || b.CloseTime != nil:
		open, closeAt := firstOpenRange(cur.GetHours())
		if b.OpenTime != nil {
			open = *b.OpenTime
		}
		if b.CloseTime != nil {
			closeAt = *b.CloseTime
		}
		req.Hours, req.OpenTime, req.CloseTime = nil, open, closeAt
	}
	return req
}

// onlyActiveChanged says whether an update leaves the outlet exactly as it is except for its active flag: the on/off switch re-sends
// the stored values, so an outlet seeded without an email or phone can still be switched.
func onlyActiveChanged(cur *catalogv1.Outlet, req *catalogv1.UpdateOutletRequest) bool {
	same := &catalogv1.UpdateOutletRequest{
		Id: cur.GetId(), Name: cur.GetName(), Address: cur.GetAddress(), Phone: cur.GetPhone(), Email: cur.GetEmail(),
		Latitude: cur.GetLatitude(), Longitude: cur.GetLongitude(), Active: req.GetActive(), Images: cur.GetImages(),
		Category: cur.GetCategory(), DeliveryTypes: cur.GetDeliveryTypes(), Timezone: cur.GetTimezone(), Hours: cur.GetHours(),
	}
	return proto.Equal(same, req)
}

// requireOutletContact is the rule of a full edit: the outlet keeps a phone number, an email address and a category.
func requireOutletContact(req *catalogv1.UpdateOutletRequest) error {
	if strings.TrimSpace(req.GetPhone()) == "" {
		return apperr.Invalid("The phone number cannot be empty")
	}
	if strings.TrimSpace(req.GetEmail()) == "" {
		return apperr.Invalid("The email address cannot be empty")
	}
	if strings.TrimSpace(req.GetCategory()) == "" {
		return apperr.Invalid("The category cannot be empty")
	}
	return nil
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
	// The update RPC replaces every field, so what the caller left out (active, the pictures, the category, the hours, the delivery
	// types) is read from the outlet as it is and kept. If that outlet cannot be read the update stops: guessing a value would
	// silently deactivate an outlet or reset its hours.
	cur, err := h.Catalog.GetOutlet(c.Request.Context(), &catalogv1.GetOutletRequest{Id: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	req := mergeOutletUpdate(id, cur.GetOutlet(), b)
	if !onlyActiveChanged(cur.GetOutlet(), req) {
		if err := requireOutletContact(req); err != nil {
			httpx.Fail(c, err)
			return
		}
	}
	res, err := h.Catalog.UpdateOutlet(c.Request.Context(), req)
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, outletDTO(res.GetOutlet()))
}

// BOListServices lists the services. Without `limit` it is the whole list as a bare array; with one it is a searchable page,
// {items, total}: q, category_id, unit, active=true|false, tax_type=none|ppn, discount_type=none|value|percent, sort_by (id, name, unit, price, category_id, active), sort_dir, limit, offset.
func (h *Handler) BOListServices(c *gin.Context) {
	if c.Query("limit") != "" {
		res, err := h.Catalog.ListServices(c.Request.Context(), &catalogv1.ListServicesRequest{
			Q: c.Query("q"), CategoryId: int64(httpx.IntQuery(c, "category_id", 0)), Unit: c.Query("unit"), Active: c.Query("active"), TaxType: c.Query("tax_type"), DiscountType: c.Query("discount_type"),
			SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"),
			Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
		if err != nil {
			httpx.Fail(c, httpx.Upstream(err))
			return
		}
		httpx.OK(c, gin.H{"items": serviceDTOs(res.GetServices()), "total": res.GetTotal()})
		return
	}
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
	price, ok := priceBeforeTax(c, b, derefStr(b.TaxType), 0)
	if !ok {
		return
	}
	res, err := h.Catalog.CreateService(c.Request.Context(), &catalogv1.CreateServiceRequest{
		Name: b.Name, Unit: b.Unit, Price: price, CategoryId: derefInt(b.CategoryID), TaxType: derefStr(b.TaxType), InternalNotes: derefStr(b.InternalNotes),
		DiscountType: derefStr(b.DiscountType), DiscountValue: derefInt(b.DiscountValue),
		VariantGroupIds: idListProto(b.VariantGroupIDs), ModifierGroupIds: idListProto(b.ModifierGroupIDs)})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, serviceDTO(res.GetService()))
}

// priceBeforeTax is the price a body asks for. `stored` is what an update falls back to when the body gives none.
func priceBeforeTax(c *gin.Context, b serviceBody, taxType string, stored int64) (int64, bool) {
	switch {
	case b.PriceBeforeTax != nil:
		return *b.PriceBeforeTax, true
	case b.Price != nil && (taxType == "" || taxType == "none"):
		return *b.Price, true
	case b.Price != nil:
		httpx.Fail(c, apperr.Invalid("Send price_before_tax: the price is keyed in before tax"))
		return 0, false
	}
	return stored, true
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(n *int64) int64 {
	if n == nil {
		return 0
	}
	return *n
}

func categoryDTOs(in []*catalogv1.ServiceCategory) []CategoryDTO {
	out := make([]CategoryDTO, 0, len(in))
	for _, c := range in {
		out = append(out, CategoryDTO{ID: c.GetId(), Name: c.GetName(), Icon: c.GetIcon()})
	}
	return out
}

// BOListCategories is the list of service categories (the services form picks one).
func (h *Handler) BOListCategories(c *gin.Context) {
	res, err := h.Catalog.ListServiceCategories(c.Request.Context(), &catalogv1.ListServiceCategoriesRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, categoryDTOs(res.GetCategories()))
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
	// The update RPC replaces every field, so a category that is left out must keep the stored one.
	category := int64(0)
	taxType := derefStr(b.TaxType)
	notes := derefStr(b.InternalNotes)
	discountType, discountValue := derefStr(b.DiscountType), derefInt(b.DiscountValue)
	storedPrice := int64(0)
	if b.CategoryID != nil && b.TaxType != nil && b.InternalNotes != nil && b.DiscountType != nil && b.DiscountValue != nil && (b.PriceBeforeTax != nil || b.Price != nil) {
		category = *b.CategoryID
	} else if list, err := h.Catalog.ListServices(c.Request.Context(), &catalogv1.ListServicesRequest{IncludeInactive: true}); err == nil {
		for _, s := range list.GetServices() {
			if s.GetId() == id {
				category = s.GetCategoryId()
				storedPrice = s.GetPriceBeforeTax()
				if b.TaxType == nil {
					taxType = s.GetTaxType()
				}
				if b.InternalNotes == nil {
					notes = s.GetInternalNotes()
				}
				// A discount left out is kept as stored; a type given without its value (or the reverse) keeps the stored half.
				if b.DiscountType == nil {
					discountType = s.GetDiscountType()
				}
				if b.DiscountValue == nil {
					discountValue = s.GetDiscountValue()
					if b.DiscountType != nil && *b.DiscountType == "none" {
						discountValue = 0 // no discount has no size: turning it off does not need the old value taken back
					}
				}
			}
		}
		if b.CategoryID != nil {
			category = *b.CategoryID
		}
	}
	if taxType == "" || discountType == "" {
		httpx.Fail(c, apperr.Invalid("tax_type and discount_type are required: the stored ones could not be read"))
		return
	}
	price, ok := priceBeforeTax(c, b, taxType, storedPrice)
	if !ok {
		return
	}
	active := true
	if b.Active != nil {
		active = *b.Active
	}
	res, err := h.Catalog.UpdateService(c.Request.Context(), &catalogv1.UpdateServiceRequest{
		Id: id, Name: b.Name, Unit: b.Unit, Price: price, Active: active, CategoryId: category, TaxType: taxType, InternalNotes: notes,
		DiscountType: discountType, DiscountValue: discountValue,
		VariantGroupIds: idListProto(b.VariantGroupIDs), ModifierGroupIds: idListProto(b.ModifierGroupIDs)})
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
		Hours: []string{},
	}
	// Today's hours on the outlet's own clock; a closed day sends no hours (the app's field is optional).
	if o.GetOpenTime() != "" && o.GetCloseTime() != "" {
		dto.Hours = []string{wallClock(o.GetOpenTime()), wallClock(o.GetCloseTime())}
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
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Price          int64  `json:"price"`
	PriceBeforeTax int64  `json:"price_before_tax"`
	// The discount comes off price_before_tax first; price_after_discount is what tax is added to. `discount` and
	// `discount_type` are how the app has always read it (12.5 with "percentage", or whole rupiah with "fixed").
	DiscountAmount     int64   `json:"discount_amount"`
	PriceAfterDiscount int64   `json:"price_after_discount"`
	PriceAfterTax      int64   `json:"price_after_tax"`
	TaxType            string  `json:"tax_type"`
	TaxRateBps         int32   `json:"tax_rate_bps"`
	Unit               string  `json:"unit"`
	ServiceType        string  `json:"service_type"`
	ServiceCategory    string  `json:"service_category"`
	MinKg              *int32  `json:"min_kg,omitempty"`
	MaxKg              *int32  `json:"max_kg,omitempty"`
	Discount           float64 `json:"discount"`
	DiscountType       string  `json:"discount_type"`
	Icon               string  `json:"icon"`
	Image              string  `json:"image,omitempty"`
}

func serviceViewDTO(s *catalogv1.ServiceView) ServiceViewDTO {
	dto := ServiceViewDTO{
		ID: s.GetId(), Name: s.GetName(), Description: s.GetDescription(), Price: s.GetPrice(), Unit: s.GetUnit(),
		PriceBeforeTax: s.GetPriceBeforeTax(), PriceAfterTax: s.GetPriceAfterTax(), TaxType: s.GetTaxType(), TaxRateBps: s.GetTaxRateBps(),
		DiscountAmount: s.GetDiscountAmount(), PriceAfterDiscount: s.GetPriceAfterDiscount(),
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
	out.Tiers = tierDTOs(d.GetTiers())
	out.Sections = sectionDTOs(d.GetSections())
	httpx.OK(c, out)
}

func tierDTOs(in []*catalogv1.ServiceTier) []tierDTO {
	out := make([]tierDTO, 0, len(in))
	for _, t := range in {
		tier := tierDTO{ID: t.GetId(), Code: t.GetCode(), Name: t.GetName(), SortOrder: t.GetSortOrder()}
		if p := t.GetPricing(); p != nil {
			tier.Pricing = &tierPricingDTO{
				ID: p.GetId(), ServiceID: p.GetServiceId(), TierID: p.GetTierId(), SKU: p.GetSku(),
				EstimatedTime: p.GetEstimatedTime(), EstimatedTimeUnit: p.GetEstimatedTimeUnit(),
				Price: p.GetPrice(), Discount: p.GetDiscount(), DiscountType: p.GetDiscountType(), Unit: p.GetUnit(),
			}
		}
		out = append(out, tier)
	}
	return out
}

func sectionDTOs(in []*catalogv1.ServiceSection) []sectionDTO {
	out := make([]sectionDTO, 0, len(in))
	for _, sec := range in {
		section := sectionDTO{Title: sec.GetTitle(), Description: sec.GetDescription(), Type: sec.GetType(), Addons: []addonDTO{}}
		for _, a := range sec.GetAddons() {
			section.Addons = append(section.Addons, addonDTO{
				ID: a.GetId(), Name: a.GetName(), Description: a.GetDescription(), Price: a.GetPrice(), Unit: a.GetUnit(),
				Discount: a.GetDiscount(), DiscountType: a.GetDiscountType(), Icon: a.GetIcon(),
			})
		}
		out = append(out, section)
	}
	return out
}

func withoutNotesAll(in []ServiceDTO) []ServiceDTO {
	for i := range in {
		in[i] = withoutNotes(in[i])
	}
	return in
}

// TaxRateDTO is the rate of one tax type, in basis points (1100 = 11%).
type TaxRateDTO struct {
	Type    string `json:"type"`
	RateBps int32  `json:"rate_bps"`
}

// TaxRates: GET /backoffice/v1/tax and /pos/v1/tax -> {types:[{type, rate_bps}]}. A form uses it to preview the price after tax
// while it is typed; the rounding it must show is the server's (price + tax, tax rounded once half away from zero).
func (h *Handler) TaxRates(c *gin.Context) {
	res, err := h.Catalog.ListTaxRates(c.Request.Context(), &catalogv1.ListTaxRatesRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	types := make([]TaxRateDTO, 0, len(res.GetRates()))
	for _, r := range res.GetRates() {
		types = append(types, TaxRateDTO{Type: r.GetTaxType(), RateBps: r.GetRateBps()})
	}
	httpx.OK(c, gin.H{"types": types})
}

// BODeleteService deletes a service with its group links and its own recipe (the catalog does both in one transaction). Orders keep
// their own copy of what they sold, so nothing already sold changes. 404 for an unknown id.
func (h *Handler) BODeleteService(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, apperr.Invalid("The service id is invalid"))
		return
	}
	if _, err := h.Catalog.DeleteService(c.Request.Context(), &catalogv1.DeleteServiceRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"id": id})
}

// BODeleteOutlet deletes an outlet that nothing points at. Staff, admins and orders (other services) are counted first and any of them
// refuses the delete with 409 and a message naming what is left; the catalog itself refuses while the outlet has stock records. An
// outlet that is refused is switched off instead (PATCH active=false). The check and the delete are two calls, so a staff member added in
// between is not caught; the outlet's opening hours go with it, and its pictures stay in the media storage.
func (h *Handler) BODeleteOutlet(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, apperr.Invalid("The outlet id is invalid"))
		return
	}
	ctx := c.Request.Context()
	if _, err := h.Catalog.GetOutlet(ctx, &catalogv1.GetOutletRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	if h.User == nil || h.Tx == nil {
		httpx.Fail(c, apperr.Internal("Outlet delete checks are not available"))
		return
	}
	var left []string
	for _, kind := range []string{"staff", "admin"} {
		res, err := h.User.ListAccounts(ctx, &userv1.ListAccountsRequest{Kind: kind, OutletId: id, Limit: 1})
		if err != nil {
			httpx.Fail(c, httpx.Upstream(err))
			return
		}
		if n := res.GetTotal(); n > 0 {
			left = append(left, strconv.FormatInt(n, 10)+" "+kind+" account"+plural(n))
		}
	}
	orders, err := h.Tx.ListOrders(ctx, &transactionv1.ListOrdersRequest{OutletId: id, Limit: 1})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	if n := orders.GetTotal(); n > 0 {
		left = append(left, strconv.FormatInt(n, 10)+" order"+plural(n))
	}
	if len(left) > 0 {
		httpx.Fail(c, apperr.Conflict("This outlet still has "+strings.Join(left, ", ")+" and cannot be deleted; switch it off instead"))
		return
	}
	if _, err := h.Catalog.DeleteOutlet(ctx, &catalogv1.DeleteOutletRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"id": id})
}

func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}
