package query

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// serviceDiscoveryFake counts the reads that reached discovery.
type serviceDiscoveryFake struct {
	ServiceDiscovery
	browsed int
}

func (f *serviceDiscoveryFake) Services(context.Context, string, int) ([]domain.ServiceView, error) {
	f.browsed++
	return nil, nil
}

func TestBrowseServicesRefusesAnUnknownCategory(t *testing.T) {
	f := &serviceDiscoveryFake{}
	h := NewBrowseServicesHandler(f)

	if _, err := h.Handle(context.Background(), BrowseServices{Category: "premium"}); err == nil {
		t.Error("an unknown category was accepted")
	}
	if f.browsed != 0 {
		t.Errorf("discovery was reached for a bad category: %d reads", f.browsed)
	}
	for _, ok := range []string{"", domain.ServiceCategoryRegular, domain.ServiceCategoryAlacarte} {
		if _, err := h.Handle(context.Background(), BrowseServices{Category: ok}); err != nil {
			t.Errorf("category %q: %v", ok, err)
		}
	}
}
