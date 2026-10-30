package searchindex

import (
	"reflect"
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

func TestQuery(t *testing.T) {
	// 2026-10-25 00:00 WIB is 2026-10-24 17:00 UTC.
	from := time.Date(2026, 10, 24, 17, 0, 0, 0, time.UTC).Unix()
	got := Query(domain.ListFilter{
		OutletIDs: []int64{1, 2}, Status: "ready", Query: "budi", From: "2026-10-25", To: "2026-10-26",
		SortBy: "total", Desc: true, Limit: 10, Offset: 30,
	})
	wantFilters := []string{"outlet_id:=[1,2]", "status:=`ready`", "created_at:>=" + itoa(from), "created_at:<=" + itoa(from+2*86400-1)}
	if !reflect.DeepEqual(got.Filters, wantFilters) {
		t.Fatalf("filters = %v, want %v", got.Filters, wantFilters)
	}
	if !reflect.DeepEqual(got.Sort, []string{"total:desc", "created_at:desc", "uid:asc"}) {
		t.Fatalf("sort = %v", got.Sort)
	}
	if got.Q != "budi" || got.Limit != 10 || got.Offset != 30 || !reflect.DeepEqual(got.QueryBy, []string{"code", "customer_name", "customer_phone", "uid"}) {
		t.Fatalf("query = %+v", got)
	}
}

func TestQueryDefaults(t *testing.T) {
	got := Query(domain.ListFilter{Limit: 25})
	if got.Filters != nil {
		t.Fatalf("filters = %v", got.Filters)
	}
	if !reflect.DeepEqual(got.Sort, []string{"created_at:asc", "uid:asc"}) {
		t.Fatalf("sort = %v", got.Sort)
	}
}

func TestQueryStatusIsQuoted(t *testing.T) {
	got := Query(domain.ListFilter{Status: "x` || outlet_id:>0 || `", Limit: 1})
	if got.Filters[0] != "status:=`x || outlet_id:>0 || `" {
		t.Fatalf("filter = %q", got.Filters[0])
	}
}

func TestEverySortFieldIsMapped(t *testing.T) {
	for f := range domain.SortFields {
		if sortField[f] == "" {
			t.Errorf("order sort %q has no index field", f)
		}
	}
}

func TestDoc(t *testing.T) {
	d := Doc(domain.Order{ID: "u-1", Code: "TRX-25102026-ABCD", CustomerName: "Budi", OutletID: 2, Total: 50000, Status: "created", CreatedAt: time.Unix(1000, 0)})
	if d["id"] != "u-1" || d["customer_sort"] != "budi" || d["created_at"] != int64(1000) {
		t.Fatalf("doc = %v", d)
	}
}

func itoa(n int64) string { return fmtInt(n) }

func fmtInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
