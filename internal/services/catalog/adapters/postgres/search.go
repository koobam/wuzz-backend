package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/adapters/searchindex"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// This file is where the Typesense index meets the store. Postgres stays the source of truth: a list asks the index for the ids of
// its page (and the total) and then reads those rows from Postgres in that order, so a row never drifts from the database. After
// every write the affected rows are re-read and put in the index (or removed, if the row is gone).
//
// Without an index (Typesense not configured) the same lists are answered by Postgres directly. With one, a search that cannot
// reach it is an error — a 503 — never a quiet switch back to Postgres.

// WithIndex gives the store a search index; nil keeps listing from Postgres.
func (s *Store) WithIndex(ix *searchindex.Index, log *slog.Logger) *Store {
	s.ix, s.log = ix, log
	return s
}

// SearchEnabled says whether lists are answered by the index.
func (s *Store) SearchEnabled() bool { return s.ix.Enabled() }

const searchTimeout = 3 // seconds, the index write after a commit

// ---- paged outlets and services ----

var outletOrder = map[string]string{"id": "id", "name": "lower(name)", "address": "lower(address)", "phone": "lower(phone)", "active": "active"}

func orderBy(m map[string]string, by, dir string) string {
	col, ok := m[by]
	if !ok {
		col = m["id"]
	}
	if dir == "desc" {
		return col + " DESC"
	}
	return col + " ASC"
}

func (s *Store) PageOutlets(ctx context.Context, q domain.OutletQuery) ([]domain.Outlet, int64, error) {
	if s.ix.Enabled() {
		res, err := s.ix.Search(ctx, searchindex.Outlets, searchindex.OutletQuery(q))
		if err != nil {
			return nil, 0, err
		}
		out, err := s.outletsByIDs(ctx, res.IDs)
		return out, res.Total, err
	}
	where := `($1 = '' OR strpos(lower(name), lower($1)) > 0 OR strpos(lower(address), lower($1)) > 0 OR strpos(lower(phone), lower($1)) > 0 OR strpos(lower(email), lower($1)) > 0)
		AND ($2 = '' OR active = ($2 = 'true'))`
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outlets WHERE `+where, q.Q, q.Active).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("Could not list outlets").WithCause(err)
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM outlets WHERE %s ORDER BY %s, id ASC LIMIT $3 OFFSET $4`, outletCols, where, orderBy(outletOrder, q.SortBy, q.SortDir)),
		q.Q, q.Active, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, apperr.Internal("Could not list outlets").WithCause(err)
	}
	defer rows.Close()
	out := []domain.Outlet{}
	for rows.Next() {
		o, err := scanOutlet(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	out, err = s.attachHours(ctx, out)
	return out, total, err
}

// outletsByIDs reads the outlets with these ids, in the order given. An id with no row (the index is a moment behind) is skipped.
func (s *Store) outletsByIDs(ctx context.Context, ids []int64) ([]domain.Outlet, error) {
	out := []domain.Outlet{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+outletCols+` FROM outlets WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, apperr.Internal("Could not list outlets").WithCause(err)
	}
	defer rows.Close()
	byID := map[int64]domain.Outlet{}
	for rows.Next() {
		o, err := scanOutlet(rows)
		if err != nil {
			return nil, err
		}
		byID[o.ID] = o
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if o, ok := byID[id]; ok {
			out = append(out, o)
		} else {
			s.stale(searchindex.Outlets, id)
		}
	}
	rows.Close()
	return s.attachHours(ctx, out)
}

var serviceOrder = map[string]string{"id": "s.id", "name": "lower(s.name)", "unit": "s.unit", "price": "s.price", "category_id": "COALESCE(s.category_id, 0)", "active": "s.active"}

func (s *Store) PageServices(ctx context.Context, q domain.ServiceQuery) ([]domain.ServiceItem, int64, error) {
	var items []domain.ServiceItem
	var total int64
	if s.ix.Enabled() {
		res, err := s.ix.Search(ctx, searchindex.Services, searchindex.ServiceQuery(q))
		if err != nil {
			return nil, 0, err
		}
		total = res.Total
		if items, err = s.servicesByIDs(ctx, res.IDs); err != nil {
			return nil, 0, err
		}
	} else {
		where := `($1 = '' OR strpos(lower(s.name), lower($1)) > 0 OR strpos(lower(s.description), lower($1)) > 0 OR strpos(lower(s.internal_notes), lower($1)) > 0
				OR strpos(lower(COALESCE(c.name, '')), lower($1)) > 0)
			AND ($2::bigint = 0 OR COALESCE(s.category_id, 0) = $2) AND ($3 = '' OR s.unit = $3) AND ($4 = '' OR s.active = ($4 = 'true')) AND ($5 = '' OR s.tax_type = $5) AND ($6 = '' OR s.discount_kind = $6)`
		from := `FROM services s LEFT JOIN service_categories c ON c.id = s.category_id WHERE ` + where
		if err := s.pool.QueryRow(ctx, `SELECT count(*) `+from, q.Q, q.CategoryID, q.Unit, q.Active, q.TaxType, q.DiscountType).Scan(&total); err != nil {
			return nil, 0, apperr.Internal("Could not list services").WithCause(err)
		}
		rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s %s ORDER BY %s, s.id ASC LIMIT $7 OFFSET $8`, prefixed("s.", serviceCols), from, orderBy(serviceOrder, q.SortBy, q.SortDir)),
			q.Q, q.CategoryID, q.Unit, q.Active, q.TaxType, q.DiscountType, q.Limit, q.Offset)
		if err != nil {
			return nil, 0, apperr.Internal("Could not list services").WithCause(err)
		}
		items = []domain.ServiceItem{}
		for rows.Next() {
			it, err := scanService(rows)
			if err != nil {
				rows.Close()
				return nil, 0, err
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, 0, apperr.Internal("Could not list services").WithCause(err)
		}
	}
	if err := applyTax(ctx, s.pool, items); err != nil {
		return nil, 0, err
	}
	return items, total, attachGroups(ctx, s.pool, items)
}

