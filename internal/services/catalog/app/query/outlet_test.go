package query

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type discoveryFake struct {
	OutletDiscovery
	outlets  []domain.OutletView
	byID     map[int64]domain.OutletView
	gotQuery domain.NearbyQuery
}

func (f *discoveryFake) Nearby(_ context.Context, q domain.NearbyQuery) ([]domain.OutletView, string, error) {
	f.gotQuery = q
	return f.outlets, "next-page", nil
}

func (f *discoveryFake) ByID(_ context.Context, id int64, _ *domain.Point) (domain.OutletView, error) {
	if o, ok := f.byID[id]; ok {
		return o, nil
	}
	return domain.OutletView{}, errors.New("unknown outlet")
}

func view(ids ...int64) []domain.OutletView {
	out := make([]domain.OutletView, 0, len(ids))
	for _, id := range ids {
		out = append(out, domain.OutletView{ID: id})
	}
	return out
}

func idsOf(vs []domain.OutletView) []int64 {
	out := make([]int64, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

func TestNearbyOutletsClampsThePageSize(t *testing.T) {
	for name, tc := range map[string]struct{ in, want int }{
		"none asked gets the default": {0, domain.DefaultPageSize},
		"a negative gets the default": {-3, domain.DefaultPageSize},
		"a huge page is cut":          {5000, domain.MaxPageSize},
		"a normal page is kept":       {20, 20},
	} {
		f := &discoveryFake{}
		if _, err := NewNearbyOutletsHandler(f).Handle(context.Background(), NearbyOutlets{Query: domain.NearbyQuery{Limit: tc.in}}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if f.gotQuery.Limit != tc.want {
			t.Errorf("%s: discovery was asked for %d, want %d", name, f.gotQuery.Limit, tc.want)
		}
	}
}

func TestNearbyOutletsPinsTheSelectedOutletOnTheFirstPage(t *testing.T) {
	f := &discoveryFake{outlets: view(1, 2, 3), byID: map[int64]domain.OutletView{3: {ID: 3}, 9: {ID: 9}}}
	h := NewNearbyOutletsHandler(f)
	ctx := context.Background()

	// Selected one is already in the list: it moves to the top, nothing is duplicated.
	got, err := h.Handle(ctx, NearbyOutlets{Query: domain.NearbyQuery{SelectedID: 3, Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{3, 1, 2}; !slices.Equal(idsOf(got.Outlets), want) || got.Next != "next-page" {
		t.Errorf("pinned in the list: got %v next %q, want %v", idsOf(got.Outlets), got.Next, want)
	}

	// Selected one is not in the page: it is added on top and the page keeps its size.
	got, _ = h.Handle(ctx, NearbyOutlets{Query: domain.NearbyQuery{SelectedID: 9, Limit: 3}})
	if want := []int64{9, 1, 2}; !slices.Equal(idsOf(got.Outlets), want) {
		t.Errorf("pinned from outside: got %v, want %v", idsOf(got.Outlets), want)
	}
}

func TestNearbyOutletsDoesNotPinWhenItWouldMislead(t *testing.T) {
	f := &discoveryFake{outlets: view(1, 2, 3), byID: map[int64]domain.OutletView{3: {ID: 3}}}
	h := NewNearbyOutletsHandler(f)
	ctx := context.Background()
	plain := []int64{1, 2, 3}

	for name, q := range map[string]domain.NearbyQuery{
		"a later page":          {SelectedID: 3, Cursor: "c1"},
		"a text search":         {SelectedID: 3, Text: "pusat"},
		"no selection":          {},
		"a stale selection":     {SelectedID: 404},
		"a non-positive choice": {SelectedID: -1},
	} {
		got, err := h.Handle(ctx, NearbyOutlets{Query: q})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !slices.Equal(idsOf(got.Outlets), plain) {
			t.Errorf("%s: got %v, want the list untouched %v", name, idsOf(got.Outlets), plain)
		}
	}
}
