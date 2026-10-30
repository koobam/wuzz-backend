package query

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type recipeReadFake struct {
	Recipes
	read int
}

func (f *recipeReadFake) GetBOM(context.Context, string, int64) ([]domain.BOMLine, error) {
	f.read++
	return nil, nil
}

func TestGetRecipeRefusesAnUnknownOwnerBeforeTheStore(t *testing.T) {
	f := &recipeReadFake{}
	if _, err := NewGetRecipeHandler(f).Handle(context.Background(), GetRecipe{OwnerType: "chair", OwnerID: 1}); err == nil {
		t.Error("an unknown owner was accepted")
	}
	if f.read != 0 {
		t.Errorf("the store was reached for an unknown owner: %d reads", f.read)
	}
	if _, err := NewGetRecipeHandler(f).Handle(context.Background(), GetRecipe{OwnerType: domain.OwnerService, OwnerID: 1}); err != nil || f.read != 1 {
		t.Errorf("a service recipe: err %v, reads %d", err, f.read)
	}
}
