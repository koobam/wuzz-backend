package searchindex

import (
	"reflect"
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

func TestOutletQuery(t *testing.T) {
	got := OutletQuery(domain.OutletQuery{Q: "bsd", Active: "true", SortBy: "name", SortDir: "desc", Limit: 10, Offset: 20})
	want := struct {
		filters, sort []string
	}{[]string{"active:=true"}, []string{"name_sort:desc", "idn:asc"}}
	if !reflect.DeepEqual(got.Filters, want.filters) || !reflect.DeepEqual(got.Sort, want.sort) || got.Limit != 10 || got.Offset != 20 || got.Q != "bsd" {
		t.Fatalf("got %+v", got)
	}
	if !reflect.DeepEqual(got.QueryBy, []string{"name", "address", "phone", "email"}) {
		t.Fatalf("query_by = %v", got.QueryBy)
	}
}

func TestSortByID(t *testing.T) {
	// Sorting by id must not repeat the id as its own tie-break.
	got := OutletQuery(domain.OutletQuery{SortBy: "id", SortDir: "desc", Limit: 5})
	if !reflect.DeepEqual(got.Sort, []string{"idn:desc"}) {
		t.Fatalf("sort = %v", got.Sort)
	}
}

func TestServiceQuery(t *testing.T) {
	tests := []struct {
		name string
		in   domain.ServiceQuery
		want []string
	}{
		{"nothing", domain.ServiceQuery{SortBy: "id", Limit: 10}, nil},
		{"all", domain.ServiceQuery{CategoryID: 4, Unit: "kg", Active: "false", SortBy: "price", Limit: 10}, []string{"active:=false", "category_id:=4", "unit:=`kg`"}},
		{"tax type", domain.ServiceQuery{TaxType: "ppn", SortBy: "id", Limit: 10}, []string{"tax_type:=`ppn`"}},
		{"discount type", domain.ServiceQuery{DiscountType: "percent", SortBy: "id", Limit: 10}, []string{"discount_type:=`percent`"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ServiceQuery(tc.in).Filters; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("filters = %v, want %v", got, tc.want)
			}
		})
	}
	if got := ServiceQuery(domain.ServiceQuery{SortBy: "category_id", Limit: 1}).Sort; !reflect.DeepEqual(got, []string{"category_id:asc", "idn:asc"}) {
		t.Fatalf("sort = %v", got)
	}
}

func TestMaterialQuery(t *testing.T) {
	got := MaterialQuery(domain.MaterialQuery{Q: "deterjen", Category: "chemical", Active: "true", SortBy: "sku", SortDir: "asc", Limit: 10})
	if !reflect.DeepEqual(got.Filters, []string{"active:=true", "category:=`chemical`"}) {
		t.Fatalf("filters = %v", got.Filters)
	}
	if !reflect.DeepEqual(got.Sort, []string{"sku_sort:asc", "idn:asc"}) {
		t.Fatalf("sort = %v", got.Sort)
	}
}

