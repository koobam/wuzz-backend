package query

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

func TestListAndSummaryShareTheirFilterRules(t *testing.T) {
	admin := domain.Caller{Admin: true}
	cashier := domain.Caller{Outlets: []int64{1}}
	good, err := scope(admin, 0, "fulfilled", "  trx-ab ", "2026-10-01", "2026-10-25")
	if err != nil || good.Query != "trx-ab" || good.From != "2026-10-01" || good.To != "2026-10-25" || len(good.OutletIDs) != 0 {
		t.Fatalf("good = %+v, %v", good, err)
	}
	if f, err := scope(cashier, 0, "", "", "", ""); err != nil || len(f.OutletIDs) != 1 || f.OutletIDs[0] != 1 {
		t.Errorf("a cashier sees their outlets: %+v, %v", f, err)
	}
	if _, err := scope(cashier, 2, "", "", "", ""); err == nil {
		t.Error("another outlet is refused")
	}
	if _, err := scope(domain.Caller{}, 0, "", "", "", ""); err == nil {
		t.Error("a caller with no roles is refused")
	}
	// One end alone is fine; the far ends of a year are fine; a year and a day is not.
	for name, ft := range map[string][2]string{"from only": {"2026-10-01", ""}, "to only": {"", "2026-10-25"}, "366 days": {"2025-10-25", "2026-10-25"}} {
		if _, err := scope(admin, 0, "", "", ft[0], ft[1]); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'x'
	}
	bad := map[string][4]string{
		"unknown status": {"shipped", "", "", ""},
		"long search":    {"", string(long), "", ""},
		"bad from":       {"", "", "25/10/2026", ""},
		"bad to":         {"", "", "", "2026-13-40"},
		"reversed":       {"", "", "2026-10-25", "2026-10-24"},
		"367 days":       {"", "", "2025-10-24", "2026-10-25"},
	}
	for name, v := range bad {
		if _, err := scope(admin, 0, v[0], v[1], v[2], v[3]); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// listSpy is a store that only answers ListOrders and remembers the filter it was given.
type listSpy struct {
	Orders
	got   domain.ListFilter
	calls int
}

func (s *listSpy) ListOrders(_ context.Context, f domain.ListFilter) ([]domain.Order, int64, error) {
	s.got, s.calls = f, s.calls+1
	return nil, 0, nil
}

func TestListOrdersReadsTheRequestBeforeTheStore(t *testing.T) {
	admin := domain.Caller{Admin: true}
	spy := &listSpy{}
	h := NewListOrdersHandler(spy)

	for name, q := range map[string]ListOrders{
		"an unknown sort column":    {Caller: admin, SortBy: "password"},
		"an unknown sort direction": {Caller: admin, SortDir: "sideways"},
		"not staff":                 {Caller: domain.Caller{}},
	} {
		if _, err := h.Handle(context.Background(), q); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if spy.calls != 0 {
		t.Fatalf("a refused request reached the store %d times", spy.calls)
	}

	for name, tc := range map[string]struct {
		in         ListOrders
		wantLimit  int
		wantOffset int
		wantSort   string
		wantDesc   bool
	}{
		"defaults are newest first, 25 a page": {ListOrders{Caller: admin}, 25, 0, "created_at", true},
		"a huge page is cut":                   {ListOrders{Caller: admin, Limit: 5000}, 25, 0, "created_at", true},
		"a negative offset is zero":            {ListOrders{Caller: admin, Limit: 10, Offset: -4}, 10, 0, "created_at", true},
		"ascending by total":                   {ListOrders{Caller: admin, SortBy: "total", SortDir: "asc", Limit: 50, Offset: 100}, 50, 100, "total", false},
	} {
		if _, err := h.Handle(context.Background(), tc.in); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		g := spy.got
		if g.Limit != tc.wantLimit || g.Offset != tc.wantOffset || g.SortBy != tc.wantSort || g.Desc != tc.wantDesc {
			t.Errorf("%s: store got limit %d offset %d sort %q desc %v", name, g.Limit, g.Offset, g.SortBy, g.Desc)
		}
	}
}

// statsSpy is a store that only answers Stats and remembers what it was asked.
type statsSpy struct {
	Orders
	got     domain.StatsRange
	outlets []int64
}

func (s *statsSpy) Stats(_ context.Context, outlets []int64, r domain.StatsRange) (domain.Stats, error) {
	s.got, s.outlets = r, outlets
	return domain.Stats{}, nil
}

func TestStatsRange(t *testing.T) {
	admin := domain.Caller{Admin: true}
	cases := []struct {
		name     string
		days     int
		from, to string
		ok       bool
		want     domain.StatsRange
	}{
		{"default is a week", 0, "", "", true, domain.StatsRange{Days: 7}},
		{"the last 30 days", 30, "", "", true, domain.StatsRange{Days: 30}},
		{"too many days falls back to a week", 400, "", "", true, domain.StatsRange{Days: 7}},
		{"a chosen range", 0, "2026-10-01", "2026-10-25", true, domain.StatsRange{From: "2026-10-01", To: "2026-10-25", Days: 7}},
		{"one day", 0, "2026-10-25", "2026-10-25", true, domain.StatsRange{From: "2026-10-25", To: "2026-10-25", Days: 7}},
		{"exactly 92 days", 0, "2026-07-26", "2026-10-25", true, domain.StatsRange{From: "2026-07-26", To: "2026-10-25", Days: 7}},
		{"93 days is too long", 0, "2026-07-25", "2026-10-25", false, domain.StatsRange{}},
		{"backwards", 0, "2026-10-25", "2026-10-01", false, domain.StatsRange{}},
		{"only from", 0, "2026-10-01", "", false, domain.StatsRange{}},
		{"only to", 0, "", "2026-10-25", false, domain.StatsRange{}},
		{"not dates", 0, "yesterday", "today", false, domain.StatsRange{}},
		{"impossible date", 0, "2026-02-30", "2026-03-02", false, domain.StatsRange{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spy := &statsSpy{}
			_, err := NewOrderStatsHandler(spy).Handle(context.Background(), OrderStats{Caller: admin, Days: c.days, From: c.from, To: c.to})
			if (err == nil) != c.ok {
				t.Fatalf("error = %v, want ok=%v", err, c.ok)
			}
			if c.ok && spy.got != c.want {
				t.Errorf("store got %+v, want %+v", spy.got, c.want)
			}
		})
	}
}

func TestStatsNeedsStaff(t *testing.T) {
	if _, err := NewOrderStatsHandler(&statsSpy{}).Handle(context.Background(), OrderStats{Caller: domain.Caller{}, Days: 7}); err == nil {
		t.Error("a customer must not read the statistics")
	}
}

func TestStatsAreScopedToTheCallersOutlets(t *testing.T) {
	cashier := domain.Caller{Outlets: []int64{1, 3}}
	spy := &statsSpy{}
	h := NewOrderStatsHandler(spy)

	if _, err := h.Handle(context.Background(), OrderStats{Caller: cashier, Days: 7}); err != nil {
		t.Fatal(err)
	}
	if len(spy.outlets) != 2 {
		t.Errorf("a cashier sees their own outlets, store was asked for %v", spy.outlets)
	}
	if _, err := h.Handle(context.Background(), OrderStats{Caller: cashier, OutletID: 2, Days: 7}); err == nil {
		t.Error("another outlet's statistics were readable")
	}
	if _, err := h.Handle(context.Background(), OrderStats{Caller: domain.Caller{Admin: true}, Days: 7}); err != nil || len(spy.outlets) != 0 {
		t.Errorf("an admin sees every outlet (nil), store was asked for %v (%v)", spy.outlets, err)
	}
}
