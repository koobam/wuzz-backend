package searchindex

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/koobam/wuzz-backend/internal/pkg/search"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

func lower(s string) string { return strings.ToLower(s) }

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// OutletDoc is an outlet as the index holds it.
func OutletDoc(o domain.Outlet) map[string]any {
	return map[string]any{
		"id": fmt.Sprint(o.ID), "idn": o.ID,
		"name": o.Name, "address": o.Address, "phone": o.Phone, "email": o.Email,
		"name_sort": lower(o.Name), "address_sort": lower(o.Address), "phone_sort": lower(o.Phone),
		"active": o.Active, "active_n": b2i(o.Active),
	}
}

// ServiceDoc is a service as the index holds it; categoryName is the name of its category ("" = none), so a search finds a service by it.
func ServiceDoc(s domain.ServiceItem, categoryName string) map[string]any {
	return map[string]any{
		"id": fmt.Sprint(s.ID), "idn": s.ID,
		"name": s.Name, "description": s.Description, "internal_notes": s.InternalNotes, "category_name": categoryName,
		"name_sort": lower(s.Name), "unit": s.Unit, "price": s.Price, "category_id": s.CategoryID, "tax_type": s.TaxType, "discount_type": s.DiscountType,
		"active": s.Active, "active_n": b2i(s.Active),
	}
}

// MaterialDoc is a material as the index holds it.
func MaterialDoc(m domain.Material) map[string]any {
	return map[string]any{
		"id": fmt.Sprint(m.ID), "idn": m.ID,
		"sku": m.SKU, "name": m.Name, "notes": m.Notes,
		"sku_sort": lower(m.SKU), "name_sort": lower(m.Name),
		"category": m.Category, "active": m.Active,
		"created_at": m.CreatedAt.Unix(), "updated_at": m.UpdatedAt.Unix(),
	}
}

// Group is what a variant or a modifier group needs to be indexed.
type Group struct {
	ID            int64
	Name, Notes   string
	OptionNames   []string
	ServiceIDs    []int64
	Min           int32 // modifier groups only
	Created, Edit int64 // unix seconds
}

// GroupDoc is a group as the index holds it. Services is empty for a group no service uses ("used" is false).
func GroupDoc(g Group) map[string]any {
	ids := g.ServiceIDs
	if ids == nil {
		ids = []int64{}
	}
	names := g.OptionNames
	if names == nil {
		names = []string{}
	}
	return map[string]any{
		"id": fmt.Sprint(g.ID), "idn": g.ID,
		"name": g.Name, "notes": g.Notes, "option_names": names, "name_sort": lower(g.Name),
		"service_ids": ids, "used": len(ids) > 0, "min": int64(g.Min),
		"created_at": g.Created, "updated_at": g.Edit,
	}
}

// ---- queries: how a list's parameters become a search ----

// withID ends a sort with the id, so the pages of a list never overlap or repeat a row.
func withID(primary string, dir string) []string {
	if primary == "idn" {
		return []string{search.Sort("idn", dir)}
	}
	return []string{search.Sort(primary, dir), "idn:asc"}
}

func triState(field, v string) []string {
	switch v {
	case "true":
		return []string{search.EqBool(field, true)}
	case "false":
		return []string{search.EqBool(field, false)}
	}
	return nil
}

var outletSort = map[string]string{"id": "idn", "name": "name_sort", "address": "address_sort", "phone": "phone_sort", "active": "active_n"}

// OutletQuery turns an outlet list's parameters into a search.
func OutletQuery(q domain.OutletQuery) search.Query {
	return search.Query{
		Q: q.Q, QueryBy: []string{"name", "address", "phone", "email"},
		Filters: triState("active", q.Active),
		Sort:    withID(outletSort[q.SortBy], q.SortDir),
		Limit:   q.Limit, Offset: q.Offset,
	}
}

var serviceSort = map[string]string{"id": "idn", "name": "name_sort", "unit": "unit", "price": "price", "category_id": "category_id", "active": "active_n"}

