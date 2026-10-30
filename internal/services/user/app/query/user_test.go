package query

import (
	"context"
	"strconv"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// listFake records what List was asked for.
type listFake struct {
	AccountList
	kind string
	f    domain.ListFilter
}

func (l *listFake) List(_ context.Context, kind string, f domain.ListFilter) ([]domain.Account, int64, error) {
	l.kind, l.f = kind, f
	return nil, 0, nil
}

func TestListAccountsRefusesWhatItCannotList(t *testing.T) {
	for name, q := range map[string]ListAccounts{
		"a customer kind": {Kind: "user"},
		"no kind":         {},
		"a bad status":    {Kind: domain.KindStaff, Status: "deleted"},
	} {
		fake := &listFake{}
		if _, err := NewListAccountsHandler(fake).Handle(context.Background(), q); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
		if fake.kind != "" {
			t.Errorf("%s: reached the store with kind %q", name, fake.kind)
		}
	}
}

func TestListAccountsClampsThePage(t *testing.T) {
	for name, tc := range map[string]struct{ in, want domain.ListFilter }{
		"no limit gets the default": {domain.ListFilter{}, domain.ListFilter{Limit: 25, SortBy: "id", SortDir: "desc"}},
		"a huge limit is cut":       {domain.ListFilter{Limit: 5000}, domain.ListFilter{Limit: 25, SortBy: "id", SortDir: "desc"}},
		"a negative offset is zero": {domain.ListFilter{Limit: 10, Offset: -4}, domain.ListFilter{Limit: 10, SortBy: "id", SortDir: "desc"}},
		"a normal page is kept":     {domain.ListFilter{Query: "ani", Status: "active", Limit: 50, Offset: 100}, domain.ListFilter{Query: "ani", Status: "active", Limit: 50, Offset: 100, SortBy: "id", SortDir: "desc"}},
	} {
		fake := &listFake{}
		q := ListAccounts{Kind: domain.KindAdmin, Query: tc.in.Query, Status: tc.in.Status, Limit: tc.in.Limit, Offset: tc.in.Offset}
		if _, err := NewListAccountsHandler(fake).Handle(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fake.f != tc.want || fake.kind != domain.KindAdmin {
			t.Errorf("%s: store got kind %q filter %+v, want admin %+v", name, fake.kind, fake.f, tc.want)
		}
	}
}

func TestListSortAndOutletFilter(t *testing.T) {
	// Nothing asked: newest first, as the lists have always come. A column alone is ascending; a direction alone is about id.
	for name, c := range map[string]struct{ by, dir, wantBy, wantDir string }{
		"nothing":     {"", "", "id", "desc"},
		"a column":    {"name", "", "name", "asc"},
		"a direction": {"", "asc", "id", "asc"},
		"both":        {"created_at", "desc", "created_at", "desc"},
	} {
		fake := &listFake{}
		if _, err := NewListAccountsHandler(fake).Handle(context.Background(), ListAccounts{Kind: domain.KindStaff, OutletID: 3, SortBy: c.by, SortDir: c.dir}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fake.f.SortBy != c.wantBy || fake.f.SortDir != c.wantDir || fake.f.OutletID != 3 {
			t.Errorf("%s: store got %+v; want %s %s on outlet 3", name, fake.f, c.wantBy, c.wantDir)
		}
	}
	for name, q := range map[string]ListAccounts{
		"points are for customers": {Kind: domain.KindStaff, SortBy: "points"},
		"unknown column":           {Kind: domain.KindStaff, SortBy: "phone; drop table staff"},
		"unknown direction":        {Kind: domain.KindAdmin, SortBy: "name", SortDir: "up"},
		"negative outlet":          {Kind: domain.KindStaff, OutletID: -1},
	} {
		fake := &listFake{}
		if _, err := NewListAccountsHandler(fake).Handle(context.Background(), q); err == nil || fake.kind != "" {
			t.Errorf("%s: err = %v, reached the store = %v", name, err, fake.kind != "")
		}
	}
	if _, err := NewListAccountsHandler(&listFake{}).Handle(context.Background(), ListAccounts{Kind: domain.KindStaff, SortBy: "bogus"}); err == nil || err.Error() != "Unknown sort column" {
		t.Errorf("message = %v; want \"Unknown sort column\"", err)
	}
}

type usersFake struct {
	UserList
	f domain.ListFilter
}

func (u *usersFake) List(_ context.Context, f domain.ListFilter) ([]domain.User, int64, error) {
	u.f = f
	return nil, 0, nil
}

func TestListUsersSort(t *testing.T) {
	fake := &usersFake{}
	if _, err := NewListUsersHandler(fake).Handle(context.Background(), ListUsers{SortBy: "points", SortDir: "desc"}); err != nil || fake.f.SortBy != "points" || fake.f.SortDir != "desc" {
		t.Errorf("points desc: %+v, %v", fake.f, err)
	}
	if _, err := NewListUsersHandler(fake).Handle(context.Background(), ListUsers{}); err != nil || fake.f.SortBy != "id" || fake.f.SortDir != "desc" {
		t.Errorf("default: %+v, %v", fake.f, err)
	}
	if _, err := NewListUsersHandler(fake).Handle(context.Background(), ListUsers{SortBy: "email"}); err == nil {
		t.Error("a column outside the whitelist is refused")
	}
}

// resolveFake answers Resolve with the customers it holds, and records what it was asked for.
type resolveFake struct {
	have   []domain.User
	ids    []int64
	phones []string
	asked  int
}

func (r *resolveFake) Resolve(_ context.Context, ids []int64, phones []string) ([]domain.User, error) {
	r.asked++
	r.ids, r.phones = ids, phones
	var out []domain.User
	for _, u := range r.have {
		match := false
		for _, id := range ids {
			match = match || u.ID == strconv.FormatInt(id, 10)
		}
		for _, p := range phones {
			match = match || u.Phone == p
		}
		if match {
			out = append(out, u)
		}
	}
	return out, nil
}

func TestResolveCustomers(t *testing.T) {
	fake := &resolveFake{have: []domain.User{{ID: "7", Phone: "6281234500201"}, {ID: "8", Phone: "6281234500202"}}}
	h := NewResolveCustomersHandler(fake)
	res, err := h.Handle(context.Background(), ResolveCustomers{
		IDs:    []string{"7", " 9 ", "a:1", "zzz", "-3"},
		Phones: []string{"081234500202", "+62 812-3450-0201", "0899999999", "abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Users) != 2 {
		t.Errorf("users = %+v", res.Users)
	}
	// What was asked is cleaned: only real ids, and phone numbers in the form sign-up stores.
	if len(fake.ids) != 2 || fake.ids[0] != 7 || fake.ids[1] != 9 {
		t.Errorf("ids sent to the store = %v", fake.ids)
	}
	if len(fake.phones) != 3 || fake.phones[0] != "6281234500202" || fake.phones[1] != "6281234500201" || fake.phones[2] != "62899999999" {
		t.Errorf("phones sent to the store = %v", fake.phones)
	}
	// Unmatched are the inputs as given: a missing id, ids that are no ids, a number nobody has, a string that is no number.
	want := []string{" 9 ", "a:1", "zzz", "-3", "0899999999", "abc"}
	if len(res.Unmatched) != len(want) {
		t.Fatalf("unmatched = %q, want %q", res.Unmatched, want)
	}
	for i := range want {
		if res.Unmatched[i] != want[i] {
			t.Errorf("unmatched = %q, want %q", res.Unmatched, want)
			break
		}
	}
}

func TestResolveCustomersAsksNothingOfNothing(t *testing.T) {
	fake := &resolveFake{}
	res, err := NewResolveCustomersHandler(fake).Handle(context.Background(), ResolveCustomers{IDs: []string{"x"}, Phones: []string{"!"}})
	if err != nil || fake.asked != 0 || len(res.Users) != 0 || len(res.Unmatched) != 2 {
		t.Errorf("res %+v err %v, store asked %d times", res, err, fake.asked)
	}
	many := make([]string, maxResolve+1)
	if _, err := NewResolveCustomersHandler(fake).Handle(context.Background(), ResolveCustomers{IDs: many}); err == nil {
		t.Error("more than the limit should be refused")
	}
}