// prefixed puts a table alias in front of every column of a comma-separated column list. The one expression in the list,
// COALESCE(category_id, 0), has a comma of its own, so it is written out whole.
func prefixed(alias, cols string) string {
	const category = "COALESCE(category_id, 0)"
	cols = strings.Replace(cols, category, "category_id_or_zero", 1)
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "category_id_or_zero" {
			parts[i] = "COALESCE(" + alias + "category_id, 0)"
			continue
		}
		parts[i] = alias + p
	}
	return strings.Join(parts, ", ")
}

func (s *Store) servicesByIDs(ctx context.Context, ids []int64) ([]domain.ServiceItem, error) {
	out := []domain.ServiceItem{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+serviceCols+` FROM services WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, apperr.Internal("Could not list services").WithCause(err)
	}
	byID := map[int64]domain.ServiceItem{}
	for rows.Next() {
		it, err := scanService(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		byID[it.ID] = it
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("Could not list services").WithCause(err)
	}
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			out = append(out, it)
		} else {
			s.stale(searchindex.Services, id)
		}
	}
	return out, nil
}

// ---- materials and groups through the index ----

func (s *Store) searchMaterials(ctx context.Context, f domain.MaterialQuery) ([]domain.Material, int64, error) {
	res, err := s.ix.Search(ctx, searchindex.Materials, searchindex.MaterialQuery(f))
	if err != nil {
		return nil, 0, err
	}
	out := []domain.Material{}
	if len(res.IDs) == 0 {
		return out, res.Total, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+materialCols+` FROM materials m WHERE m.id = ANY($1)`, res.IDs)
	if err != nil {
		return nil, 0, internal("Could not list materials", err)
	}
	defer rows.Close()
	byID := map[int64]domain.Material{}
	for rows.Next() {
		m, err := scanMaterial(rows)
		if err != nil {
			return nil, 0, err
		}
		byID[m.ID] = m
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for _, id := range res.IDs {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		} else {
			s.stale(searchindex.Materials, id)
		}
	}
	return out, res.Total, nil
}

// searchGroupIDs is the index's version of matchingGroupIDs: the ids of the page, in order, and the total.
func (s *Store) searchGroupIDs(ctx context.Context, kind string, f domain.GroupQuery) ([]int64, int64, error) {
	res, err := s.ix.Search(ctx, groupKind(kind), searchindex.GroupQuery(f))
	if err != nil {
		return nil, 0, err
	}
	return res.IDs, res.Total, nil
}

func groupKind(kind string) searchindex.Kind {
	if kind == "variant" {
		return searchindex.VariantGroups
	}
	return searchindex.ModifierGroups
}

// stale notes an index entry whose row is gone; the next reconcile removes it.
func (s *Store) stale(k searchindex.Kind, id int64) {
	if s.log != nil {
		s.log.Warn("search index has a document with no row", slog.String("collection", string(k)), slog.Int64("id", id))
	}
}

// ---- keeping the index in step with writes ----

// touched is the set of rows a write changed, by collection.
type touched map[searchindex.Kind][]int64

func (t touched) add(k searchindex.Kind, ids ...int64) { t[k] = append(t[k], ids...) }

// sync re-reads each touched row from Postgres after the commit and puts it in the index, or removes it if the row is gone. It is
// best effort — the user's write has already succeeded — but a failure is logged loudly by the index and repaired by the reconcile.
func (s *Store) sync(ctx context.Context, t touched) {
	if !s.ix.Enabled() {
		return
	}
	// The request may be over by the time the index answers; the write must not be lost with it.
	ctx = context.WithoutCancel(ctx)
	for k, ids := range t {
		seen := map[int64]bool{}
		for _, id := range ids {
			if id == 0 || seen[id] {
				continue
			}
			seen[id] = true
			doc, err := s.document(ctx, k, id)
			switch {
			case err == nil:
				_ = s.ix.Put(ctx, k, doc)
			case isNotFound(err):
				_ = s.ix.Remove(ctx, k, id)
			default:
				if s.log != nil {
					s.log.Error("search index sync could not read the row", slog.String("collection", string(k)), slog.Int64("id", id), slog.Any("error", err))
				}
			}
		}
	}
}

func isNotFound(err error) bool {
	return apperr.IsKind(err, apperr.KindNotFound) || errors.Is(err, errGone)
}

var errGone = errors.New("row gone")

// document builds the index document of one row.
func (s *Store) document(ctx context.Context, k searchindex.Kind, id int64) (map[string]any, error) {
	switch k {
	case searchindex.Outlets:
		o, err := s.GetOutlet(ctx, id)
		if err != nil {
			return nil, err
		}
		return searchindex.OutletDoc(o), nil
	case searchindex.Services:
		it, err := scanService(s.pool.QueryRow(ctx, `SELECT `+serviceCols+` FROM services WHERE id = $1`, id))
		if err != nil {
			return nil, err
		}
		names, err := s.categoryNames(ctx)
		if err != nil {
			return nil, err
		}
		return searchindex.ServiceDoc(it, names[it.CategoryID]), nil
	case searchindex.Materials:
		m, err := s.GetMaterial(ctx, id)
		if err != nil {
			return nil, err
		}
		return searchindex.MaterialDoc(m), nil
	case searchindex.Banners:
		b, err := s.GetBanner(ctx, id)
		if err != nil {
			return nil, err
		}
		return searchindex.BannerDoc(b), nil
	case searchindex.Vouchers:
		v, err := s.GetVoucher(ctx, id)
		if err != nil {
			return nil, err
		}
		return searchindex.VoucherDoc(v), nil
	case searchindex.VariantGroups:
		g, err := s.GetVariantGroup(ctx, id)
		if err != nil {
			return nil, err
		}
		return variantGroupDoc(g), nil
	case searchindex.ModifierGroups:
		g, err := s.GetModifierGroup(ctx, id)
		if err != nil {
			return nil, err
		}
		return modifierGroupDoc(g), nil
	}
	return nil, fmt.Errorf("unknown collection %q", k)
}

func (s *Store) categoryNames(ctx context.Context) (map[int64]string, error) {
	cats, err := s.ListServiceCategories(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(cats))
	for _, c := range cats {
		out[c.ID] = c.Name
	}
	return out, nil
}

func refIDs(in []domain.ServiceRef) []int64 {
	out := make([]int64, 0, len(in))
	for _, r := range in {
		out = append(out, r.ID)
	}
	return out
}

func variantGroupDoc(g domain.VariantGroup) map[string]any {
	names := make([]string, 0, len(g.Options))
	for _, o := range g.Options {
		names = append(names, o.Name)
	}
	return searchindex.GroupDoc(searchindex.Group{ID: g.ID, Name: g.Name, Notes: g.InternalNotes, OptionNames: names, ServiceIDs: refIDs(g.Services),
		Created: g.CreatedAt.Unix(), Edit: g.UpdatedAt.Unix()})
}

func modifierGroupDoc(g domain.ModifierGroup) map[string]any {
	names := make([]string, 0, len(g.Options))
	for _, o := range g.Options {
		names = append(names, o.Name)
	}
	return searchindex.GroupDoc(searchindex.Group{ID: g.ID, Name: g.Name, Notes: g.InternalNotes, OptionNames: names, ServiceIDs: refIDs(g.Services), Min: g.Min,
		Created: g.CreatedAt.Unix(), Edit: g.UpdatedAt.Unix()})
}

// everything is the searchindex.Source: every row of a collection, read from Postgres, as documents.
func (s *Store) Everything(ctx context.Context, k searchindex.Kind) ([]map[string]any, error) {
	const all = 1 << 30
	var docs []map[string]any
	switch k {
	case searchindex.Outlets:
		outlets, err := s.ListOutlets(ctx, nil, true, nil)
		if err != nil {
			return nil, err
		}
		for _, o := range outlets {
			docs = append(docs, searchindex.OutletDoc(o))
		}
	case searchindex.Services:
		items, err := s.ListServices(ctx, true)
		if err != nil {
			return nil, err
		}
		names, err := s.categoryNames(ctx)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			docs = append(docs, searchindex.ServiceDoc(it, names[it.CategoryID]))
		}
	case searchindex.Materials:
		mats, _, err := s.listMaterialsSQL(ctx, domain.MaterialQuery{SortBy: "id", SortDir: "asc", Limit: all})
		if err != nil {
			return nil, err
		}
		for _, m := range mats {
			docs = append(docs, searchindex.MaterialDoc(m))
		}
	case searchindex.Banners:
		banners, _, err := s.listBannersSQL(ctx, domain.BannerQuery{SortBy: "id", SortDir: "asc", Limit: all})
		if err != nil {
			return nil, err
		}
		for _, b := range banners {
			docs = append(docs, searchindex.BannerDoc(b))
		}
	case searchindex.Vouchers:
		vouchers, _, err := s.listVouchersSQL(ctx, domain.VoucherQuery{SortBy: "id", SortDir: "asc", Limit: all})
		if err != nil {
			return nil, err
		}
		for _, v := range vouchers {
			docs = append(docs, searchindex.VoucherDoc(v))
		}
	case searchindex.VariantGroups:
		ids, _, err := matchingGroupIDs(ctx, s.pool, "variant", domain.GroupQuery{SortBy: "id", SortDir: "asc", Limit: all})
		if err != nil {
			return nil, err
		}
		byID, _, err := variantGroups(ctx, s.pool, ids)
		if err != nil {
			return nil, err
		}
		groups, err := withVariantServices(ctx, s.pool, byID, ids)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			docs = append(docs, variantGroupDoc(g))
		}
	case searchindex.ModifierGroups:
		ids, _, err := matchingGroupIDs(ctx, s.pool, "modifier", domain.GroupQuery{SortBy: "id", SortDir: "asc", Limit: all})
		if err != nil {
			return nil, err
		}
		byID, _, err := modifierGroups(ctx, s.pool, ids)
		if err != nil {
			return nil, err
		}
		groups, err := withModifierServices(ctx, s.pool, byID, ids)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			docs = append(docs, modifierGroupDoc(g))
		}
	default:
		return nil, fmt.Errorf("unknown collection %q", k)
	}
	return docs, nil
}

// linkedGroupIDs are the variant and modifier groups a service offers right now.
func (s *Store) linkedGroupIDs(ctx context.Context, serviceID int64) (variants, modifiers []int64) {
	if !s.ix.Enabled() || serviceID == 0 {
		return nil, nil
	}
	read := func(table, col string) []int64 {
		rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE service_id = $1`, col, table), serviceID)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		return ids
	}
	return read("service_variant_groups", "variant_group_id"), read("service_modifier_groups", "modifier_group_id")
}

// syncService puts a service in the index, and every group it offers now or offered before: whether a group is "used", and by which
// services, is part of the group's document.
func (s *Store) syncService(ctx context.Context, id int64, oldVariants, oldModifiers []int64) {
	if !s.ix.Enabled() {
		return
	}
	nowVariants, nowModifiers := s.linkedGroupIDs(ctx, id)
	s.sync(ctx, touched{
		searchindex.Services:       {id},
		searchindex.VariantGroups:  append(oldVariants, nowVariants...),
		searchindex.ModifierGroups: append(oldModifiers, nowModifiers...),
	})
}
