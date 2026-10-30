package command

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// ModifierGroups is what the modifier-group writes need of the store.
type ModifierGroups interface {
	CreateModifierGroup(ctx context.Context, g domain.ModifierGroup) (domain.ModifierGroup, error)
	UpdateModifierGroup(ctx context.Context, g domain.ModifierGroup) (domain.ModifierGroup, error)
	// DeleteModifierGroup refuses while a service still offers the group.
	DeleteModifierGroup(ctx context.Context, id int64) error
}

// ---------- CreateModifierGroup ----------

type CreateModifierGroup struct{ Group domain.ModifierGroup }

type CreateModifierGroupHandler struct{ groups ModifierGroups }

func NewCreateModifierGroupHandler(groups ModifierGroups) *CreateModifierGroupHandler {
	return &CreateModifierGroupHandler{groups: groups}
}

// Handle validates the group before it reaches the store; the store, not the caller, assigns the id.
func (h *CreateModifierGroupHandler) Handle(ctx context.Context, cmd CreateModifierGroup) (domain.ModifierGroup, error) {
	g := cmd.Group
	g.ID = 0
	if err := g.Validate(); err != nil {
		return domain.ModifierGroup{}, err
	}
	return h.groups.CreateModifierGroup(ctx, g)
}

// ---------- UpdateModifierGroup ----------

type UpdateModifierGroup struct{ Group domain.ModifierGroup }

type UpdateModifierGroupHandler struct{ groups ModifierGroups }

func NewUpdateModifierGroupHandler(groups ModifierGroups) *UpdateModifierGroupHandler {
	return &UpdateModifierGroupHandler{groups: groups}
}

func (h *UpdateModifierGroupHandler) Handle(ctx context.Context, cmd UpdateModifierGroup) (domain.ModifierGroup, error) {
	g := cmd.Group
	if err := g.Validate(); err != nil {
		return domain.ModifierGroup{}, err
	}
	return h.groups.UpdateModifierGroup(ctx, g)
}

// ---------- DeleteModifierGroup ----------

type DeleteModifierGroup struct{ ID int64 }

type DeleteModifierGroupHandler struct{ groups ModifierGroups }

func NewDeleteModifierGroupHandler(groups ModifierGroups) *DeleteModifierGroupHandler {
	return &DeleteModifierGroupHandler{groups: groups}
}

func (h *DeleteModifierGroupHandler) Handle(ctx context.Context, cmd DeleteModifierGroup) error {
	return h.groups.DeleteModifierGroup(ctx, cmd.ID)
}
