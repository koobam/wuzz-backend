package query

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// VariantGroups is what the variant-group reads need of the store. List returns one page and the total number of
// groups the filter matches.
type VariantGroups interface {
	ListVariantGroups(ctx context.Context, q domain.GroupQuery) ([]domain.VariantGroup, int64, error)
	GetVariantGroup(ctx context.Context, id int64) (domain.VariantGroup, error)
}

// ---------- ListVariantGroups ----------

type ListVariantGroups struct{ Query domain.GroupQuery }

type ListVariantGroupsResult struct {
	Groups []domain.VariantGroup
	Total  int64
}

type ListVariantGroupsHandler struct{ groups VariantGroups }

func NewListVariantGroupsHandler(groups VariantGroups) *ListVariantGroupsHandler {
	return &ListVariantGroupsHandler{groups: groups}
}

// Handle lists the groups matching the query, in the order it asks for.
func (h *ListVariantGroupsHandler) Handle(ctx context.Context, q ListVariantGroups) (ListVariantGroupsResult, error) {
	if err := q.Query.Validate(false); err != nil {
		return ListVariantGroupsResult{}, err
	}
	groups, total, err := h.groups.ListVariantGroups(ctx, q.Query)
	return ListVariantGroupsResult{Groups: groups, Total: total}, err
}

// ---------- GetVariantGroup ----------

type GetVariantGroup struct{ ID int64 }

type GetVariantGroupHandler struct{ groups VariantGroups }

func NewGetVariantGroupHandler(groups VariantGroups) *GetVariantGroupHandler {
	return &GetVariantGroupHandler{groups: groups}
}

func (h *GetVariantGroupHandler) Handle(ctx context.Context, q GetVariantGroup) (domain.VariantGroup, error) {
	return h.groups.GetVariantGroup(ctx, q.ID)
}
