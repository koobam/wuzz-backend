package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/adapters/searchindex"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// queryer is what both the pool and a transaction offer, so the readers work in either.
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func internal(msg string, err error) error { return apperr.Internal(msg).WithCause(err) }

// ---- reading groups ----

const variantGroupCols = `id, name, internal_notes, sort_index, created_at, updated_at`

// variantGroups reads groups with their options; nil ids means every group, in the order the back office lists them.
func variantGroups(ctx context.Context, q queryer, ids []int64) (map[int64]*domain.VariantGroup, []int64, error) {
	rows, err := q.Query(ctx, `SELECT `+variantGroupCols+` FROM variant_groups WHERE ($1::bigint[] IS NULL OR id = ANY($1)) ORDER BY sort_index, id`, ids)
	if err != nil {
		return nil, nil, internal("Could not read variant groups", err)
	}
	defer rows.Close()
	byID := map[int64]*domain.VariantGroup{}
	var order []int64
	for rows.Next() {
		g := &domain.VariantGroup{}
		if err := rows.Scan(&g.ID, &g.Name, &g.InternalNotes, &g.SortIndex, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, nil, internal("Could not read variant groups", err)
		}
		byID[g.ID], order = g, append(order, g.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, internal("Could not read variant groups", err)
	}
	rows.Close()
	if len(order) == 0 {
		return byID, order, nil
	}
	opts, err := q.Query(ctx, `
		SELECT group_id, id, name, price_delta, is_default, active, sort_index, estimated_time, estimated_time_unit
		FROM variant_options WHERE group_id = ANY($1) ORDER BY sort_index, id`, order)
	if err != nil {
		return nil, nil, internal("Could not read variant options", err)
	}
	defer opts.Close()
	for opts.Next() {
		var gid int64
		var o domain.VariantOption
		if err := opts.Scan(&gid, &o.ID, &o.Name, &o.PriceDelta, &o.IsDefault, &o.Active, &o.SortIndex, &o.EstimatedTime, &o.EstimatedTimeUnit); err != nil {
			return nil, nil, internal("Could not read variant options", err)
		}
		byID[gid].Options = append(byID[gid].Options, o)
	}
	return byID, order, opts.Err()
}

const modifierGroupCols = `id, name, internal_notes, min, max, created_at, updated_at`

func modifierGroups(ctx context.Context, q queryer, ids []int64) (map[int64]*domain.ModifierGroup, []int64, error) {
	rows, err := q.Query(ctx, `SELECT `+modifierGroupCols+` FROM modifier_groups WHERE ($1::bigint[] IS NULL OR id = ANY($1)) ORDER BY lower(name), id`, ids)
	if err != nil {
		return nil, nil, internal("Could not read modifier groups", err)
	}
	defer rows.Close()
	byID := map[int64]*domain.ModifierGroup{}
	var order []int64
	for rows.Next() {
		g := &domain.ModifierGroup{}
		if err := rows.Scan(&g.ID, &g.Name, &g.InternalNotes, &g.Min, &g.Max, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, nil, internal("Could not read modifier groups", err)
		}
		byID[g.ID], order = g, append(order, g.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, internal("Could not read modifier groups", err)
	}
	rows.Close()
	if len(order) == 0 {
		return byID, order, nil
	}
	opts, err := q.Query(ctx, `
		SELECT group_id, id, name, description, price, active, sort_index
		FROM modifier_options WHERE group_id = ANY($1) ORDER BY sort_index, id`, order)
	if err != nil {
		return nil, nil, internal("Could not read modifier options", err)
	}
	defer opts.Close()
	for opts.Next() {
		var gid int64
		var o domain.ModifierOption
		if err := opts.Scan(&gid, &o.ID, &o.Name, &o.Description, &o.Price, &o.Active, &o.SortIndex); err != nil {
			return nil, nil, internal("Could not read modifier options", err)
		}
		byID[gid].Options = append(byID[gid].Options, o)
	}
	return byID, order, opts.Err()
}

// usedBy lists, per group id, the services that link it; kind is "variant" or "modifier".
func usedBy(ctx context.Context, q queryer, kind string, ids []int64) (map[int64][]domain.ServiceRef, error) {
	rows, err := q.Query(ctx, fmt.Sprintf(`
		SELECT l.%[1]s_group_id, s.id, s.name FROM service_%[1]s_groups l JOIN services s ON s.id = l.service_id
		WHERE l.%[1]s_group_id = ANY($1) ORDER BY lower(s.name), s.id`, kind), ids)
	if err != nil {
		return nil, internal("Could not read where the groups are used", err)
	}
	defer rows.Close()
	out := map[int64][]domain.ServiceRef{}
	for rows.Next() {
		var gid int64
		var ref domain.ServiceRef
		if err := rows.Scan(&gid, &ref.ID, &ref.Name); err != nil {
			return nil, internal("Could not read where the groups are used", err)
		}
		out[gid] = append(out[gid], ref)
	}
	return out, rows.Err()
}

// linkedGroupIDs reads, per service, the ids of its linked groups in the order they are shown.
func linkedGroupIDs(ctx context.Context, q queryer, kind string, serviceIDs []int64) (map[int64][]int64, []int64, error) {
	rows, err := q.Query(ctx, fmt.Sprintf(`SELECT service_id, %[1]s_group_id FROM service_%[1]s_groups WHERE service_id = ANY($1) ORDER BY service_id, sort_index, %[1]s_group_id`, kind), serviceIDs)
	if err != nil {
		return nil, nil, internal("Could not read the service's groups", err)
	}
	defer rows.Close()
	per := map[int64][]int64{}
	seen := map[int64]bool{}
	var all []int64
	for rows.Next() {
		var sid, gid int64
		if err := rows.Scan(&sid, &gid); err != nil {
			return nil, nil, internal("Could not read the service's groups", err)
		}
		per[sid] = append(per[sid], gid)
		if !seen[gid] {
			seen[gid], all = true, append(all, gid)
		}
	}
	return per, all, rows.Err()
}

// attachGroups fills VariantGroups and ModifierGroups of the services, two queries per kind however many there are.
func attachGroups(ctx context.Context, q queryer, items []domain.ServiceItem) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	vLinks, vAll, err := linkedGroupIDs(ctx, q, "variant", ids)
	if err != nil {
		return err
	}
	vGroups := map[int64]*domain.VariantGroup{}
	if len(vAll) > 0 {
		if vGroups, _, err = variantGroups(ctx, q, vAll); err != nil {
			return err
		}
	}
	mLinks, mAll, err := linkedGroupIDs(ctx, q, "modifier", ids)
	if err != nil {
		return err
	}
	mGroups := map[int64]*domain.ModifierGroup{}
	if len(mAll) > 0 {
		if mGroups, _, err = modifierGroups(ctx, q, mAll); err != nil {
			return err
		}
	}
	for i := range items {
		for _, gid := range vLinks[items[i].ID] {
			items[i].VariantGroups = append(items[i].VariantGroups, *vGroups[gid])
		}
		for _, gid := range mLinks[items[i].ID] {
			items[i].ModifierGroups = append(items[i].ModifierGroups, *mGroups[gid])
		}
	}
	return nil
}

// setServiceLinks replaces a service's links to variant and modifier groups with the given ordered ids; a nil list is left
// alone. An id that names no group is refused before anything is written.
func setServiceLinks(ctx context.Context, tx pgx.Tx, serviceID int64, variantIDs, modifierIDs []int64) error {
	for _, l := range []struct {
		kind string
		ids  []int64
	}{{"variant", variantIDs}, {"modifier", modifierIDs}} {
		if l.ids == nil {
			continue
		}
		var found int
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s_groups WHERE id = ANY($1)`, l.kind), l.ids).Scan(&found); err != nil {
			return internal("Could not check the groups", err)
		}
		if found != len(l.ids) {
			return apperr.Invalid(fmt.Sprintf("One of the %s groups does not exist", l.kind))
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM service_%[1]s_groups WHERE service_id = $1`, l.kind), serviceID); err != nil {
			return internal("Could not update the service's groups", err)
		}
		for i, gid := range l.ids {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO service_%[1]s_groups (service_id, %[1]s_group_id, sort_index) VALUES ($1, $2, $3)`, l.kind), serviceID, gid, i+1); err != nil {
				return internal("Could not update the service's groups", err)
			}
		}
	}
	return nil
}

// ---- variant groups ----

// groupOrder maps a domain.GroupSortFields name to its SQL. Ties fall back to the id, so the order is stable.
var groupOrder = map[string]string{"id": "g.id", "name": "lower(g.name)", "created_at": "g.created_at", "updated_at": "g.updated_at"}

// matchingGroupIDs returns, in the order the query asks for, the ids of the groups it matches. The search is a literal,
// case-insensitive substring (strpos, not LIKE, so a % or _ typed by the user is just a character).
func matchingGroupIDs(ctx context.Context, q queryer, kind string, f domain.GroupQuery) ([]int64, int64, error) {
	dir := "ASC"
	if f.SortDir == "desc" {
		dir = "DESC"
	}
	by, ok := groupOrder[f.SortBy]
	if !ok {
		by = groupOrder["id"]
	}
	where := fmt.Sprintf(`($1 = '' OR strpos(lower(g.name), lower($1)) > 0 OR strpos(lower(g.internal_notes), lower($1)) > 0
		OR EXISTS (SELECT 1 FROM %[1]s_options o WHERE o.group_id = g.id AND strpos(lower(o.name), lower($1)) > 0))`, kind)
	if kind == "modifier" {
		where += ` AND ($2 = '' OR ($2 = 'required' AND g.min >= 1) OR ($2 = 'optional' AND g.min = 0))`
	}
	args := []any{f.Q}
	if kind == "modifier" {
		args = append(args, f.Requirement)
	}
	// Which services use the group: the link table, asked with EXISTS so a group on many services still counts once.
	link := fmt.Sprintf(`SELECT 1 FROM service_%[1]s_groups l WHERE l.%[1]s_group_id = g.id`, kind)
	if f.ServiceID > 0 {
		args = append(args, f.ServiceID)
		where += fmt.Sprintf(` AND EXISTS (%s AND l.service_id = $%d)`, link, len(args))
	}
	switch f.Usage {
	case "used":
		where += ` AND EXISTS (` + link + `)`
	case "unused":
		where += ` AND NOT EXISTS (` + link + `)`
	}
	// The count and the page see the same filter; the id tie-break keeps the pages from overlapping.
	var total int64
	if err := q.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s_groups g WHERE %s`, kind, where), args...).Scan(&total); err != nil {
		return nil, 0, internal("Could not list groups", err)
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := q.Query(ctx, fmt.Sprintf(`SELECT g.id FROM %s_groups g WHERE %s ORDER BY %s %s, g.id ASC LIMIT $%d OFFSET $%d`,
		kind, where, by, dir, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, internal("Could not list groups", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, 0, internal("Could not list groups", err)
		}
		ids = append(ids, id)
	}
	return ids, total, rows.Err()
}

// matchingIDs is the ids of the page a group list asks for: from the search index when there is one, else from Postgres.
func (s *Store) matchingIDs(ctx context.Context, kind string, f domain.GroupQuery) ([]int64, int64, error) {
	if s.ix.Enabled() {
		return s.searchGroupIDs(ctx, kind, f)
	}
	return matchingGroupIDs(ctx, s.pool, kind, f)
}

func (s *Store) ListVariantGroups(ctx context.Context, f domain.GroupQuery) ([]domain.VariantGroup, int64, error) {
	ids, total, err := s.matchingIDs(ctx, "variant", f)
	if err != nil || len(ids) == 0 {
		return []domain.VariantGroup{}, total, err
	}
	byID, _, err := variantGroups(ctx, s.pool, ids)
	if err != nil {
		return nil, 0, err
	}
	out, err := withVariantServices(ctx, s.pool, byID, ids)
	return out, total, err
}

func withVariantServices(ctx context.Context, q queryer, byID map[int64]*domain.VariantGroup, order []int64) ([]domain.VariantGroup, error) {
	out := make([]domain.VariantGroup, 0, len(order))
	if len(order) == 0 {
		return out, nil
	}
	used, err := usedBy(ctx, q, "variant", order)
	if err != nil {
		return nil, err
	}
	for _, id := range order {
		g := *byID[id]
		g.Services = used[id]
		out = append(out, g)
	}
	return out, nil
}

func (s *Store) GetVariantGroup(ctx context.Context, id int64) (domain.VariantGroup, error) {
	return getVariantGroup(ctx, s.pool, id)
}

func getVariantGroup(ctx context.Context, q queryer, id int64) (domain.VariantGroup, error) {
	byID, order, err := variantGroups(ctx, q, []int64{id})
	if err != nil {
		return domain.VariantGroup{}, err
	}
	if len(order) == 0 {
		return domain.VariantGroup{}, apperr.NotFound("Variant group not found")
	}
	out, err := withVariantServices(ctx, q, byID, order)
	if err != nil {
		return domain.VariantGroup{}, err
	}
	return out[0], nil
}

func (s *Store) CreateVariantGroup(ctx context.Context, g domain.VariantGroup) (domain.VariantGroup, error) {
	var out domain.VariantGroup
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO variant_groups (name, internal_notes, sort_index)
			VALUES ($1, $2, COALESCE((SELECT max(sort_index) FROM variant_groups), 0) + 1) RETURNING id`,
			g.Name, g.InternalNotes).Scan(&id); err != nil {
			return err
		}
		for _, o := range g.Options {
			if err := insertVariantOption(ctx, tx, id, o); err != nil {
				return err
			}
		}
		var err error
		out, err = getVariantGroup(ctx, tx, id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.VariantGroups: {out.ID}})
	}
	return out, wrapGroup(err, "Could not create the variant group")
}

func insertVariantOption(ctx context.Context, q queryer, groupID int64, o domain.VariantOption) error {
	_, err := q.Exec(ctx, `
		INSERT INTO variant_options (group_id, name, price_delta, is_default, active, sort_index, estimated_time, estimated_time_unit)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		groupID, o.Name, o.PriceDelta, o.IsDefault, o.Active, o.SortIndex, o.EstimatedTime, o.EstimatedTimeUnit)
	return err
}

