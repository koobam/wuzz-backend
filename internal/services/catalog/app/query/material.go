package query

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Materials is what the material reads need of the store.
type Materials interface {
	ListMaterials(ctx context.Context, q domain.MaterialQuery) ([]domain.Material, int64, error)
	GetMaterial(ctx context.Context, id int64) (domain.Material, error)
}

// ---------- ListMaterials ----------

type ListMaterials struct{ Query domain.MaterialQuery }

type ListMaterialsResult struct {
	Materials []domain.Material
	Total     int64
}

type ListMaterialsHandler struct{ materials Materials }

func NewListMaterialsHandler(materials Materials) *ListMaterialsHandler {
	return &ListMaterialsHandler{materials: materials}
}

func (h *ListMaterialsHandler) Handle(ctx context.Context, q ListMaterials) (ListMaterialsResult, error) {
	if err := q.Query.Validate(); err != nil {
		return ListMaterialsResult{}, err
	}
	items, total, err := h.materials.ListMaterials(ctx, q.Query)
	return ListMaterialsResult{Materials: items, Total: total}, err
}

// ---------- GetMaterial ----------

type GetMaterial struct{ ID int64 }

type GetMaterialHandler struct{ materials Materials }

func NewGetMaterialHandler(materials Materials) *GetMaterialHandler {
	return &GetMaterialHandler{materials: materials}
}

func (h *GetMaterialHandler) Handle(ctx context.Context, q GetMaterial) (domain.Material, error) {
	return h.materials.GetMaterial(ctx, q.ID)
}
