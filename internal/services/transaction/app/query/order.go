// Package query is transaction's read side: one handler per use case, each with the few repository methods it needs.
package query

import (
	"context"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// Orders is what the order reads need of the store.
type Orders interface {
	GetOrder(ctx context.Context, id string) (domain.Order, error)
	ListOrders(ctx context.Context, f domain.ListFilter) ([]domain.Order, int64, error)
	// SummarizeOrders counts and adds up the orders the filter matches (paging and order are ignored).
	SummarizeOrders(ctx context.Context, f domain.ListFilter) (domain.SummaryCounts, error)
	// Stats aggregates the orders created within the range; an empty outletIDs means every outlet.
	Stats(ctx context.Context, outletIDs []int64, r domain.StatsRange) (domain.Stats, error)
}

// ---------- GetOrder ----------

type GetOrder struct {
	Caller domain.Caller
	ID     string
}

type GetOrderHandler struct{ orders Orders }

func NewGetOrderHandler(orders Orders) *GetOrderHandler { return &GetOrderHandler{orders: orders} }

func (h *GetOrderHandler) Handle(ctx context.Context, q GetOrder) (domain.Order, error) {
	o, err := h.orders.GetOrder(ctx, q.ID)
	if err != nil {
		return domain.Order{}, err
	}
	// 404, not 403: do not confirm that another outlet's order exists.
	if err := q.Caller.CanRead(o); err != nil {
		return domain.Order{}, err
	}
	return o, nil
}

// ---------- ListOrders ----------

type ListOrders struct {
	Caller          domain.Caller
	OutletID        int64
	Status, Query   string
	SortBy, SortDir string
	From, To        string
	Limit, Offset   int
}

type ListOrdersResult struct {
	Orders []domain.Order
	// Total is how many orders the filter matches, not how many are in this page.
	Total int64
}

type ListOrdersHandler struct{ orders Orders }

func NewListOrdersHandler(orders Orders) *ListOrdersHandler {
	return &ListOrdersHandler{orders: orders}
}

func (h *ListOrdersHandler) Handle(ctx context.Context, q ListOrders) (ListOrdersResult, error) {
	f, err := scope(q.Caller, q.OutletID, q.Status, q.Query, q.From, q.To)
	if err != nil {
		return ListOrdersResult{}, err
	}
	f.Limit, f.Offset = q.Limit, q.Offset
	sortBy := q.SortBy
	if sortBy == "" {
		sortBy = "created_at"
	}
	if !domain.SortFields[sortBy] {
		return ListOrdersResult{}, apperr.Invalid("Unknown sort column")
	}
	if q.SortDir != "" && q.SortDir != "asc" && q.SortDir != "desc" {
		return ListOrdersResult{}, apperr.Invalid("Sort direction must be asc or desc")
	}
	f.SortBy, f.Desc = sortBy, q.SortDir != "asc"
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 25
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	orders, total, err := h.orders.ListOrders(ctx, f)
	return ListOrdersResult{Orders: orders, Total: total}, err
}

// ---------- SummarizeOrders ----------

type SummarizeOrders struct {
	Caller        domain.Caller
	OutletID      int64
	Status, Query string
	From, To      string
}

type SummarizeOrdersHandler struct{ orders Orders }

func NewSummarizeOrdersHandler(orders Orders) *SummarizeOrdersHandler {
	return &SummarizeOrdersHandler{orders: orders}
}

// Handle totals the orders a caller may see under the same filters as ListOrders.
func (h *SummarizeOrdersHandler) Handle(ctx context.Context, q SummarizeOrders) (domain.Summary, error) {
	f, err := scope(q.Caller, q.OutletID, q.Status, q.Query, q.From, q.To)
	if err != nil {
		return domain.Summary{}, err
	}
	counts, err := h.orders.SummarizeOrders(ctx, f)
	if err != nil {
		return domain.Summary{}, err
	}
	return domain.NewSummary(counts), nil
}

// ---------- OrderStats ----------

type OrderStats struct {
	Caller   domain.Caller
	OutletID int64
	// Days is the last N days, today included; out of range means a week. From and To (both, or neither) override it.
	Days     int
	From, To string
}

type OrderStatsHandler struct{ orders Orders }

func NewOrderStatsHandler(orders Orders) *OrderStatsHandler {
	return &OrderStatsHandler{orders: orders}
}

// Handle aggregates the orders the caller may see, created within the range: the last `days` days, or from..to.
func (h *OrderStatsHandler) Handle(ctx context.Context, q OrderStats) (domain.Stats, error) {
	c := q.Caller
	if !c.IsStaff() {
		return domain.Stats{}, apperr.Forbidden("Staff access is required")
	}
	var outlets []int64
	switch {
	case q.OutletID != 0 && !c.MayUse(q.OutletID):
		return domain.Stats{}, domain.ErrOutletForbidden
	case q.OutletID != 0:
		outlets = []int64{q.OutletID}
	case !c.Admin:
		outlets = c.Outlets
	}
	r := domain.StatsRange{Days: q.Days}
	if r.Days < 1 || r.Days > domain.MaxStatsDays {
		r.Days = 7
	}
	if q.From != "" || q.To != "" {
		f, errF := time.Parse(dateLayout, q.From)
		t, errT := time.Parse(dateLayout, q.To)
		switch {
		case q.From == "" || q.To == "":
			return domain.Stats{}, apperr.Invalid("Give both from and to, or neither")
		case errF != nil || errT != nil:
			return domain.Stats{}, apperr.Invalid("from and to must be dates like 2026-10-25")
		case t.Before(f):
			return domain.Stats{}, apperr.Invalid("from must not be after to")
		case t.Sub(f) > (domain.MaxStatsDays-1)*24*time.Hour:
			return domain.Stats{}, apperr.Invalid("The range can be at most 92 days")
		}
		r.From, r.To = q.From, q.To
	}
	return h.orders.Stats(ctx, outlets, r)
}

// ---------- shared by ListOrders and SummarizeOrders ----------

// maxFilterDays is the widest from..to a list or a summary accepts.
const maxFilterDays = 366

const dateLayout = "2006-01-02"

// scope checks the filters a list and a summary share and works out which outlets the caller may see. Both use it, so the two
// always mean the same orders.
func scope(c domain.Caller, outletID int64, status, query, from, to string) (domain.ListFilter, error) {
	if !c.IsStaff() {
		return domain.ListFilter{}, apperr.Forbidden("Staff access is required")
	}
	if status != "" && !domain.ValidStatus(status) {
		return domain.ListFilter{}, apperr.Invalid("Unknown status")
	}
	f := domain.ListFilter{Status: status, Query: strings.TrimSpace(query), From: from, To: to}
	if len([]rune(f.Query)) > 64 {
		return domain.ListFilter{}, apperr.Invalid("Search is too long")
	}
	var df, dt time.Time
	var errF, errT error
	if from != "" {
		df, errF = time.Parse(dateLayout, from)
	}
	if to != "" {
		dt, errT = time.Parse(dateLayout, to)
	}
	switch {
	case errF != nil || errT != nil:
		return domain.ListFilter{}, apperr.Invalid("from and to must be dates like 2026-10-25")
	case from != "" && to != "" && dt.Before(df):
		return domain.ListFilter{}, apperr.Invalid("from must not be after to")
	case from != "" && to != "" && dt.Sub(df) > (maxFilterDays-1)*24*time.Hour:
		return domain.ListFilter{}, apperr.Invalid("The range can be at most 366 days")
	}
	switch {
	case outletID != 0 && !c.MayUse(outletID):
		return domain.ListFilter{}, domain.ErrOutletForbidden
	case outletID != 0:
		f.OutletIDs = []int64{outletID}
	case !c.Admin:
		f.OutletIDs = c.Outlets
	}
	return f, nil
}