// ServiceQuery turns a service list's parameters into a search.
func ServiceQuery(q domain.ServiceQuery) search.Query {
	f := triState("active", q.Active)
	if q.CategoryID > 0 {
		f = append(f, search.EqInt("category_id", q.CategoryID))
	}
	if q.Unit != "" {
		f = append(f, search.Eq("unit", q.Unit))
	}
	if q.TaxType != "" {
		f = append(f, search.Eq("tax_type", q.TaxType))
	}
	if q.DiscountType != "" {
		f = append(f, search.Eq("discount_type", q.DiscountType))
	}
	return search.Query{
		Q: q.Q, QueryBy: []string{"name", "description", "internal_notes", "category_name"},
		Filters: f,
		Sort:    withID(serviceSort[q.SortBy], q.SortDir),
		Limit:   q.Limit, Offset: q.Offset,
	}
}

var materialSort = map[string]string{"id": "idn", "sku": "sku_sort", "name": "name_sort", "created_at": "created_at", "updated_at": "updated_at"}

// MaterialQuery turns a material list's parameters into a search.
func MaterialQuery(q domain.MaterialQuery) search.Query {
	f := triState("active", q.Active)
	if q.Category != "" {
		f = append(f, search.Eq("category", q.Category))
	}
	return search.Query{
		Q: q.Q, QueryBy: []string{"sku", "name", "notes"},
		Filters: f,
		Sort:    withID(materialSort[q.SortBy], q.SortDir),
		Limit:   q.Limit, Offset: q.Offset,
	}
}

var groupSort = map[string]string{"id": "idn", "name": "name_sort", "created_at": "created_at", "updated_at": "updated_at"}

// GroupQuery turns a variant or modifier group list's parameters into a search.
func GroupQuery(q domain.GroupQuery) search.Query {
	var f []string
	if q.ServiceID > 0 {
		f = append(f, search.EqInt("service_ids", q.ServiceID))
	}
	switch q.Usage {
	case "used":
		f = append(f, search.EqBool("used", true))
	case "unused":
		f = append(f, search.EqBool("used", false))
	}
	switch q.Requirement {
	case "required":
		f = append(f, search.Gte("min", 1))
	case "optional":
		f = append(f, search.Lte("min", 0))
	}
	return search.Query{
		Q: q.Q, QueryBy: []string{"name", "notes", "option_names"},
		Filters: f,
		Sort:    withID(groupSort[q.SortBy], q.SortDir),
		Limit:   q.Limit, Offset: q.Offset,
	}
}

// BannerDoc is a banner as the index holds it. An absent start is 0 and an absent end is far away, and a banner with no outlet limit
// lists the outlet 0, so "applies to outlet 7" is one membership test: outlet_ids is 0 or 7.
func BannerDoc(b domain.Banner) map[string]any {
	ids := b.OutletIDs
	if len(ids) == 0 {
		ids = []int64{0}
	}
	return map[string]any{
		"id": fmt.Sprint(b.ID), "idn": b.ID,
		"title": b.Title, "internal_notes": b.InternalNotes, "title_sort": lower(b.Title),
		"sort_n": int64(b.SortIndex), "status": b.Status, "active_n": b2i(b.Status == domain.BannerActive), "target_type": b.TargetType,
		"starts_n": b.StartUnix(), "ends_n": b.EndUnix(), "outlet_ids": ids,
		"created_at": b.CreatedAt.Unix(), "updated_at": b.UpdatedAt.Unix(),
	}
}

var bannerSort = map[string]string{"id": "idn", "title": "title_sort", "status": "active_n", "sort_index": "sort_n", "starts_at": "starts_n", "ends_at": "ends_n",
	"created_at": "created_at", "updated_at": "updated_at"}

