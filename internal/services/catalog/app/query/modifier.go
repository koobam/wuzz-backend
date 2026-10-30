package query

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// ModifierGroups is what the modifier-group reads need of the store. List returns one page and the total number of
// groups the filter matches.
type ModifierGroups interface {
	ListModifierGroups(ctx context.Context, q domain.GroupQuery) ([]domain.ModifierGroup, int64, error)
	GetModifierGroup(ctx context.Context, id int64) (domain.ModifierGroup, error)
}

// ---------- ListModifierGroups ----------

type ListModifierGroups struct{ Query domain.GroupQuery }

type ListModifierGroupsResult struct {
	Groups []domain.ModifierGroup
	Total  int64
}

type ListModifierGroupsHandler struct{ groups ModifierGroups }

func NewListModifierGroupsHandler(groups ModifierGroups) *ListModifierGroupsHandler {
	return &ListModifierGroupsHandler{groups: groups}
}

// Handle lists the groups matching the query, in the order it asks for.
func (h *ListModifierGroupsHandler) Handle(ctx context.Context, q ListModifierGroups) (ListModifierGroupsResult, error) {
	if err := q.Query.Validate(true); err != nil {
		return ListModifierGroupsResult{}, err
	}
	groups, total, err := h.groups.ListModifierGroups(ctx, q.Query)
	return ListModifierGroupsResult{Groups: groups, Total: total}, err
}

// ---------- GetModifierGroup ----------

type GetModifierGroup struct{ ID int64 }

type GetModifierGroupHandler struct{ groups ModifierGroups }

func NewGetModifierGroupHandler(groups ModifierGroups) *GetModifierGroupHandler {
	return &GetModifierGroupHandler{groups: groups}
}

func (h *GetModifierGroupHandler) Handle(ctx context.Context, q GetModifierGroup) (domain.ModifierGroup, error) {
	return h.groups.GetModifierGroup(ctx, q.ID)
}