// UpdateVariantGroup replaces the group's name, note and options. Options are matched by id, so those that stay keep their
// id; the ones the form no longer has are deleted and the ones without an id are added.
func (s *Store) UpdateVariantGroup(ctx context.Context, g domain.VariantGroup) (domain.VariantGroup, error) {
	var out domain.VariantGroup
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE variant_groups SET name=$2, internal_notes=$3, updated_at=now() WHERE id=$1`, g.ID, g.Name, g.InternalNotes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("Variant group not found")
		}
		existing, err := optionIDs(ctx, tx, "variant", g.ID)
		if err != nil {
			return err
		}
		kept := make([]int64, 0, len(g.Options))
		for _, o := range g.Options {
			if o.ID == 0 {
				continue
			}
			if !existing[o.ID] {
				return apperr.Invalid(fmt.Sprintf("Option %d is not in this group", o.ID))
			}
			kept = append(kept, o.ID)
		}
		if err := deleteOptionBOM(ctx, tx, domain.OwnerVariantOption, droppedIDs(existing, kept)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM variant_options WHERE group_id=$1 AND NOT (id = ANY($2))`, g.ID, kept); err != nil {
			return err
		}
		// Clear what could collide while the options are rewritten one by one: the default flag and the names.
		if _, err := tx.Exec(ctx, `UPDATE variant_options SET is_default=false, name='~' || id::text WHERE group_id=$1`, g.ID); err != nil {
			return err
		}
		for _, o := range g.Options {
			if o.ID == 0 {
				if err := insertVariantOption(ctx, tx, g.ID, o); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(ctx, `
				UPDATE variant_options SET name=$3, price_delta=$4, is_default=$5, active=$6, sort_index=$7,
				       estimated_time=$8, estimated_time_unit=$9, updated_at=now()
				WHERE group_id=$1 AND id=$2`,
				g.ID, o.ID, o.Name, o.PriceDelta, o.IsDefault, o.Active, o.SortIndex, o.EstimatedTime, o.EstimatedTimeUnit); err != nil {
				return err
			}
		}
		out, err = getVariantGroup(ctx, tx, g.ID)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.VariantGroups: {out.ID}})
	}
	return out, wrapGroup(err, "Could not update the variant group")
}

