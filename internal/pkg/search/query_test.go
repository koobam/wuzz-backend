package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

func TestFilters(t *testing.T) {
	tests := []struct {
		name, got, want string
	}{
		{"text", Eq("unit", "kg"), "unit:=`kg`"},
		{"a backtick cannot break out of the quotes", Eq("name", "a` || id:>0 || `b"), "name:=`a || id:>0 || b`"},
		{"line breaks are dropped", Eq("name", "a\nb\rc"), "name:=`abc`"},
		{"filter syntax in a value stays inside the quotes", Eq("status", "x && outlet_id:>0"), "status:=`x && outlet_id:>0`"},
		{"int", EqInt("category_id", 3), "category_id:=3"},
		{"bool", EqBool("active", true), "active:=true"},
		{"range", Gte("created_at", 10) + " && " + Lte("created_at", 20), "created_at:>=10 && created_at:<=20"},
		{"in", InInt("outlet_id", []int64{1, 2, 3}), "outlet_id:=[1,2,3]"},
		{"sort asc", Sort("name_sort", ""), "name_sort:asc"},
		{"sort desc", Sort("name_sort", "desc"), "name_sort:desc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// node answers like Typesense and records the request it was asked.
func node(t *testing.T, status int, body string, seen *http.Request) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "key")
}

func TestSearchRequest(t *testing.T) {
	var seen http.Request
	c := node(t, 200, `{"found": 42, "hits": [{"document": {"id": "7"}}, {"document": {"id": "3"}}]}`, &seen)
	res, err := c.Search(context.Background(), "catalog_outlets", Query{
		Q: "kilo", QueryBy: []string{"name", "address"}, Filters: []string{"active:=true", "unit:=`kg`"},
		Sort: []string{"name_sort:asc", "idn:asc"}, Limit: 10, Offset: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 42 || len(res.IDs) != 2 || res.IDs[0] != 7 || res.IDs[1] != 3 {
		t.Fatalf("result = %+v", res)
	}
	q := seen.URL.Query()
	want := map[string]string{
		"q": "kilo", "query_by": "name,address", "infix": "always,always", "filter_by": "active:=true && unit:=`kg`",
		"sort_by": "name_sort:asc,idn:asc", "limit": "10", "offset": "20", "include_fields": "id",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if seen.Header.Get("X-TYPESENSE-API-KEY") != "key" {
		t.Error("the API key was not sent")
	}
	if !strings.HasSuffix(seen.URL.Path, "/collections/catalog_outlets/documents/search") {
		t.Errorf("path = %s", seen.URL.Path)
	}
}

func TestSearchEmptyTextMatchesEverything(t *testing.T) {
	var seen http.Request
	c := node(t, 200, `{"found": 0, "hits": []}`, &seen)
	if _, err := c.Search(context.Background(), "x", Query{Q: "  ", QueryBy: []string{"name"}, Limit: 5}); err != nil {
		t.Fatal(err)
	}
	q := seen.URL.Query()
	if q.Get("q") != "*" || q.Get("query_by") != "" {
		t.Fatalf("q=%q query_by=%q", q.Get("q"), q.Get("query_by"))
	}
}

func TestSearchFailureIsUnavailable(t *testing.T) {
	var seen http.Request
	for _, status := range []int{500, 503, 400} {
		c := node(t, status, `{"message":"nope"}`, &seen)
		_, err := c.Search(context.Background(), "x", Query{Limit: 1})
		if !apperr.IsKind(err, apperr.KindUnavailable) {
			t.Errorf("status %d: err = %v, want an Unavailable error", status, err)
		}
	}
	// A node that is not there at all.
	c := New("http://127.0.0.1:1", "k")
	if _, err := c.Search(context.Background(), "x", Query{Limit: 1}); !apperr.IsKind(err, apperr.KindUnavailable) {
		t.Errorf("unreachable node: err = %v", err)
	}
}

func TestImportReportsTheFirstRefusal(t *testing.T) {
	var seen http.Request
	c := node(t, 200, `{"success":true}`+"\n"+`{"success":false,"error":"Field `+"`idn`"+` must be an int64."}`, &seen)
	err := c.Import(context.Background(), "x", []map[string]any{{"id": "1"}, {"id": "2"}})
	if err == nil || !strings.Contains(err.Error(), "must be an int64") {
		t.Fatalf("err = %v", err)
	}
	if got := seen.URL.Query().Get("action"); got != "upsert" {
		t.Errorf("action = %q", got)
	}
}

func TestNewWithoutURLIsNil(t *testing.T) {
	if New("  ", "k").Enabled() {
		t.Fatal("an empty URL must mean search is not configured")
	}
}

func TestExportIDs(t *testing.T) {
	var seen http.Request
	c := node(t, 200, `{"id":"1"}`+"\n"+`{"id":"2"}`+"\n", &seen)
	ids, err := c.ExportIDs(context.Background(), "x")
	if err != nil || len(ids) != 2 || ids[1] != "2" {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}
