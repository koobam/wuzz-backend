package catalogapi

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

// Vouchers: money off an order. The back office writes them and can put one in customers' wallets; a customer reads their own wallet and
// claims a coded voucher by typing its code. Whether a voucher is live, what a customer still has left of it and the rules of a grant are
// worked out by the catalog service; this file only carries them. Customers belong to the user service, so the one place that needs to
// find them (an injection) asks it first.

type voucherServiceDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// VoucherDTO is a voucher for the back office. type is "public" (no code) or "code". starts_at and ends_at are RFC 3339 in UTC, or null
// when the window is open there. outlet_ids and service_ids are empty for a voucher that can be spent anywhere.
type VoucherDTO struct {
	ID            int64               `json:"id"`
	Code          string              `json:"code"`
	Type          string              `json:"type"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	InternalNotes string              `json:"internal_notes"`
	DiscountType  string              `json:"discount_type"`
	Value         int64               `json:"value"`
	MaxDiscount   int64               `json:"max_discount"`
	MinSpend      int64               `json:"min_spend"`
	MaxClaims     int32               `json:"max_claims"`
	ClaimsCount   int64               `json:"claims_count"`
	UsageLimit    int32               `json:"usage_limit"`
	Status        string              `json:"status"`
	Color         string              `json:"color"`
	ImageURL      string              `json:"image_url"`
	StartsAt      *string             `json:"starts_at"`
	EndsAt        *string             `json:"ends_at"`
	OutletIDs     []int64             `json:"outlet_ids"`
	Outlets       []bannerOutletDTO   `json:"outlets"`
	ServiceIDs    []int64             `json:"service_ids"`
	Services      []voucherServiceDTO `json:"services"`
	IsLive        bool                `json:"is_live"`
	CreatedAt     string              `json:"created_at"`
	UpdatedAt     string              `json:"updated_at"`
}

func voucherDTO(v *catalogv1.Voucher) VoucherDTO {
	d := VoucherDTO{ID: v.GetId(), Code: v.GetCode(), Type: v.GetType(), Title: v.GetTitle(), Description: v.GetDescription(), InternalNotes: v.GetInternalNotes(),
		DiscountType: v.GetDiscountType(), Value: v.GetValue(), MaxDiscount: v.GetMaxDiscount(), MinSpend: v.GetMinSpend(), MaxClaims: v.GetMaxClaims(),
		ClaimsCount: v.GetClaimsCount(), UsageLimit: v.GetUsageLimit(), Status: v.GetStatus(), Color: v.GetColor(), ImageURL: v.GetImageUrl(),
		StartsAt: optUTC(v.GetStartsAt()), EndsAt: optUTC(v.GetEndsAt()), OutletIDs: append([]int64{}, v.GetOutletIds()...),
		Outlets: make([]bannerOutletDTO, 0, len(v.GetOutlets())), ServiceIDs: append([]int64{}, v.GetServiceIds()...),
		Services: make([]voucherServiceDTO, 0, len(v.GetServices())), IsLive: v.GetIsLive(), CreatedAt: utc(v.GetCreatedAt()), UpdatedAt: utc(v.GetUpdatedAt())}
	for _, o := range v.GetOutlets() {
		d.Outlets = append(d.Outlets, bannerOutletDTO{ID: o.GetId(), Name: o.GetName()})
	}
	for _, s := range v.GetServices() {
		d.Services = append(d.Services, voucherServiceDTO{ID: s.GetId(), Name: s.GetName()})
	}
	return d
}

func voucherDTOs(in []*catalogv1.Voucher) []VoucherDTO {
	out := make([]VoucherDTO, 0, len(in))
	for _, v := range in {
		out = append(out, voucherDTO(v))
	}
	return out
}

type voucherBody struct {
	Code          string  `json:"code"` // upper-case letters and digits; "" for a public voucher
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	InternalNotes string  `json:"internal_notes"`
	DiscountType  string  `json:"discount_type"` // percentage | amount
	Value         int64   `json:"value"`         // 1..100, or rupiah
	MaxDiscount   int64   `json:"max_discount"`
	MinSpend      int64   `json:"min_spend"`
	MaxClaims     int32   `json:"max_claims"`
	UsageLimit    int32   `json:"usage_limit"`
	Status        string  `json:"status"`
	Color         string  `json:"color"`
	ImageURL      string  `json:"image_url"`
	StartsAt      *string `json:"starts_at"` // RFC 3339; null or "" = open-ended
	EndsAt        *string `json:"ends_at"`
	OutletIDs     []int64 `json:"outlet_ids"`
	ServiceIDs    []int64 `json:"service_ids"`
}

func (b voucherBody) proto() (*catalogv1.Voucher, error) {
	starts, err := rfc3339("starts_at", b.StartsAt)
	if err != nil {
		return nil, err
	}
	ends, err := rfc3339("ends_at", b.EndsAt)
	if err != nil {
		return nil, err
	}
	return &catalogv1.Voucher{Code: b.Code, Title: b.Title, Description: b.Description, InternalNotes: b.InternalNotes, DiscountType: b.DiscountType, Value: b.Value,
		MaxDiscount: b.MaxDiscount, MinSpend: b.MinSpend, MaxClaims: b.MaxClaims, UsageLimit: b.UsageLimit, Status: b.Status, Color: b.Color, ImageUrl: b.ImageURL,
		StartsAt: starts, EndsAt: ends, OutletIds: b.OutletIDs, ServiceIds: b.ServiceIDs}, nil
}

func voucherID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, apperr.Invalid("The voucher id is invalid"))
		return 0, false
	}
	return id, true
}

// BOListVouchers answers GET /backoffice/v1/vouchers: q, status (active|inactive), live (true|false), period (running|upcoming|ended|unlimited),
// discount_type (percentage|amount), outlet_id (vouchers that can be spent there), sort_by (id, title, code, status, value, claims, starts_at,
// ends_at, created_at, updated_at; default created_at, newest first), sort_dir, limit, offset.
func (h *Handler) BOListVouchers(c *gin.Context) {
	outlet := int64(0)
	if v := c.Query("outlet_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			httpx.Fail(c, apperr.Invalid("outlet_id must be a positive number"))
			return
		}
		outlet = n
	}
	res, err := h.Catalog.ListVouchers(c.Request.Context(), &catalogv1.ListVouchersRequest{
		Q: c.Query("q"), Status: c.Query("status"), Live: c.Query("live"), Period: c.Query("period"), DiscountType: c.Query("discount_type"), OutletId: outlet,
		SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"), Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"items": voucherDTOs(res.GetVouchers()), "total": res.GetTotal()})
}

func (h *Handler) BOGetVoucher(c *gin.Context) {
	id, ok := voucherID(c)
	if !ok {
		return
	}
	res, err := h.Catalog.GetVoucher(c.Request.Context(), &catalogv1.GetVoucherRequest{Id: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, voucherDTO(res.GetVoucher()))
}

func (h *Handler) BOCreateVoucher(c *gin.Context) {
	body, ok := bindJSON[voucherBody](c)
	if !ok {
		return
	}
	v, err := body.proto()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	res, err := h.Catalog.CreateVoucher(c.Request.Context(), &catalogv1.SaveVoucherRequest{Voucher: v})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, voucherDTO(res.GetVoucher()))
}

func (h *Handler) BOUpdateVoucher(c *gin.Context) {
	id, ok := voucherID(c)
	if !ok {
		return
	}
	body, ok := bindJSON[voucherBody](c)
	if !ok {
		return
	}
	v, err := body.proto()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	res, err := h.Catalog.UpdateVoucher(c.Request.Context(), &catalogv1.SaveVoucherRequest{Id: id, Voucher: v})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, voucherDTO(res.GetVoucher()))
}

// BOSetVoucherStatus switches a voucher on or off: PATCH {"status": "active" | "inactive"}.
func (h *Handler) BOSetVoucherStatus(c *gin.Context) {
	id, ok := voucherID(c)
	if !ok {
		return
	}
	body, ok := bindJSON[struct {
		Status string `json:"status"`
	}](c)
	if !ok {
		return
	}
	res, err := h.Catalog.SetVoucherStatus(c.Request.Context(), &catalogv1.SetVoucherStatusRequest{Id: id, Status: body.Status})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, voucherDTO(res.GetVoucher()))
}

// BODeleteVoucher refuses (409) while customers hold the voucher.
func (h *Handler) BODeleteVoucher(c *gin.Context) {
	id, ok := voucherID(c)
	if !ok {
		return
	}
	// The title is read first only so the activity log can say which voucher went; an unknown id is a 404 here already.
	got, err := h.Catalog.GetVoucher(c.Request.Context(), &catalogv1.GetVoucherRequest{Id: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	if _, err := h.Catalog.DeleteVoucher(c.Request.Context(), &catalogv1.GetVoucherRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"id": id, "title": got.GetVoucher().GetTitle()})
}

// BOVoucherColors are the colours a coupon may have, "#RRGGBB"; the first is the default.
func (h *Handler) BOVoucherColors(c *gin.Context) {
	res, err := h.Catalog.ListVoucherColors(c.Request.Context(), &catalogv1.ListVoucherColorsRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"colors": res.GetColors()})
}

// VoucherGrantDTO is a customer's copy of a voucher. customer_name and customer_phone are as they were when it was granted.
type VoucherGrantDTO struct {
	ID            int64   `json:"id"`
	VoucherID     int64   `json:"voucher_id"`
	CustomerID    int64   `json:"customer_id"`
	CustomerName  string  `json:"customer_name"`
	CustomerPhone string  `json:"customer_phone"`
	Source        string  `json:"source"` // claim | inject
	GrantedBy     string  `json:"granted_by"`
	GrantedAt     string  `json:"granted_at"`
	ExpiresAt     *string `json:"expires_at"`
	UsesRemaining int32   `json:"uses_remaining"`
}

// BOVoucherGrants answers GET /backoffice/v1/vouchers/:id/grants: who holds the voucher, newest first. q matches part of the customer's name or
// phone number; limit, offset.
func (h *Handler) BOVoucherGrants(c *gin.Context) {
	id, ok := voucherID(c)
	if !ok {
		return
	}
	res, err := h.Catalog.ListVoucherGrants(c.Request.Context(), &catalogv1.ListVoucherGrantsRequest{VoucherId: id, Q: c.Query("q"),
		Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	items := make([]VoucherGrantDTO, 0, len(res.GetGrants()))
	for _, g := range res.GetGrants() {
		items = append(items, VoucherGrantDTO{ID: g.GetId(), VoucherID: g.GetVoucherId(), CustomerID: g.GetCustomerId(), CustomerName: g.GetCustomerName(),
			CustomerPhone: g.GetCustomerPhone(), Source: g.GetSource(), GrantedBy: g.GetGrantedBy(), GrantedAt: utc(g.GetGrantedAt()), ExpiresAt: optUTC(g.GetExpiresAt()),
			UsesRemaining: g.GetUsesRemaining()})
	}
	httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
}

// InjectionDTO is the audit of one bulk send: who sent which voucher to how many customers, and what became of the rest.
type InjectionDTO struct {
	ID           int64  `json:"id"`
	VoucherID    int64  `json:"voucher_id"`
	VoucherTitle string `json:"voucher_title"`
	VoucherCode  string `json:"voucher_code"`
	ActorID      string `json:"actor_id"`
	ActorName    string `json:"actor_name"`
	TargetCount  int32  `json:"target_count"`
	// InjectedCount is how many customers got the voucher. Skipped says why the rest did not: already held it, the claim limit was
	// reached, no such customer, or the customer is switched off or banned. The five counts always add up to target_count.
	InjectedCount int32          `json:"injected_count"`
	Skipped       map[string]int `json:"skipped"`
	CreatedAt     string         `json:"created_at"`
}

func injectionDTO(i *catalogv1.VoucherInjection) InjectionDTO {
	return InjectionDTO{ID: i.GetId(), VoucherID: i.GetVoucherId(), VoucherTitle: i.GetVoucherTitle(), VoucherCode: i.GetVoucherCode(), ActorID: i.GetActorId(),
		ActorName: i.GetActorName(), TargetCount: i.GetTargetCount(), InjectedCount: i.GetInjectedCount(), CreatedAt: utc(i.GetCreatedAt()),
		Skipped: map[string]int{"already_held": int(i.GetSkippedAlreadyHeld()), "over_limit": int(i.GetSkippedOverLimit()),
			"not_found": int(i.GetSkippedNotFound()), "inactive": int(i.GetSkippedInactive())}}
}

type injectBody struct {
	CustomerIDs []flexString `json:"customer_ids"`
	Phones      []string     `json:"phones"`
	ExpiresAt   *string      `json:"expires_at"` // RFC 3339; null = until the voucher ends
}

// BOInjectVoucher answers POST /backoffice/v1/vouchers/:id/inject: {"customer_ids": [12, "15"], "phones": ["0812…"], "expires_at": null}. It
// puts the voucher in the wallet of each customer found, once, within the voucher's claim limit, and answers with the audit. At most 500
// customers in all; a customer named twice counts once.
func (h *Handler) BOInjectVoucher(c *gin.Context) {
	id, ok := voucherID(c)
	if !ok {
		return
	}
	body, ok := bindJSON[injectBody](c)
	if !ok {
		return
	}
	expires, err := rfc3339("expires_at", body.ExpiresAt)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var ids, phones []string
	seen := map[string]bool{}
	for _, raw := range body.CustomerIDs {
		if s := strings.TrimSpace(string(raw)); s != "" && !seen["i"+s] {
			seen["i"+s] = true
			ids = append(ids, s)
		}
	}
	for _, raw := range body.Phones {
		if s := strings.TrimSpace(raw); s != "" && !seen["p"+s] {
			seen["p"+s] = true
			phones = append(phones, s)
		}
	}
	if len(ids)+len(phones) == 0 {
		httpx.Fail(c, apperr.Invalid("Name at least one customer, by id or by phone number"))
		return
	}
	if len(ids)+len(phones) > 500 {
		httpx.Fail(c, apperr.Invalid("One injection can name at most 500 customers"))
		return
	}
	ctx := c.Request.Context()
	// The voucher is checked before the customers are looked up, so a voucher that cannot be sent is refused without touching the user service.
	if _, err := h.Catalog.GetVoucher(ctx, &catalogv1.GetVoucherRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	found, err := h.User.ResolveCustomers(ctx, &userv1.ResolveCustomersRequest{Ids: ids, Phones: phones})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	req := &catalogv1.InjectVoucherRequest{VoucherId: id, NotFound: int32(len(found.GetUnmatched())), ExpiresAt: expires}
	for _, u := range found.GetUsers() {
		n, err := strconv.ParseInt(u.GetId(), 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		req.Customers = append(req.Customers, &catalogv1.InjectCustomer{Id: n, Name: u.GetName(), Phone: u.GetPhone(), Active: u.GetStatus() == "active"})
	}
	res, err := h.Catalog.InjectVoucher(ctx, req)
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	// title and injected_count/target_count are what the activity log's sentence is made of.
	httpx.OK(c, struct {
		InjectionDTO
		Title string `json:"title"`
	}{injectionDTO(res.GetInjection()), res.GetInjection().GetVoucherTitle()})
}

// BOListVoucherInjections answers GET /backoffice/v1/voucher-injections: voucher_id (0 or absent = every voucher), limit, offset; newest first.
func (h *Handler) BOListVoucherInjections(c *gin.Context) {
	voucher := int64(0)
	if v := c.Query("voucher_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			httpx.Fail(c, apperr.Invalid("voucher_id must be a positive number"))
			return
		}
		voucher = n
	}
	res, err := h.Catalog.ListVoucherInjections(c.Request.Context(), &catalogv1.ListVoucherInjectionsRequest{VoucherId: voucher,
		Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	items := make([]InjectionDTO, 0, len(res.GetInjections()))
	for _, i := range res.GetInjections() {
		items = append(items, injectionDTO(i))
	}
	httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
}

// MyVoucherDTO is a voucher as the signed-in customer holds it: no internal note, no status, no counts. source is public (in every wallet),
// claim (typed in) or inject (sent by the shop). expires_at is when it can no longer be used: the earlier of the voucher's end and the customer's
// own expiry, or null.
type MyVoucherDTO struct {
	ID            int64               `json:"id"`
	Code          string              `json:"code"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	DiscountType  string              `json:"discount_type"`
	Value         int64               `json:"value"`
	MaxDiscount   int64               `json:"max_discount"`
	MinSpend      int64               `json:"min_spend"`
	Color         string              `json:"color"`
	ImageURL      string              `json:"image_url"`
	StartsAt      *string             `json:"starts_at"`
	ExpiresAt     *string             `json:"expires_at"`
	OutletIDs     []int64             `json:"outlet_ids"`
	Outlets       []bannerOutletDTO   `json:"outlets"`
	ServiceIDs    []int64             `json:"service_ids"`
	Services      []voucherServiceDTO `json:"services"`
	Source        string              `json:"source"`
	ClaimedAt     *string             `json:"claimed_at"`
	UsesRemaining int32               `json:"uses_remaining"`
	UsageLimit    int32               `json:"usage_limit"`
}

