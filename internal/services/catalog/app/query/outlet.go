// Package query is catalog's read side: one handler per use case, each with the few repository methods it needs.
package query

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Outlets is what the outlet reads need of the store.
type Outlets interface {
	// A non-nil within keeps only outlets inside that box.
	ListOutlets(ctx context.Context, ids []int64, includeInactive bool, within *domain.Bounds) ([]domain.Outlet, error)
	GetOutlet(ctx context.Context, id int64) (domain.Outlet, error)
	// PageOutlets is the back office's table: one page of the outlets the query matches, and how many match in all.
	PageOutlets(ctx context.Context, q domain.OutletQuery) ([]domain.Outlet, int64, error)
}

// OutletDiscovery is the read side the customer app browses outlets through: geo and text search, and the service areas.
type OutletDiscovery interface {
	// Nearby returns up to q.Limit outlets and the cursor of the next page ("" if none).
	Nearby(ctx context.Context, q domain.NearbyQuery) ([]domain.OutletView, string, error)
	// Coverage returns the service area containing p; ok is false outside every area.
	Coverage(ctx context.Context, p domain.Point) (area domain.CoverageArea, ok bool, err error)
	// ByID returns one outlet; the distance is filled in when near is set.
	ByID(ctx context.Context, id int64, near *domain.Point) (domain.OutletView, error)
}

// ---------- ListOutlets ----------

type ListOutlets struct {
	// IDs narrows the list to these outlets; empty means every outlet.
	IDs             []int64
	IncludeInactive bool
	// Within keeps only outlets inside this box; nil means everywhere.
	Within *domain.Bounds
}

type ListOutletsHandler struct{ outlets Outlets }

func NewListOutletsHandler(outlets Outlets) *ListOutletsHandler {
	return &ListOutletsHandler{outlets: outlets}
}

func (h *ListOutletsHandler) Handle(ctx context.Context, q ListOutlets) ([]domain.Outlet, error) {
	return h.outlets.ListOutlets(ctx, q.IDs, q.IncludeInactive, q.Within)
}

// ---------- PageOutlets ----------

type PageOutlets struct{ Query domain.OutletQuery }

type PageOutletsResult struct {
	Outlets []domain.Outlet
	Total   int64
}

type PageOutletsHandler struct{ outlets Outlets }

func NewPageOutletsHandler(outlets Outlets) *PageOutletsHandler {
	return &PageOutletsHandler{outlets: outlets}
}

func (h *PageOutletsHandler) Handle(ctx context.Context, q PageOutlets) (PageOutletsResult, error) {
	if err := q.Query.Validate(); err != nil {
		return PageOutletsResult{}, err
	}
	outlets, total, err := h.outlets.PageOutlets(ctx, q.Query)
	return PageOutletsResult{Outlets: outlets, Total: total}, err
}

// ---------- GetOutlet ----------

type GetOutlet struct{ ID int64 }

type GetOutletHandler struct{ outlets Outlets }

func NewGetOutletHandler(outlets Outlets) *GetOutletHandler {
	return &GetOutletHandler{outlets: outlets}
}

func (h *GetOutletHandler) Handle(ctx context.Context, q GetOutlet) (domain.Outlet, error) {
	return h.outlets.GetOutlet(ctx, q.ID)
}

// ---------- NearbyOutlets ----------

type NearbyOutlets struct{ Query domain.NearbyQuery }

type NearbyOutletsResult struct {
	Outlets []domain.OutletView
	// Next is the cursor of the next page, "" when there is none.
	Next string
}

type NearbyOutletsHandler struct{ discovery OutletDiscovery }

func NewNearbyOutletsHandler(discovery OutletDiscovery) *NearbyOutletsHandler {
	return &NearbyOutletsHandler{discovery: discovery}
}

// Handle pages through active outlets. On the first page an explicitly selected outlet is pinned to the top (unless a
// text query is narrowing the list), so the picker shows what the user already chose.
func (h *NearbyOutletsHandler) Handle(ctx context.Context, in NearbyOutlets) (NearbyOutletsResult, error) {
	q := in.Query
	if q.Limit <= 0 {
		q.Limit = domain.DefaultPageSize
	}
	if q.Limit > domain.MaxPageSize {
		q.Limit = domain.MaxPageSize
	}
	outlets, next, err := h.discovery.Nearby(ctx, q)
	if err != nil {
		return NearbyOutletsResult{}, err
	}
	if q.Cursor != "" || q.SelectedID <= 0 || q.Text != "" {
		return NearbyOutletsResult{Outlets: outlets, Next: next}, nil
	}
	selected, err := h.discovery.ByID(ctx, q.SelectedID, q.Near)
	if err != nil {
		// A stale or unknown selection must not break the list.
		return NearbyOutletsResult{Outlets: outlets, Next: next}, nil
	}
	pinned := make([]domain.OutletView, 0, len(outlets)+1)
	pinned = append(pinned, selected)
	for _, o := range outlets {
		if o.ID != selected.ID {
			pinned = append(pinned, o)
		}
	}
	if len(pinned) > q.Limit {
		pinned = pinned[:q.Limit]
	}
	return NearbyOutletsResult{Outlets: pinned, Next: next}, nil
}

// ---------- ResolveCoverage ----------

type ResolveCoverage struct{ Point domain.Point }

// ResolveCoverageResult says which service area a position is in; Found is false outside every area.
type ResolveCoverageResult struct {
	Area  domain.CoverageArea
	Found bool
}

type ResolveCoverageHandler struct{ discovery OutletDiscovery }

func NewResolveCoverageHandler(discovery OutletDiscovery) *ResolveCoverageHandler {
	return &ResolveCoverageHandler{discovery: discovery}
}

func (h *ResolveCoverageHandler) Handle(ctx context.Context, q ResolveCoverage) (ResolveCoverageResult, error) {
	area, ok, err := h.discovery.Coverage(ctx, q.Point)
	return ResolveCoverageResult{Area: area, Found: ok}, err
}
