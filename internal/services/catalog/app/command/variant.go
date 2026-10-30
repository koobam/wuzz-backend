package command

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// VariantGroups is what the variant-group writes need of the store.
type VariantGroups interface {
	CreateVariantGroup(ctx context.Context, g domain.VariantGroup) (domain.VariantGroup, error)
	UpdateVariantGroup(ctx context.Context, g domain.VariantGroup) (domain.VariantGroup, error)
	// DeleteVariantGroup refuses while a service still offers the group.
	DeleteVariantGroup(ctx context.Context, id int64) error
}

// ---------- CreateVariantGroup ----------

type CreateVariantGroup struct{ Group domain.VariantGroup }

type CreateVariantGroupHandler struct{ groups VariantGroups }

func NewCreateVariantGroupHandler(groups VariantGroups) *CreateVariantGroupHandler {
	return &CreateVariantGroupHandler{groups: groups}
}

// Handle validates the group before it reaches the store; the store, not the caller, assigns the id.
func (h *CreateVariantGroupHandler) Handle(ctx context.Context, cmd CreateVariantGroup) (domain.VariantGroup, error) {
	g := cmd.Group
	g.ID = 0
	if err := g.Validate(); err != nil {
		return domain.VariantGroup{}, err
	}
	return h.groups.CreateVariantGroup(ctx, g)
}

// ---------- UpdateVariantGroup ----------

type UpdateVariantGroup struct{ Group domain.VariantGroup }

type UpdateVariantGroupHandler struct{ groups VariantGroups }

func NewUpdateVariantGroupHandler(groups VariantGroups) *UpdateVariantGroupHandler {
	return &UpdateVariantGroupHandler{groups: groups}
}

func (h *UpdateVariantGroupHandler) Handle(ctx context.Context, cmd UpdateVariantGroup) (domain.VariantGroup, error) {
	g := cmd.Group
	if err := g.Validate(); err != nil {
		return domain.VariantGroup{}, err
	}
	return h.groups.UpdateVariantGroup(ctx, g)
}

// ---------- DeleteVariantGroup ----------

type DeleteVariantGroup struct{ ID int64 }

type DeleteVariantGroupHandler struct{ groups VariantGroups }

func NewDeleteVariantGroupHandler(groups VariantGroups) *DeleteVariantGroupHandler {
	return &DeleteVariantGroupHandler{groups: groups}
}

func (h *DeleteVariantGroupHandler) Handle(ctx context.Context, cmd DeleteVariantGroup) error {
	return h.groups.DeleteVariantGroup(ctx, cmd.ID)
}
