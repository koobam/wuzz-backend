// Package searchindex keeps catalog's Typesense collections: outlets, services, materials, variant groups, modifier groups, banners and vouchers.
// Each is a derived copy of what Postgres holds, made for searching, sorting and filtering a list; a search returns only ids and
// the total, and the rows are always read back from Postgres. Collections are versioned (outlets_v1) behind an alias (catalog_outlets),
// so a change of schema is a rebuild into a new collection and a switch of the alias, never an edit in place.
package searchindex

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/koobam/wuzz-backend/internal/pkg/search"
)

// SchemaVersion is bumped whenever a collection's fields change; on start every collection whose alias points at an older version is
// rebuilt from Postgres.
const SchemaVersion = 6

// Kind names a collection.
type Kind string

const (
	Outlets        Kind = "outlets"
	Services       Kind = "services"
	Materials      Kind = "materials"
	VariantGroups  Kind = "variant_groups"
	ModifierGroups Kind = "modifier_groups"
	Banners        Kind = "banners"
	Vouchers       Kind = "vouchers"
)

// Kinds are every collection, in the order they are built.
var Kinds = []Kind{Outlets, Services, Materials, VariantGroups, ModifierGroups, Banners, Vouchers}

// Index is the catalog's search index. A nil *Index means search is not configured, and its methods must not be called; use Enabled.
type Index struct {
	cols map[Kind]*search.Collection
	log  *slog.Logger
}

// New returns the index over c, or nil when c is nil (no Typesense configured).
func New(c *search.Client, log *slog.Logger) *Index {
	if !c.Enabled() {
		return nil
	}
	ix := &Index{cols: map[Kind]*search.Collection{}, log: log}
	for _, k := range Kinds {
		ix.cols[k] = search.NewCollection(c, log, Alias(k), SchemaVersion, fields(k))
	}
	return ix
}

// Enabled says whether search is configured.
func (ix *Index) Enabled() bool { return ix != nil }

// Alias is the name a kind is read and written through.
func Alias(k Kind) string { return aliasPrefix + string(k) }

// aliasPrefix is a variable only so the integration test can work in collections of its own.
var aliasPrefix = "catalog_"

func sortable() *bool { t := true; return &t }

func text(name string, sortToo bool) search.Field {
	f := search.Field{Name: name, Type: "string", Infix: true}
	if sortToo {
		f.Sort = sortable()
	}
	return f
}

func sortStr(name string) search.Field {
	return search.Field{Name: name, Type: "string", Sort: sortable()}
}
func num(name string) search.Field { return search.Field{Name: name, Type: "int64", Sort: sortable()} }

// schemas are the fields of each collection. Text a person types is matched with infix search; every field a list can be ordered
// by is sortable, and a text column is sorted on a lower-cased copy (name_sort) so "b" does not come after "A".
func fields(k Kind) []search.Field {
	var f []search.Field
	switch k {
	case Outlets:
		f = []search.Field{text("name", false), text("address", false), text("phone", false), text("email", false),
			num("idn"), sortStr("name_sort"), sortStr("address_sort"), sortStr("phone_sort"), num("active_n"), {Name: "active", Type: "bool"}}
	case Services:
		f = []search.Field{text("name", false), text("description", false), text("internal_notes", false), text("category_name", false),
			num("idn"), sortStr("name_sort"), sortStr("unit"), num("price"), num("category_id"), {Name: "tax_type", Type: "string", Facet: true}, {Name: "discount_type", Type: "string", Facet: true}, num("active_n"), {Name: "active", Type: "bool"}}
	case Materials:
		f = []search.Field{text("sku", false), text("name", false), text("notes", false),
			num("idn"), sortStr("sku_sort"), sortStr("name_sort"), sortStr("category"), {Name: "active", Type: "bool"},
			num("created_at"), num("updated_at")}
	case VariantGroups, ModifierGroups:
		f = []search.Field{text("name", false), text("notes", false), {Name: "option_names", Type: "string[]", Infix: true},
			num("idn"), sortStr("name_sort"), num("created_at"), num("updated_at"),
			{Name: "service_ids", Type: "int64[]"}, {Name: "used", Type: "bool"}, num("min")}
	case Banners:
		// Whether a banner is live changes with the clock, so it is not a field: the window is (starts_n, ends_n) and a list asks for
		// "live" by comparing them with the time of the request. outlet_ids holds 0 for a banner with no outlet limit.
		f = []search.Field{text("title", false), text("internal_notes", false),
			num("idn"), sortStr("title_sort"), num("sort_n"), {Name: "status", Type: "string", Facet: true}, num("active_n"), {Name: "target_type", Type: "string", Facet: true},
			num("starts_n"), num("ends_n"), {Name: "outlet_ids", Type: "int64[]"}, num("created_at"), num("updated_at")}
	case Vouchers:
		// Like banners, whether a voucher is live (and where its window stands) is judged at query time from (starts_n, ends_n), never
		// stored. claims_n is how many customers hold it: it changes with every claim and injection, so those write the document again.
		f = []search.Field{text("title", false), text("code", false), text("description", false), text("internal_notes", false),
			num("idn"), sortStr("title_sort"), sortStr("code_sort"), {Name: "status", Type: "string", Facet: true}, num("active_n"),
			{Name: "discount_type", Type: "string", Facet: true}, {Name: "type", Type: "string", Facet: true}, num("value_n"), num("claims_n"),
			num("starts_n"), num("ends_n"), {Name: "outlet_ids", Type: "int64[]"}, num("created_at"), num("updated_at")}
	}
	return f
}

// Search runs one query against a kind.
func (ix *Index) Search(ctx context.Context, k Kind, q search.Query) (search.Result, error) {
	return ix.cols[k].Search(ctx, q)
}

// Put writes one document; Remove deletes one. Both are called right after Postgres commits.
func (ix *Index) Put(ctx context.Context, k Kind, doc map[string]any) error {
	return ix.cols[k].Put(ctx, doc)
}

func (ix *Index) Remove(ctx context.Context, k Kind, id int64) error {
	return ix.cols[k].Remove(ctx, fmt.Sprint(id))
}

// Source reads every document of a kind from Postgres.
type Source func(ctx context.Context, k Kind) ([]map[string]any, error)

func (ix *Index) bind(k Kind, src Source) search.Source {
	return func(ctx context.Context) ([]map[string]any, error) { return src(ctx, k) }
}

// Ensure makes sure every collection exists at the current schema version and is filled; see search.Collection.Ensure. It is what
// runs when a service starts.
func (ix *Index) Ensure(ctx context.Context, src Source) error {
	for _, k := range Kinds {
		if err := ix.cols[k].Ensure(ctx, ix.bind(k, src)); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

// Rebuild rebuilds the named kinds (all of them when none is named) from Postgres.
func (ix *Index) Rebuild(ctx context.Context, src Source, only ...Kind) error {
	for _, k := range kindsOr(only) {
		if err := ix.cols[k].Rebuild(ctx, ix.bind(k, src)); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

// Reconcile repairs every collection against Postgres; see search.Collection.Reconcile.
func (ix *Index) Reconcile(ctx context.Context, src Source) error {
	for _, k := range Kinds {
		if err := ix.cols[k].Reconcile(ctx, ix.bind(k, src)); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

func kindsOr(only []Kind) []Kind {
	if len(only) > 0 {
		return only
	}
	return Kinds
}
