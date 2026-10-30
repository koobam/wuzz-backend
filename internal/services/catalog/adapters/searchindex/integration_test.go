package searchindex

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/search"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Runs against a real Typesense when TYPESENSE_TEST_URL is set (the local stack's is http://localhost:8109); skipped otherwise. It works
// in collections of its own (test_catalog_*) and removes them afterwards.
func TestAgainstTypesense(t *testing.T) {
	url := os.Getenv("TYPESENSE_TEST_URL")
	if url == "" {
		t.Skip("TYPESENSE_TEST_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := search.New(url, os.Getenv("TYPESENSE_TEST_API_KEY"))
	aliasPrefix = "test_catalog_"
	ix := New(client, slog.Default())
	t.Cleanup(func() {
		defer func() { aliasPrefix = "catalog_" }()
		for _, k := range Kinds {
			_ = client.DeleteAlias(context.Background(), Alias(k))
			_ = client.DeleteCollection(context.Background(), fmt.Sprintf("%s_v%d", Alias(k), SchemaVersion))
			_ = client.DeleteCollection(context.Background(), fmt.Sprintf("%s_v%d_rebuild", Alias(k), SchemaVersion))
		}
	})

	soon, gone := time.Now().Add(time.Hour).Truncate(time.Second), time.Now().Add(-time.Hour).Truncate(time.Second)
	src := func(_ context.Context, k Kind) ([]map[string]any, error) {
		switch k {
		case Outlets:
			return []map[string]any{
				OutletDoc(domain.Outlet{ID: 1, Name: "Wuzz BSD", Address: "Jl. Pahlawan Seribu", Phone: "0211", Active: true}),
				OutletDoc(domain.Outlet{ID: 2, Name: "wuzz Kemang", Address: "Jl. Kemang Raya", Phone: "0212", Active: false}),
				OutletDoc(domain.Outlet{ID: 10, Name: "Alpha Laundry", Address: "Jl. Mawar", Phone: "0219", Active: true}),
			}, nil
		case Services:
			return []map[string]any{
				ServiceDoc(domain.ServiceItem{ID: 1, Name: "Cuci Kiloan", Unit: "kg", Price: 8000, Active: true, CategoryID: 1}, "Pakaian"),
				ServiceDoc(domain.ServiceItem{ID: 2, Name: "Cuci Sepatu", Unit: "pcs", Price: 35000, Active: true, CategoryID: 2}, "Sepatu"),
			}, nil
		case VariantGroups, ModifierGroups:
			return []map[string]any{
				GroupDoc(Group{ID: 1, Name: "Ukuran", OptionNames: []string{"Base", "Large"}, ServiceIDs: []int64{1, 2}}),
				GroupDoc(Group{ID: 2, Name: "Pewangi", OptionNames: []string{"Lavender"}, Min: 1}),
			}, nil
		case Banners:
			return []map[string]any{
				BannerDoc(domain.Banner{ID: 1, Title: "Promo Oktober", Status: domain.BannerActive, SortIndex: 2}),
				BannerDoc(domain.Banner{ID: 2, Title: "Diskon Kemang", Status: domain.BannerActive, SortIndex: 1, OutletIDs: []int64{7}}),
				BannerDoc(domain.Banner{ID: 3, Title: "Draft", Status: domain.BannerInactive, SortIndex: 3}),
				BannerDoc(domain.Banner{ID: 4, Title: "Belum mulai", Status: domain.BannerActive, SortIndex: 4, StartsAt: &soon}),
				BannerDoc(domain.Banner{ID: 5, Title: "Sudah usai", Status: domain.BannerActive, SortIndex: 5, EndsAt: &gone}),
			}, nil
		case Vouchers:
			return []map[string]any{
				VoucherDoc(domain.Voucher{ID: 1, Title: "Diskon Oktober", Status: domain.VoucherActive, DiscountType: domain.DiscountPercentage, Value: 10}),
				VoucherDoc(domain.Voucher{ID: 2, Code: "KEMANG20", Title: "Kemang", Status: domain.VoucherActive, DiscountType: domain.DiscountAmount, Value: 20000, ClaimsCount: 5, OutletIDs: []int64{7}}),
				VoucherDoc(domain.Voucher{ID: 3, Code: "DRAFT", Title: "Draft", Status: domain.VoucherInactive, DiscountType: domain.DiscountAmount, Value: 5000}),
				VoucherDoc(domain.Voucher{ID: 4, Title: "Belum mulai", Status: domain.VoucherActive, DiscountType: domain.DiscountAmount, Value: 1000, StartsAt: &soon}),
				VoucherDoc(domain.Voucher{ID: 5, Title: "Sudah usai", Status: domain.VoucherActive, DiscountType: domain.DiscountAmount, Value: 2000, EndsAt: &gone}),
			}, nil
		}
		return nil, nil
	}
	if err := ix.Rebuild(ctx, src); err != nil {
		t.Fatal(err)
	}
	// Rebuilding twice, with the alias already live, must work too.
	if err := ix.Rebuild(ctx, src, Outlets); err != nil {
		t.Fatal(err)
	}
	// Writes are visible right away (read-your-writes).
	if err := ix.Put(ctx, Outlets, OutletDoc(domain.Outlet{ID: 3, Name: "Zeta Cuci", Address: "Jl. Z", Phone: "0213", Active: true})); err != nil {
		t.Fatal(err)
	}

	ids := func(k Kind, q search.Query) []int64 {
		t.Helper()
		res, err := ix.Search(ctx, k, q)
		if err != nil {
			t.Fatal(err)
		}
		return res.IDs
	}

	// Sorting by name is case-insensitive and by id numeric (10 after 2), the id breaks ties.
	if got := ids(Outlets, OutletQuery(domain.OutletQuery{SortBy: "name", SortDir: "asc", Limit: 10})); !reflect.DeepEqual(got, []int64{10, 1, 2, 3}) {
		t.Errorf("by name = %v", got)
	}
	if got := ids(Outlets, OutletQuery(domain.OutletQuery{SortBy: "id", SortDir: "desc", Limit: 10})); !reflect.DeepEqual(got, []int64{10, 3, 2, 1}) {
		t.Errorf("by id desc = %v", got)
	}
	// Paging.
	if got := ids(Outlets, OutletQuery(domain.OutletQuery{SortBy: "id", Limit: 2, Offset: 2})); !reflect.DeepEqual(got, []int64{3, 10}) {
		t.Errorf("page 2 = %v", got)
	}
	// Filter and total.
	res, err := ix.Search(ctx, Outlets, OutletQuery(domain.OutletQuery{Active: "false", SortBy: "id", Limit: 10}))
	if err != nil || res.Total != 1 || res.IDs[0] != 2 {
		t.Errorf("inactive = %+v, %v", res, err)
	}
	// Search inside a word, ignoring case.
	if got := ids(Outlets, OutletQuery(domain.OutletQuery{Q: "kema", SortBy: "id", Limit: 10})); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("infix kema = %v", got)
	}
	if got := ids(Outlets, OutletQuery(domain.OutletQuery{Q: "WUZZ", SortBy: "id", Limit: 10})); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Errorf("wuzz = %v", got)
	}
	// Services: by category name, filter by unit, sort by price desc.
	if got := ids(Services, ServiceQuery(domain.ServiceQuery{Q: "sepatu", SortBy: "id", Limit: 10})); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("service by category name = %v", got)
	}
	if got := ids(Services, ServiceQuery(domain.ServiceQuery{SortBy: "price", SortDir: "desc", Limit: 10})); !reflect.DeepEqual(got, []int64{2, 1}) {
		t.Errorf("service by price = %v", got)
	}
	if got := ids(Services, ServiceQuery(domain.ServiceQuery{Unit: "kg", Limit: 10, SortBy: "id"})); !reflect.DeepEqual(got, []int64{1}) {
		t.Errorf("unit kg = %v", got)
	}
	// Groups: used / unused / service / requirement, and a search through the option names.
	if got := ids(VariantGroups, GroupQuery(domain.GroupQuery{Usage: "used", SortBy: "id", Limit: 10})); !reflect.DeepEqual(got, []int64{1}) {
		t.Errorf("used = %v", got)
	}
	if got := ids(VariantGroups, GroupQuery(domain.GroupQuery{Usage: "unused", SortBy: "id", Limit: 10})); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("unused = %v", got)
	}
	if got := ids(ModifierGroups, GroupQuery(domain.GroupQuery{ServiceID: 2, SortBy: "id", Limit: 10})); !reflect.DeepEqual(got, []int64{1}) {
		t.Errorf("service 2 = %v", got)
	}
	if got := ids(ModifierGroups, GroupQuery(domain.GroupQuery{Requirement: "required", SortBy: "id", Limit: 10})); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("required = %v", got)
	}
	if got := ids(VariantGroups, GroupQuery(domain.GroupQuery{Q: "lavend", SortBy: "id", Limit: 10})); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("option name = %v", got)
	}
	// Banners: the carousel's order, and "live" judged by the clock of the request, not by anything stored.
	now := time.Now()
	if got := ids(Banners, BannerQuery(domain.BannerQuery{SortBy: "sort_index", Limit: 10, Now: now})); !reflect.DeepEqual(got, []int64{2, 1, 3, 4, 5}) {
		t.Errorf("banners in order = %v", got)
	}
	if got := ids(Banners, BannerQuery(domain.BannerQuery{Live: "true", SortBy: "sort_index", Limit: 10, Now: now})); !reflect.DeepEqual(got, []int64{2, 1}) {
		t.Errorf("live banners = %v", got)
	}
	if got := ids(Banners, BannerQuery(domain.BannerQuery{Live: "false", SortBy: "sort_index", Limit: 10, Now: now})); !reflect.DeepEqual(got, []int64{3, 4, 5}) {
		t.Errorf("banners not live = %v", got)
	}
	if got := ids(Banners, BannerQuery(domain.BannerQuery{Live: "true", SortBy: "sort_index", Limit: 10, Now: now.Add(2 * time.Hour)})); !reflect.DeepEqual(got, []int64{2, 1, 4}) {
		t.Errorf("live banners two hours on = %v (the one that starts has started, the open ones still run)", got)
	}
	if got := ids(Banners, BannerQuery(domain.BannerQuery{OutletID: 7, SortBy: "id", Limit: 10, Now: now})); !reflect.DeepEqual(got, []int64{1, 2, 3, 4, 5}) {
		t.Errorf("banners for outlet 7 = %v", got)
	}
	if got := ids(Banners, BannerQuery(domain.BannerQuery{OutletID: 8, SortBy: "id", Limit: 10, Now: now})); !reflect.DeepEqual(got, []int64{1, 3, 4, 5}) {
		t.Errorf("banners for outlet 8 = %v (not the one limited to outlet 7)", got)
	}
	if got := ids(Banners, BannerQuery(domain.BannerQuery{Q: "kemang", SortBy: "id", Limit: 10, Now: now})); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("banner by title = %v", got)
	}
	// Vouchers: live and period are judged by the clock of the request; the search finds a code; claims sort.
	voucherIDs := func(q domain.VoucherQuery) []int64 {
		q.Limit, q.Now = 10, now
		if q.SortBy == "" {
			q.SortBy = "id"
		}
		return ids(Vouchers, VoucherQuery(q))
	}
	if got := voucherIDs(domain.VoucherQuery{Live: "true"}); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Errorf("live vouchers = %v", got)
	}
	if got := voucherIDs(domain.VoucherQuery{Live: "false"}); !reflect.DeepEqual(got, []int64{3, 4, 5}) {
		t.Errorf("vouchers not live = %v", got)
	}
	if got := voucherIDs(domain.VoucherQuery{Period: "running"}); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Errorf("running vouchers (the status does not matter) = %v", got)
	}
	if got := voucherIDs(domain.VoucherQuery{Period: "upcoming"}); !reflect.DeepEqual(got, []int64{4}) {
		t.Errorf("upcoming vouchers = %v", got)
	}
	if got := voucherIDs(domain.VoucherQuery{Period: "ended"}); !reflect.DeepEqual(got, []int64{5}) {
		t.Errorf("ended vouchers = %v", got)
	}
	if got := voucherIDs(domain.VoucherQuery{Period: "unlimited"}); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Errorf("unlimited vouchers = %v", got)
	}
	if got := voucherIDs(domain.VoucherQuery{OutletID: 8}); !reflect.DeepEqual(got, []int64{1, 3, 4, 5}) {
		t.Errorf("vouchers for outlet 8 = %v (not the one limited to outlet 7)", got)
	}
	if got := voucherIDs(domain.VoucherQuery{Q: "kemang20"}); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("voucher by code = %v", got)
	}
	if got := voucherIDs(domain.VoucherQuery{DiscountType: "percentage"}); !reflect.DeepEqual(got, []int64{1}) {
		t.Errorf("percentage vouchers = %v", got)
	}
	if got := voucherIDs(domain.VoucherQuery{SortBy: "claims", SortDir: "desc"}); len(got) == 0 || got[0] != 2 {
		t.Errorf("most claimed first = %v", got)
	}
	// A hostile search is only text.
	if _, err := ix.Search(ctx, Outlets, OutletQuery(domain.OutletQuery{Q: "`) || id:>0 (", SortBy: "id", Limit: 10})); err != nil {
		t.Errorf("hostile text: %v", err)
	}
	// Remove and reconcile.
	if err := ix.Remove(ctx, Outlets, 3); err != nil {
		t.Fatal(err)
	}
	if got := ids(Outlets, OutletQuery(domain.OutletQuery{SortBy: "id", Limit: 10})); !reflect.DeepEqual(got, []int64{10, 2, 1}) && !reflect.DeepEqual(got, []int64{1, 2, 10}) {
		t.Errorf("after remove = %v", got)
	}
	if err := ix.Put(ctx, Outlets, OutletDoc(domain.Outlet{ID: 99, Name: "Ghost"})); err != nil {
		t.Fatal(err)
	}
	if err := ix.Reconcile(ctx, src); err != nil {
		t.Fatal(err)
	}
	if got := ids(Outlets, OutletQuery(domain.OutletQuery{Q: "ghost", SortBy: "id", Limit: 10})); len(got) != 0 {
		t.Errorf("reconcile kept a document with no row: %v", got)
	}
}
