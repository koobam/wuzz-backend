package command

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type serviceFake struct {
	Services
	created []domain.ServiceItem
}

func (f *serviceFake) CreateService(_ context.Context, s domain.ServiceItem) (domain.ServiceItem, error) {
	f.created = append(f.created, s)
	return s, nil
}

func TestCreateServiceIsAlwaysActiveAndValidated(t *testing.T) {
	f := &serviceFake{}
	h := NewCreateServiceHandler(f)

	if _, err := h.Handle(context.Background(), CreateService{Service: domain.ServiceItem{Name: "Kiloan", Unit: domain.UnitKg, Price: 7000}}); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || !f.created[0].Active {
		t.Errorf("a new service must be stored active, got %+v", f.created)
	}

	if _, err := h.Handle(context.Background(), CreateService{Service: domain.ServiceItem{Name: "Kiloan", Unit: "box", Price: 7000}}); err == nil {
		t.Error("a service priced per box was accepted")
	}
	if len(f.created) != 1 {
		t.Errorf("an invalid service reached the store: %d writes", len(f.created))
	}
}

// A create that says nothing about a discount has none; one that asks for more than the price is refused before the store.
func TestCreateServiceDiscountDefaultsAndLimits(t *testing.T) {
	f := &serviceFake{}
	h := NewCreateServiceHandler(f)
	ctx := context.Background()

	if _, err := h.Handle(ctx, CreateService{Service: domain.ServiceItem{Name: "Kiloan", Unit: domain.UnitKg, Price: 7000}}); err != nil {
		t.Fatal(err)
	}
	if f.created[0].DiscountType != domain.DiscountNone || f.created[0].DiscountValue != 0 {
		t.Errorf("default discount: %+v", f.created[0])
	}
	if _, err := h.Handle(ctx, CreateService{Service: domain.ServiceItem{Name: "Kiloan", Unit: domain.UnitKg, Price: 7000, DiscountType: domain.DiscountValue, DiscountValue: 7001}}); err == nil {
		t.Error("a discount above the price was accepted")
	}
	if _, err := h.Handle(ctx, CreateService{Service: domain.ServiceItem{Name: "Kiloan", Unit: domain.UnitKg, Price: 7000, DiscountType: domain.DiscountPercent, DiscountValue: 10001}}); err == nil {
		t.Error("a discount above 100% was accepted")
	}
	if len(f.created) != 1 {
		t.Errorf("an invalid discount reached the store: %d writes", len(f.created))
	}
}