func optionIDs(ctx context.Context, q queryer, kind string, groupID int64) (map[int64]bool, error) {
	rows, err := q.Query(ctx, fmt.Sprintf(`SELECT id FROM %s_options WHERE group_id=$1`, kind), groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// DeleteVariantGroup refuses while a service still offers the group, naming the services, rather than letting the foreign
// keys quietly take it off every one of them.
func (s *Store) DeleteVariantGroup(ctx context.Context, id int64) error {
	return deleteGroup(ctx, s, "variant", id)
}

func deleteGroup(ctx context.Context, s *Store, kind string, id int64) error {
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s_groups WHERE id = $1 FOR UPDATE)`, kind), id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return apperr.NotFound(fmt.Sprintf("%s group not found", capitalize(kind)))
		}
		used, err := usedBy(ctx, tx, kind, []int64{id})
		if err != nil {
			return err
		}
		if len(used[id]) > 0 {
			return apperr.Conflict(domain.UsedBy(used[id]))
		}
		// The options go with the group, and so do their recipe lines.
		opts, err := optionIDs(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(opts))
		for oid := range opts {
			ids = append(ids, oid)
		}
		if err := deleteOptionBOM(ctx, tx, kind+"_option", ids); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s_groups WHERE id = $1`, kind), id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{groupKind(kind): {id}})
	}
	return wrapGroup(err, "Could not delete the group")
}

