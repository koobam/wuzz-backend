package command

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Materials is what the material writes need of the store.
type Materials interface {
	CreateMaterial(ctx context.Context, m domain.Material) (domain.Material, error)
	UpdateMaterial(ctx context.Context, m domain.Material) (domain.Material, error)
	// DeleteMaterial refuses while any recipe uses the material.
	DeleteMaterial(ctx context.Context, id int64) error
}

// ---------- CreateMaterial ----------

type CreateMaterial struct{ Input domain.MaterialInput }

type CreateMaterialHandler struct{ materials Materials }

func NewCreateMaterialHandler(materials Materials) *CreateMaterialHandler {
	return &CreateMaterialHandler{materials: materials}
}

// Handle reads the material exactly (domain.ParseMaterial) before it reaches the store.
func (h *CreateMaterialHandler) Handle(ctx context.Context, cmd CreateMaterial) (domain.Material, error) {
	m, err := domain.ParseMaterial(cmd.Input)
	if err != nil {
		return domain.Material{}, err
	}
	return h.materials.CreateMaterial(ctx, m)
}

// ---------- UpdateMaterial ----------

type UpdateMaterial struct {
	ID    int64
	Input domain.MaterialInput
}

type UpdateMaterialHandler struct{ materials Materials }

func NewUpdateMaterialHandler(materials Materials) *UpdateMaterialHandler {
	return &UpdateMaterialHandler{materials: materials}
}

func (h *UpdateMaterialHandler) Handle(ctx context.Context, cmd UpdateMaterial) (domain.Material, error) {
	m, err := domain.ParseMaterial(cmd.Input)
	if err != nil {
		return domain.Material{}, err
	}
	m.ID = cmd.ID
	return h.materials.UpdateMaterial(ctx, m)
}

// ---------- DeleteMaterial ----------

type DeleteMaterial struct{ ID int64 }

type DeleteMaterialHandler struct{ materials Materials }

func NewDeleteMaterialHandler(materials Materials) *DeleteMaterialHandler {
	return &DeleteMaterialHandler{materials: materials}
}

func (h *DeleteMaterialHandler) Handle(ctx context.Context, cmd DeleteMaterial) error {
	return h.materials.DeleteMaterial(ctx, cmd.ID)
}
