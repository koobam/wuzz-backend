package query

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Recipes is what the recipe (bill of materials) read needs of the store.
type Recipes interface {
	GetBOM(ctx context.Context, ownerType string, ownerID int64) ([]domain.BOMLine, error)
}

// Costing is what the costing reads need of the store.
type Costing interface {
	LoadCostingContext(ctx context.Context, serviceID int64) (domain.CostingContext, error)
}

// ---------- GetRecipe ----------

type GetRecipe struct {
	OwnerType string
	OwnerID   int64
}

type GetRecipeHandler struct{ recipes Recipes }

func NewGetRecipeHandler(recipes Recipes) *GetRecipeHandler {
	return &GetRecipeHandler{recipes: recipes}
}

func (h *GetRecipeHandler) Handle(ctx context.Context, q GetRecipe) ([]domain.BOMLine, error) {
	if err := domain.ValidateOwner(q.OwnerType); err != nil {
		return nil, err
	}
	return h.recipes.GetBOM(ctx, q.OwnerType, q.OwnerID)
}

// ---------- GetServiceCosting ----------

type GetServiceCosting struct{ ServiceID int64 }

type GetServiceCostingHandler struct{ costing Costing }

func NewGetServiceCostingHandler(costing Costing) *GetServiceCostingHandler {
	return &GetServiceCostingHandler{costing: costing}
}

// Handle loads a service with its recipe and the recipe of each option it offers: the cost table of the service.
func (h *GetServiceCostingHandler) Handle(ctx context.Context, q GetServiceCosting) (domain.CostingContext, error) {
	return h.costing.LoadCostingContext(ctx, q.ServiceID)
}

// ---------- CalcCosting ----------

type CalcCosting struct {
	ServiceID int64
	Input     domain.CalcInput
}

type CalcCostingResult struct {
	Context domain.CostingContext
	Result  domain.CalcResult
}

type CalcCostingHandler struct{ costing Costing }

func NewCalcCostingHandler(costing Costing) *CalcCostingHandler {
	return &CalcCostingHandler{costing: costing}
}

// Handle costs a sale of a service exactly.
func (h *CalcCostingHandler) Handle(ctx context.Context, q CalcCosting) (CalcCostingResult, error) {
	c, err := h.costing.LoadCostingContext(ctx, q.ServiceID)
	if err != nil {
		return CalcCostingResult{Context: c}, err
	}
	res, err := domain.Calc(c, q.Input)
	return CalcCostingResult{Context: c, Result: res}, err
}