func capitalize(s string) string { return string(s[0]-32) + s[1:] }

// ---- modifier groups ----

func (s *Store) ListModifierGroups(ctx context.Context, f domain.GroupQuery) ([]domain.ModifierGroup, int64, error) {
	ids, total, err := s.matchingIDs(ctx, "modifier", f)
	if err != nil || len(ids) == 0 {
		return []domain.ModifierGroup{}, total, err
	}
	byID, _, err := modifierGroups(ctx, s.pool, ids)
	if err != nil {
		return nil, 0, err
	}
	out, err := withModifierServices(ctx, s.pool, byID, ids)
	return out, total, err
}

func withModifierServices(ctx context.Context, q queryer, byID map[int64]*domain.ModifierGroup, order []int64) ([]domain.ModifierGroup, error) {
	out := make([]domain.ModifierGroup, 0, len(order))
	if len(order) == 0 {
		return out, nil
	}
	used, err := usedBy(ctx, q, "modifier", order)
	if err != nil {
		return nil, err
	}
	for _, id := range order {
		g := *byID[id]
		g.Services = used[id]
		out = append(out, g)
	}
	return out, nil
}

func (s *Store) GetModifierGroup(ctx context.Context, id int64) (domain.ModifierGroup, error) {
	return getModifierGroup(ctx, s.pool, id)
}

