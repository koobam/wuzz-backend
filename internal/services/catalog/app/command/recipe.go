package command

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Recipes is what the recipe (bill of materials) write needs of the store.
type Recipes interface {
	SetBOM(ctx context.Context, ownerType string, ownerID int64, lines []domain.BOMLine) ([]domain.BOMLine, error)
}

// ---------- SetRecipe ----------

type SetRecipe struct {
	OwnerType string
	OwnerID   int64
	Lines     []domain.BOMLineInput
}

type SetRecipeHandler struct{ recipes Recipes }

func NewSetRecipeHandler(recipes Recipes) *SetRecipeHandler {
	return &SetRecipeHandler{recipes: recipes}
}

// Handle replaces the owner's whole recipe with the given lines.
func (h *SetRecipeHandler) Handle(ctx context.Context, cmd SetRecipe) ([]domain.BOMLine, error) {
	if err := domain.ValidateOwner(cmd.OwnerType); err != nil {
		return nil, err
	}
	lines, err := domain.ParseBOM(cmd.Lines)
	if err != nil {
		return nil, err
	}
	return h.recipes.SetBOM(ctx, cmd.OwnerType, cmd.OwnerID, lines)
}