func myVoucherDTO(v *catalogv1.MyVoucher) MyVoucherDTO {
	d := MyVoucherDTO{ID: v.GetId(), Code: v.GetCode(), Title: v.GetTitle(), Description: v.GetDescription(), DiscountType: v.GetDiscountType(), Value: v.GetValue(),
		MaxDiscount: v.GetMaxDiscount(), MinSpend: v.GetMinSpend(), Color: v.GetColor(), ImageURL: v.GetImageUrl(), StartsAt: optUTC(v.GetStartsAt()),
		ExpiresAt: optUTC(v.GetExpiresAt()), OutletIDs: append([]int64{}, v.GetOutletIds()...), Outlets: make([]bannerOutletDTO, 0, len(v.GetOutlets())),
		ServiceIDs: append([]int64{}, v.GetServiceIds()...), Services: make([]voucherServiceDTO, 0, len(v.GetServices())), Source: v.GetSource(),
		ClaimedAt: optUTC(v.GetClaimedAt()), UsesRemaining: v.GetUsesRemaining(), UsageLimit: v.GetUsageLimit()}
	for _, o := range v.GetOutlets() {
		d.Outlets = append(d.Outlets, bannerOutletDTO{ID: o.GetId(), Name: o.GetName()})
	}
	for _, s := range v.GetServices() {
		d.Services = append(d.Services, voucherServiceDTO{ID: s.GetId(), Name: s.GetName()})
	}
	return d
}

