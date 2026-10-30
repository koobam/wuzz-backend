package command

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type groupFake struct {
	VariantGroups
	created int
}

func (f *groupFake) CreateVariantGroup(_ context.Context, g domain.VariantGroup) (domain.VariantGroup, error) {
	f.created++
	return g, nil
}

func TestAVariantGroupWithoutOptionsNeverReachesTheStore(t *testing.T) {
	f := &groupFake{}
	if _, err := NewCreateVariantGroupHandler(f).Handle(context.Background(), CreateVariantGroup{Group: domain.VariantGroup{Name: "Kecepatan"}}); err == nil {
		t.Error("a group with no options was accepted")
	}
	if f.created != 0 {
		t.Errorf("an invalid group reached the store: %d writes", f.created)
	}
}
