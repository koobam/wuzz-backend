// Package searchindex keeps the Typesense collection of orders: a derived copy made for searching, sorting and filtering the order
// list. A search returns only ids and the total; the orders themselves are always read back from Postgres.
package searchindex

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/search"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// SchemaVersion is bumped whenever the collection's fields change; on start an alias that points at an older version is rebuilt.
const SchemaVersion = 1

// jakarta is Asia/Jakarta (UTC+7, no daylight saving): the calendar the order filters' days are in.
var jakarta = time.FixedZone("WIB", 7*60*60)

func sortable() *bool { t := true; return &t }

var fields = []search.Field{
	{Name: "code", Type: "string", Infix: true, Sort: sortable()},
	{Name: "customer_name", Type: "string", Infix: true},
	{Name: "customer_phone", Type: "string", Infix: true},
	// uid is the order's id as text: a person can type the start of it (an order link) as well as the code.
	{Name: "uid", Type: "string", Infix: true, Sort: sortable()},
	{Name: "customer_sort", Type: "string", Sort: sortable()},
	{Name: "status", Type: "string", Sort: sortable()},
	{Name: "outlet_id", Type: "int64", Sort: sortable()},
	{Name: "total", Type: "int64", Sort: sortable()},
	{Name: "created_at", Type: "int64", Sort: sortable()},
}

// Index is the orders index. A nil *Index means search is not configured, and its methods must not be called; use Enabled.
type Index struct{ col *search.Collection }

// New returns the index over c, or nil when c is nil (no Typesense configured).
func New(c *search.Client, log *slog.Logger) *Index {
	if !c.Enabled() {
		return nil
	}
	return &Index{col: search.NewCollection(c, log, "transaction_orders", SchemaVersion, fields)}
}

func (ix *Index) Enabled() bool { return ix != nil }

// Doc is an order as the index holds it.
func Doc(o domain.Order) map[string]any {
	return map[string]any{
		"id": o.ID, "uid": o.ID,
		"code": o.Code, "customer_name": o.CustomerName, "customer_phone": o.CustomerPhone,
		"customer_sort": strings.ToLower(o.CustomerName),
		"status":        o.Status, "outlet_id": o.OutletID, "total": o.Total, "created_at": o.CreatedAt.Unix(),
	}
}

func (ix *Index) Put(ctx context.Context, o domain.Order) error { return ix.col.Put(ctx, Doc(o)) }

// Source reads every order from Postgres.
type Source = search.Source

func (ix *Index) Ensure(ctx context.Context, src Source) error    { return ix.col.Ensure(ctx, src) }
func (ix *Index) Rebuild(ctx context.Context, src Source) error   { return ix.col.Rebuild(ctx, src) }
func (ix *Index) Reconcile(ctx context.Context, src Source) error { return ix.col.Reconcile(ctx, src) }

var sortField = map[string]string{
	"code": "code", "created_at": "created_at", "total": "total", "status": "status",
	"customer_name": "customer_sort", "outlet_id": "outlet_id", "id": "uid",
}

// day is the first instant of a calendar day (YYYY-MM-DD) in Asia/Jakarta, as Unix seconds.
func day(s string) (int64, bool) {
	t, err := time.ParseInLocation("2006-01-02", s, jakarta)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

// Query turns an order list's filter into a search. The filter's days are Asia/Jakarta calendar days of created_at, both ends included.
func Query(f domain.ListFilter) search.Query {
	var filters []string
	if len(f.OutletIDs) > 0 {
		filters = append(filters, search.InInt("outlet_id", f.OutletIDs))
	}
	if f.Status != "" {
		filters = append(filters, search.Eq("status", f.Status))
	}
	if from, ok := day(f.From); ok {
		filters = append(filters, search.Gte("created_at", from))
	}
	if to, ok := day(f.To); ok {
		filters = append(filters, search.Lte("created_at", to+24*3600-1))
	}
	dir := "asc"
	if f.Desc {
		dir = "desc"
	}
	primary := sortField[f.SortBy]
	if primary == "" {
		primary = "created_at"
	}
	// Ties: newest first, then id, so the pages of a list never overlap.
	sort := []string{search.Sort(primary, dir)}
	if primary != "created_at" {
		sort = append(sort, "created_at:desc")
	}
	if primary != "uid" {
		sort = append(sort, "uid:asc")
	}
	return search.Query{
		// "trx-" in front of an id is how a link names an order; the code is matched whole, so it is kept as typed.
		Q: f.Query, QueryBy: []string{"code", "customer_name", "customer_phone", "uid"},
		Filters: filters, Sort: sort, Limit: f.Limit, Offset: f.Offset,
	}
}

// Search returns the ids of the page (the orders' ids, in order) and the total. Every id is a UUID string.
func (ix *Index) Search(ctx context.Context, f domain.ListFilter) ([]string, int64, error) {
	return ix.col.SearchStrings(ctx, Query(f))
}

// AllIDs returns the ids of every order the filter matches, for a summary of a text search: it must count the very orders the list shows.
func (ix *Index) AllIDs(ctx context.Context, f domain.ListFilter) ([]string, error) {
	const page = 250
	f.Limit, f.Offset = page, 0
	var all []string
	for {
		ids, total, err := ix.Search(ctx, f)
		if err != nil {
			return nil, err
		}
		all = append(all, ids...)
		f.Offset += page
		if int64(len(all)) >= total || len(ids) == 0 {
			return all, nil
		}
	}
}
