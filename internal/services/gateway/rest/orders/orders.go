// Package orders is the gateway's REST surface over the transaction service,
// shared by the POS (scoped to the staff member's outlets) and the back-office
// (everything). Scoping is enforced by the transaction service from the token.
package orders

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	transactionv1 "github.com/koobam/wuzz-backend/gen/wuzz/transaction/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

type Handler struct {
	Tx transactionv1.TransactionServiceClient
}

type lineDTO struct {
	ItemID int64   `json:"item_id"`
	Name   string  `json:"name" binding:"required"`
	Unit   string  `json:"unit"`
	Price  int64   `json:"price"`
	Qty    float64 `json:"qty"`
	// The tier and add-ons picked; Price already includes them.
	Variant   string        `json:"variant,omitempty"`
	Modifiers []modifierDTO `json:"modifiers,omitempty" binding:"omitempty,max=20,dive"`
	// The catalog ids of the options chosen: only so the cost of the line can be reproduced, never used for the price.
	VariantOptionIDs  []int64 `json:"variant_option_ids,omitempty" binding:"omitempty,max=20,dive,gt=0"`
	ModifierOptionIDs []int64 `json:"modifier_option_ids,omitempty" binding:"omitempty,max=20,dive,gt=0"`
}

type modifierDTO struct {
	Group string `json:"group,omitempty"`
	Name  string `json:"name" binding:"required"`
	Price int64  `json:"price"`
}