// BannerQuery turns a banner list's parameters into a search. Live is judged by q.Now: a live banner is active and has started and not
// ended; a banner that is not live is any of the three failing.
func BannerQuery(q domain.BannerQuery) search.Query {
	f := []string{}
	if q.Status != "" {
		f = append(f, search.Eq("status", q.Status))
	}
	if q.OutletID > 0 {
		f = append(f, search.InInt("outlet_ids", []int64{0, q.OutletID}))
	}
	now := q.Now.Unix()
	switch q.Live {
	case "true":
		f = append(f, search.EqInt("active_n", 1), search.Lte("starts_n", now), "ends_n:>"+strconv.FormatInt(now, 10))
	case "false":
		f = append(f, fmt.Sprintf("(active_n:=0 || starts_n:>%d || ends_n:<=%d)", now, now))
	}
	return search.Query{
		Q: q.Q, QueryBy: []string{"title", "internal_notes"},
		Filters: f,
		Sort:    withID(bannerSort[q.SortBy], q.SortDir),
		Limit:   q.Limit, Offset: q.Offset,
	}
}

// VoucherDoc is a voucher as the index holds it. Like a banner, an absent start is 0 and an absent end is far away, and a voucher with no
// outlet limit lists the outlet 0, so "can be spent at outlet 7" is one membership test: outlet_ids is 0 or 7.
func VoucherDoc(v domain.Voucher) map[string]any {
	ids := v.OutletIDs
	if len(ids) == 0 {
		ids = []int64{0}
	}
	return map[string]any{
		"id": fmt.Sprint(v.ID), "idn": v.ID,
		"title": v.Title, "code": v.Code, "description": v.Description, "internal_notes": v.InternalNotes,
		"title_sort": lower(v.Title), "code_sort": v.Code, "status": v.Status, "active_n": b2i(v.Status == domain.VoucherActive),
		"discount_type": v.DiscountType, "type": v.Type(), "value_n": v.Value, "claims_n": v.ClaimsCount,
		"starts_n": v.StartUnix(), "ends_n": v.EndUnix(), "outlet_ids": ids,
		"created_at": v.CreatedAt.Unix(), "updated_at": v.UpdatedAt.Unix(),
	}
}

var voucherSort = map[string]string{"id": "idn", "title": "title_sort", "code": "code_sort", "status": "active_n", "value": "value_n", "claims": "claims_n",
	"starts_at": "starts_n", "ends_at": "ends_n", "created_at": "created_at", "updated_at": "updated_at"}

// VoucherQuery turns a voucher list's parameters into a search. Live and Period are judged by q.Now: live is active and inside the window;
// running is inside the window whatever the status, upcoming has not opened, ended has closed, and unlimited has neither end.
func VoucherQuery(q domain.VoucherQuery) search.Query {
	f := []string{}
	if q.Status != "" {
		f = append(f, search.Eq("status", q.Status))
	}
	if q.DiscountType != "" {
		f = append(f, search.Eq("discount_type", q.DiscountType))
	}
	if q.OutletID > 0 {
		f = append(f, search.InInt("outlet_ids", []int64{0, q.OutletID}))
	}
	now := q.Now.Unix()
	switch q.Live {
	case "true":
		f = append(f, search.EqInt("active_n", 1), search.Lte("starts_n", now), "ends_n:>"+strconv.FormatInt(now, 10))
	case "false":
		f = append(f, fmt.Sprintf("(active_n:=0 || starts_n:>%d || ends_n:<=%d)", now, now))
	}
	switch q.Period {
	case "running":
		f = append(f, search.Lte("starts_n", now), "ends_n:>"+strconv.FormatInt(now, 10))
	case "upcoming":
		f = append(f, "starts_n:>"+strconv.FormatInt(now, 10))
	case "ended":
		f = append(f, search.Lte("ends_n", now))
	case "unlimited":
		f = append(f, search.EqInt("starts_n", 0), search.EqInt("ends_n", 253402300799))
	}
	return search.Query{
		Q: q.Q, QueryBy: []string{"title", "code", "description", "internal_notes"},
		Filters: f,
		Sort:    withID(voucherSort[q.SortBy], q.SortDir),
		Limit:   q.Limit, Offset: q.Offset,
	}
}
