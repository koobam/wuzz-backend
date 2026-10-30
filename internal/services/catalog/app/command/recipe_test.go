package command

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type recipeFake struct {
	Recipes
	set int
}

func (f *recipeFake) SetBOM(_ context.Context, _ string, _ int64, lines []domain.BOMLine) ([]domain.BOMLine, error) {
	f.set++
	return lines, nil
}

func TestSetRecipeRefusesAnUnknownOwnerBeforeTheStore(t *testing.T) {
	f := &recipeFake{}
	if _, err := NewSetRecipeHandler(f).Handle(context.Background(), SetRecipe{OwnerType: "chair", OwnerID: 1}); err == nil {
		t.Error("a recipe owned by a chair was accepted")
	}
	if f.set != 0 {
		t.Errorf("the store was reached for an unknown owner: %d writes", f.set)
	}
}