type orderDTO struct {
	ID            string    `json:"id"`
	Code          string    `json:"code"`
	OutletID      int64     `json:"outlet_id"`
	CustomerName  string    `json:"customer_name"`
	CustomerPhone string    `json:"customer_phone"`
	Lines         []lineDTO `json:"lines"`
	Total         int64     `json:"total"`
	Status        string    `json:"status"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     string    `json:"created_at"`
	UpdatedAt     string    `json:"updated_at"`
}

func toDTO(o *transactionv1.Order) orderDTO {
	lines := make([]lineDTO, 0, len(o.GetLines()))
	for _, l := range o.GetLines() {
		line := lineDTO{ItemID: l.GetItemId(), Name: l.GetName(), Unit: l.GetUnit(), Price: l.GetPrice(), Qty: l.GetQty(), Variant: l.GetVariant(),
			VariantOptionIDs: l.GetVariantOptionIds(), ModifierOptionIDs: l.GetModifierOptionIds()}
		for _, m := range l.GetModifiers() {
			line.Modifiers = append(line.Modifiers, modifierDTO{Group: m.GetGroup(), Name: m.GetName(), Price: m.GetPrice()})
		}
		lines = append(lines, line)
	}
	return orderDTO{
		ID: o.GetId(), Code: o.GetCode(), OutletID: o.GetOutletId(), CustomerName: o.GetCustomerName(), CustomerPhone: o.GetCustomerPhone(),
		Lines: lines, Total: o.GetTotal(), Status: o.GetStatus(), CreatedBy: o.GetCreatedBy(),
		CreatedAt: o.GetCreatedAt().AsTime().Format(timeLayout), UpdatedAt: o.GetUpdatedAt().AsTime().Format(timeLayout),
	}
}

type createBody struct {
	ID string `json:"id" binding:"required"`
	// Code is optional: a code the client suggests (the POS no longer does). It is kept if well-formed and unused, else replaced by the server's.
	Code          string    `json:"code"`
	OutletID      int64     `json:"outlet_id" binding:"required"`
	CustomerName  string    `json:"customer_name" binding:"required"`
	CustomerPhone string    `json:"customer_phone"`
	Lines         []lineDTO `json:"lines" binding:"required,min=1,dive"`
}

// Create is idempotent on the body's id: the POS retries until it hears back.
// 201 for a new order, 200 when the id already existed.
// PinnedOutlet is the one outlet a till belongs to: the outlet its signed-in account is assigned to, or 0 when the
// account names none (or several), in which case nothing is pinned.
func PinnedOutlet(id authn.Identity) int64 {
	var found int64
	for _, r := range id.Roles {
		if rest, ok := strings.CutPrefix(r, "outlet:"); ok {
			n, err := strconv.ParseInt(rest, 10, 64)
			if err != nil || n <= 0 {
				continue
			}
			if found != 0 && found != n {
				return 0
			}
			found = n
		}
	}
	return found
}

// pinOutlet makes the POS routes below it about the caller's own outlet: an order created there is that outlet's
// whatever the body says, and the list is that outlet's. The back office does not use it.
func pinOutlet(c *gin.Context) {
	if id, ok := authn.FromContext(c.Request.Context()); ok {
		if outlet := PinnedOutlet(id); outlet != 0 {
			c.Set(pinnedKey, outlet)
		}
	}
	c.Next()
}

// PinOutlet is the middleware that does that.
func PinOutlet() gin.HandlerFunc { return pinOutlet }

const pinnedKey = "pos.outlet"

func pinned(c *gin.Context) int64 { return c.GetInt64(pinnedKey) }

// Pinned is the outlet the signed-in POS account is pinned to by PinOutlet, or 0 when it names none or several.
func Pinned(c *gin.Context) int64 { return pinned(c) }

func pinnedOr(c *gin.Context, fallback int64) int64 {
	if outlet := pinned(c); outlet != 0 {
		return outlet
	}
	return fallback
}

func (h *Handler) Create(c *gin.Context) {
	b, ok := httpx.Bind[createBody](c)
	if !ok {
		return
	}
	if outlet := pinned(c); outlet != 0 {
		b.OutletID = outlet
	}
	lines := make([]*transactionv1.OrderLine, 0, len(b.Lines))
	for _, l := range b.Lines {
		line := &transactionv1.OrderLine{ItemId: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty, Variant: l.Variant,
			VariantOptionIds: l.VariantOptionIDs, ModifierOptionIds: l.ModifierOptionIDs}
		for _, m := range l.Modifiers {
			line.Modifiers = append(line.Modifiers, &transactionv1.LineModifier{Group: m.Group, Name: m.Name, Price: m.Price})
		}
		lines = append(lines, line)
	}
	res, err := h.Tx.CreateOrder(c.Request.Context(), &transactionv1.CreateOrderRequest{
		Id: b.ID, OutletId: b.OutletID, CustomerName: b.CustomerName, CustomerPhone: b.CustomerPhone, Lines: lines, Code: b.Code})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	if res.GetCreated() {
		httpx.Created(c, toDTO(res.GetOrder()))
		return
	}
	httpx.OK(c, toDTO(res.GetOrder()))
}

func (h *Handler) Get(c *gin.Context) {
	res, err := h.Tx.GetOrder(c.Request.Context(), &transactionv1.GetOrderRequest{Id: c.Param("id")})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, toDTO(res.GetOrder()))
}

func (h *Handler) List(c *gin.Context) {
	res, err := h.Tx.ListOrders(c.Request.Context(), &transactionv1.ListOrdersRequest{
		OutletId: pinnedOr(c, int64(httpx.IntQuery(c, "outlet_id", 0))),
		Status:   c.Query("status"),
		Query:    c.Query("q"),
		SortBy:   c.Query("sort_by"),
		SortDir:  c.Query("sort_dir"),
		Limit:    int32(httpx.IntQuery(c, "limit", 25)),
		Offset:   int32(httpx.IntQuery(c, "offset", 0)),
		From:     c.Query("from"),
		To:       c.Query("to"),
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	items := make([]orderDTO, 0, len(res.GetOrders()))
	for _, o := range res.GetOrders() {
		items = append(items, toDTO(o))
	}
	httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
}

type dayStatsDTO struct {
	Date      string `json:"date"`
	Orders    int64  `json:"orders"`
	Fulfilled int64  `json:"fulfilled"`
	Cancelled int64  `json:"cancelled"`
}

// Stats is the dashboard's numbers: orders by status, all-time revenue of fulfilled orders, and a
// row per day for the last `days` days (default 7).
func (h *Handler) Stats(c *gin.Context) {
	res, err := h.Tx.OrderStats(c.Request.Context(), &transactionv1.OrderStatsRequest{
		OutletId: int64(httpx.IntQuery(c, "outlet_id", 0)),
		Days:     int32(httpx.IntQuery(c, "days", 7)),
		From:     c.Query("from"),
		To:       c.Query("to"),
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	byStatus := map[string]int64{}
	for _, s := range res.GetByStatus() {
		byStatus[s.GetStatus()] = s.GetOrders()
	}
	days := make([]dayStatsDTO, 0, len(res.GetDays()))
	for _, d := range res.GetDays() {
		days = append(days, dayStatsDTO{Date: d.GetDate(), Orders: d.GetOrders(), Fulfilled: d.GetFulfilled(), Cancelled: d.GetCancelled()})
	}
	httpx.OK(c, gin.H{"by_status": byStatus, "revenue": res.GetRevenue(), "days": days})
}

type statusBody struct {
	Status string `json:"status" binding:"required"`
}

func (h *Handler) SetStatus(c *gin.Context) {
	b, ok := httpx.Bind[statusBody](c)
	if !ok {
		return
	}
	res, err := h.Tx.UpdateOrderStatus(c.Request.Context(), &transactionv1.UpdateOrderStatusRequest{Id: c.Param("id"), Status: b.Status})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, toDTO(res.GetOrder()))
}

// Summary is the totals for the Orders page's cards. It takes the same filters as List, so the cards and the table always describe
// the same orders. Money is whole rupiah; avg_lines has 2 decimal places and the percentages 1, each rounded once, half away from zero.
func (h *Handler) Summary(c *gin.Context) {
	res, err := h.Tx.OrderSummary(c.Request.Context(), &transactionv1.OrderSummaryRequest{
		OutletId: int64(httpx.IntQuery(c, "outlet_id", 0)),
		Status:   c.Query("status"),
		Query:    c.Query("q"),
		From:     c.Query("from"),
		To:       c.Query("to"),
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{
		"orders": res.GetOrders(), "fulfilled": res.GetFulfilled(), "cancelled": res.GetCancelled(), "open": res.GetOpen(), "counted": res.GetCounted(),
		"value": res.GetValue(), "avg_value": res.GetAvgValue(), "lines": res.GetLines(), "avg_lines": res.GetAvgLines(),
		"cancelled_percent": res.GetCancelledPercent(), "fulfilled_percent": res.GetFulfilledPercent(),
	})
}