func TestGroupQuery(t *testing.T) {
	tests := []struct {
		name string
		in   domain.GroupQuery
		want []string
	}{
		{"nothing", domain.GroupQuery{SortBy: "id", Limit: 10}, nil},
		{"service", domain.GroupQuery{ServiceID: 9, SortBy: "id", Limit: 10}, []string{"service_ids:=9"}},
		{"used", domain.GroupQuery{Usage: "used", SortBy: "id", Limit: 10}, []string{"used:=true"}},
		{"unused", domain.GroupQuery{Usage: "unused", SortBy: "id", Limit: 10}, []string{"used:=false"}},
		{"required", domain.GroupQuery{Requirement: "required", SortBy: "id", Limit: 10}, []string{"min:>=1"}},
		{"optional", domain.GroupQuery{Requirement: "optional", SortBy: "id", Limit: 10}, []string{"min:<=0"}},
		{"everything", domain.GroupQuery{ServiceID: 2, Usage: "used", Requirement: "required", SortBy: "id", Limit: 10}, []string{"service_ids:=2", "used:=true", "min:>=1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := GroupQuery(tc.in).Filters; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("filters = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGroupDocNeverHasNullLists(t *testing.T) {
	d := GroupDoc(Group{ID: 5, Name: "Ukuran"})
	if ids, ok := d["service_ids"].([]int64); !ok || ids == nil {
		t.Fatalf("service_ids = %#v", d["service_ids"])
	}
	if d["used"] != false || d["id"] != "5" {
		t.Fatalf("doc = %v", d)
	}
	if d := GroupDoc(Group{ID: 5, ServiceIDs: []int64{1}}); d["used"] != true {
		t.Fatal("a group on a service is used")
	}
}

func TestSortNamesAreAllMapped(t *testing.T) {
	for f := range domain.OutletSortFields {
		if outletSort[f] == "" {
			t.Errorf("outlet sort %q has no index field", f)
		}
	}
	for f := range domain.ServiceSortFields {
		if serviceSort[f] == "" {
			t.Errorf("service sort %q has no index field", f)
		}
	}
	for f := range domain.MaterialSortFields {
		if materialSort[f] == "" {
			t.Errorf("material sort %q has no index field", f)
		}
	}
	for f := range domain.GroupSortFields {
		if groupSort[f] == "" {
			t.Errorf("group sort %q has no index field", f)
		}
	}
}

func TestBannerDoc(t *testing.T) {
	at := func(s string) *time.Time { v, _ := time.Parse(time.RFC3339, s); return &v }
	open := BannerDoc(domain.Banner{ID: 4, Title: "Promo Oktober", Status: domain.BannerActive})
	if open["starts_n"] != int64(0) || open["ends_n"] != int64(253402300799) || open["active_n"] != int64(1) {
		t.Errorf("an open window = %v..%v, active_n %v", open["starts_n"], open["ends_n"], open["active_n"])
	}
	if !reflect.DeepEqual(open["outlet_ids"], []int64{0}) {
		t.Errorf("a banner for every outlet lists outlet %v, want [0]", open["outlet_ids"])
	}
	if open["title_sort"] != "promo oktober" {
		t.Errorf("title_sort = %v", open["title_sort"])
	}
	limited := BannerDoc(domain.Banner{ID: 5, OutletIDs: []int64{3, 7}, StartsAt: at("2026-10-28T12:00:00Z")})
	if !reflect.DeepEqual(limited["outlet_ids"], []int64{3, 7}) || limited["starts_n"] != int64(1793188800) || limited["active_n"] != int64(0) {
		t.Errorf("limited = %v", limited)
	}
}

func TestBannerQuery(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) // 1793188800
	tests := []struct {
		name string
		in   domain.BannerQuery
		want []string
	}{
		{"nothing", domain.BannerQuery{SortBy: "sort_index", Limit: 10, Now: now}, []string{}},
		{"status", domain.BannerQuery{Status: "active", SortBy: "id", Limit: 10, Now: now}, []string{"status:=`active`"}},
		{"an outlet also gets the banners for every outlet", domain.BannerQuery{OutletID: 7, SortBy: "id", Limit: 10, Now: now}, []string{"outlet_ids:=[0,7]"}},
		{"live", domain.BannerQuery{Live: "true", SortBy: "id", Limit: 10, Now: now}, []string{"active_n:=1", "starts_n:<=1793188800", "ends_n:>1793188800"}},
		{"not live", domain.BannerQuery{Live: "false", SortBy: "id", Limit: 10, Now: now}, []string{"(active_n:=0 || starts_n:>1793188800 || ends_n:<=1793188800)"}},
	}
	for _, c := range tests {
		got := BannerQuery(c.in)
		if !reflect.DeepEqual(got.Filters, c.want) {
			t.Errorf("%s: filters = %v, want %v", c.name, got.Filters, c.want)
		}
	}
	got := BannerQuery(domain.BannerQuery{SortBy: "starts_at", SortDir: "desc", Limit: 10, Now: now})
	if !reflect.DeepEqual(got.Sort, []string{"starts_n:desc", "idn:asc"}) || !reflect.DeepEqual(got.QueryBy, []string{"title", "internal_notes"}) {
		t.Errorf("sort = %v, query_by = %v", got.Sort, got.QueryBy)
	}
	if got := BannerQuery(domain.BannerQuery{SortBy: "sort_index", Limit: 10, Now: now}); !reflect.DeepEqual(got.Sort, []string{"sort_n:asc", "idn:asc"}) {
		t.Errorf("the carousel's order = %v", got.Sort)
	}
}

func TestVoucherDoc(t *testing.T) {
	at := func(s string) *time.Time { v, _ := time.Parse(time.RFC3339, s); return &v }
	open := VoucherDoc(domain.Voucher{ID: 4, Code: "WELCOME", Title: "Selamat Datang", Status: domain.VoucherActive, DiscountType: domain.DiscountAmount, Value: 15000, ClaimsCount: 3})
	if open["starts_n"] != int64(0) || open["ends_n"] != int64(253402300799) || open["active_n"] != int64(1) || open["claims_n"] != int64(3) || open["value_n"] != int64(15000) {
		t.Errorf("an open window = %v", open)
	}
	if !reflect.DeepEqual(open["outlet_ids"], []int64{0}) || open["type"] != "code" || open["code_sort"] != "WELCOME" || open["title_sort"] != "selamat datang" {
		t.Errorf("doc = %v", open)
	}
	public := VoucherDoc(domain.Voucher{ID: 5, OutletIDs: []int64{3, 7}, StartsAt: at("2026-10-28T12:00:00Z")})
	if !reflect.DeepEqual(public["outlet_ids"], []int64{3, 7}) || public["starts_n"] != int64(1793188800) || public["active_n"] != int64(0) || public["type"] != "public" {
		t.Errorf("limited = %v", public)
	}
}

func TestVoucherQuery(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) // 1793188800
	tests := []struct {
		name string
		in   domain.VoucherQuery
		want []string
	}{
		{"nothing", domain.VoucherQuery{SortBy: "id", Limit: 10, Now: now}, []string{}},
		{"status and kind", domain.VoucherQuery{Status: "active", DiscountType: "amount", SortBy: "id", Limit: 10, Now: now}, []string{"status:=`active`", "discount_type:=`amount`"}},
		{"an outlet also gets the vouchers for every outlet", domain.VoucherQuery{OutletID: 7, SortBy: "id", Limit: 10, Now: now}, []string{"outlet_ids:=[0,7]"}},
		{"live", domain.VoucherQuery{Live: "true", SortBy: "id", Limit: 10, Now: now}, []string{"active_n:=1", "starts_n:<=1793188800", "ends_n:>1793188800"}},
		{"not live", domain.VoucherQuery{Live: "false", SortBy: "id", Limit: 10, Now: now}, []string{"(active_n:=0 || starts_n:>1793188800 || ends_n:<=1793188800)"}},
		{"running", domain.VoucherQuery{Period: "running", SortBy: "id", Limit: 10, Now: now}, []string{"starts_n:<=1793188800", "ends_n:>1793188800"}},
		{"upcoming", domain.VoucherQuery{Period: "upcoming", SortBy: "id", Limit: 10, Now: now}, []string{"starts_n:>1793188800"}},
		{"ended", domain.VoucherQuery{Period: "ended", SortBy: "id", Limit: 10, Now: now}, []string{"ends_n:<=1793188800"}},
		{"unlimited", domain.VoucherQuery{Period: "unlimited", SortBy: "id", Limit: 10, Now: now}, []string{"starts_n:=0", "ends_n:=253402300799"}},
	}
	for _, c := range tests {
		got := VoucherQuery(c.in)
		if !reflect.DeepEqual(got.Filters, c.want) {
			t.Errorf("%s: filters = %v, want %v", c.name, got.Filters, c.want)
		}
	}
	got := VoucherQuery(domain.VoucherQuery{SortBy: "claims", SortDir: "desc", Limit: 10, Now: now})
	if !reflect.DeepEqual(got.Sort, []string{"claims_n:desc", "idn:asc"}) || !reflect.DeepEqual(got.QueryBy, []string{"title", "code", "description", "internal_notes"}) {
		t.Errorf("sort = %v, query_by = %v", got.Sort, got.QueryBy)
	}
	for by := range domain.VoucherSortFields {
		if voucherSort[by] == "" {
			t.Errorf("sort field %q has no index field", by)
		}
	}
}