// MyVouchers answers GET /app/v1/vouchers (signed in): the customer's wallet, the vouchers they can use now. Grants first, newest first, then the
// public vouchers.
func (h *Handler) MyVouchers(c *gin.Context) {
	res, err := h.Catalog.ListMyVouchers(c.Request.Context(), &catalogv1.ListMyVouchersRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	out := make([]MyVoucherDTO, 0, len(res.GetVouchers()))
	for _, v := range res.GetVouchers() {
		out = append(out, myVoucherDTO(v))
	}
	httpx.OK(c, gin.H{"vouchers": out})
}

// ClaimVoucher answers POST /app/v1/vouchers/claim (signed in): {"code": "WELCOME10"}. The voucher goes in the caller's own wallet. 404 for a
// code that is not any voucher's, 409 when the voucher is not available now, the caller has it already, or everyone it was for has claimed it.
func (h *Handler) ClaimVoucher(c *gin.Context) {
	body, ok := bindJSON[struct {
		Code string `json:"code"`
	}](c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	me, err := h.User.GetUser(ctx, &userv1.GetUserRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	res, err := h.Catalog.ClaimVoucher(ctx, &catalogv1.ClaimVoucherRequest{Code: body.Code, CustomerName: me.GetUser().GetName(), CustomerPhone: me.GetUser().GetPhone()})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, myVoucherDTO(res.GetVoucher()))
}