func getModifierGroup(ctx context.Context, q queryer, id int64) (domain.ModifierGroup, error) {
	byID, order, err := modifierGroups(ctx, q, []int64{id})
	if err != nil {
		return domain.ModifierGroup{}, err
	}
	if len(order) == 0 {
		return domain.ModifierGroup{}, apperr.NotFound("Modifier group not found")
	}
	out, err := withModifierServices(ctx, q, byID, order)
	if err != nil {
		return domain.ModifierGroup{}, err
	}
	return out[0], nil
}

func (s *Store) CreateModifierGroup(ctx context.Context, g domain.ModifierGroup) (domain.ModifierGroup, error) {
	var out domain.ModifierGroup
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO modifier_groups (name, internal_notes, min, max) VALUES ($1, $2, $3, $4) RETURNING id`,
			g.Name, g.InternalNotes, g.Min, g.Max).Scan(&id); err != nil {
			return err
		}
		for _, o := range g.Options {
			if err := insertModifierOption(ctx, tx, id, o); err != nil {
				return err
			}
		}
		var err error
		out, err = getModifierGroup(ctx, tx, id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.ModifierGroups: {out.ID}})
	}
	return out, wrapGroup(err, "Could not create the modifier group")
}

func insertModifierOption(ctx context.Context, q queryer, groupID int64, o domain.ModifierOption) error {
	_, err := q.Exec(ctx, `INSERT INTO modifier_options (group_id, name, description, price, active, sort_index) VALUES ($1, $2, $3, $4, $5, $6)`,
		groupID, o.Name, o.Description, o.Price, o.Active, o.SortIndex)
	return err
}

func (s *Store) UpdateModifierGroup(ctx context.Context, g domain.ModifierGroup) (domain.ModifierGroup, error) {
	var out domain.ModifierGroup
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE modifier_groups SET name=$2, internal_notes=$3, min=$4, max=$5, updated_at=now() WHERE id=$1`,
			g.ID, g.Name, g.InternalNotes, g.Min, g.Max)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("Modifier group not found")
		}
		existing, err := optionIDs(ctx, tx, "modifier", g.ID)
		if err != nil {
			return err
		}
		kept := make([]int64, 0, len(g.Options))
		for _, o := range g.Options {
			if o.ID == 0 {
				continue
			}
			if !existing[o.ID] {
				return apperr.Invalid(fmt.Sprintf("Option %d is not in this group", o.ID))
			}
			kept = append(kept, o.ID)
		}
		if err := deleteOptionBOM(ctx, tx, domain.OwnerModifierOption, droppedIDs(existing, kept)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM modifier_options WHERE group_id=$1 AND NOT (id = ANY($2))`, g.ID, kept); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE modifier_options SET name='~' || id::text WHERE group_id=$1`, g.ID); err != nil {
			return err
		}
		for _, o := range g.Options {
			if o.ID == 0 {
				if err := insertModifierOption(ctx, tx, g.ID, o); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(ctx, `
				UPDATE modifier_options SET name=$3, description=$4, price=$5, active=$6, sort_index=$7, updated_at=now()
				WHERE group_id=$1 AND id=$2`, g.ID, o.ID, o.Name, o.Description, o.Price, o.Active, o.SortIndex); err != nil {
				return err
			}
		}
		out, err = getModifierGroup(ctx, tx, g.ID)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.ModifierGroups: {out.ID}})
	}
	return out, wrapGroup(err, "Could not update the modifier group")
}

func (s *Store) DeleteModifierGroup(ctx context.Context, id int64) error {
	return deleteGroup(ctx, s, "modifier", id)
}

// wrapGroup keeps an *apperr.Error as is, tells a name clash apart from a failure, and otherwise reports an internal error.
func wrapGroup(err error, msg string) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return apperr.Conflict("A group or an option with that name already exists")
	}
	return apperr.Internal(msg).WithCause(err)
}

// droppedIDs are the option ids a group had that its update no longer lists.
func droppedIDs(existing map[int64]bool, kept []int64) []int64 {
	keep := map[int64]bool{}
	for _, id := range kept {
		keep[id] = true
	}
	var out []int64
	for id := range existing {
		if !keep[id] {
			out = append(out, id)
		}
	}
	return out
}
