package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/adapters/searchindex"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Quantities are numeric columns read and written as text, never as floats: a value goes through a big.Rat and back to the
// exact decimal text, so nothing is rounded on the way. Costs are derived from the exact fractions, not stored.

func ratFromText(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("not a number: %q", s)
	}
	return r, nil
}

const materialCols = `m.id, m.sku, m.name, m.category, m.unit, m.purchase_price, m.purchase_qty::text, m.active, m.notes,
	(SELECT count(*) FROM bom_lines b WHERE b.material_id = m.id), m.created_at, m.updated_at`

func scanMaterial(r pgx.Row) (domain.Material, error) {
	var m domain.Material
	var qty string
	var used int64
	err := r.Scan(&m.ID, &m.SKU, &m.Name, &m.Category, &m.Unit, &m.PurchasePrice, &qty, &m.Active, &m.Notes, &used, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, apperr.NotFound("Material not found")
	}
	if err != nil {
		return m, internal("Could not read the material", err)
	}
	m.UsedIn = int32(used)
	if m.PurchaseQty, err = ratFromText(qty); err != nil {
		return m, internal("Could not read the material", err)
	}
	return m, nil
}

var materialOrder = map[string]string{"id": "m.id", "sku": "lower(m.sku)", "name": "lower(m.name)", "created_at": "m.created_at", "updated_at": "m.updated_at"}

func (s *Store) ListMaterials(ctx context.Context, f domain.MaterialQuery) ([]domain.Material, int64, error) {
	if s.ix.Enabled() {
		return s.searchMaterials(ctx, f)
	}
	return s.listMaterialsSQL(ctx, f)
}

func (s *Store) listMaterialsSQL(ctx context.Context, f domain.MaterialQuery) ([]domain.Material, int64, error) {
	where := `($1 = '' OR strpos(lower(m.sku), lower($1)) > 0 OR strpos(lower(m.name), lower($1)) > 0 OR strpos(lower(m.notes), lower($1)) > 0)
		AND ($2 = '' OR m.category = $2) AND ($3 = '' OR m.active = ($3 = 'true'))`
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM materials m WHERE `+where, f.Q, f.Category, f.Active).Scan(&total); err != nil {
		return nil, 0, internal("Could not list materials", err)
	}
	dir := "ASC"
	if f.SortDir == "desc" {
		dir = "DESC"
	}
	by, ok := materialOrder[f.SortBy]
	if !ok {
		by = materialOrder["id"]
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM materials m WHERE %s ORDER BY %s %s, m.id ASC LIMIT $4 OFFSET $5`, materialCols, where, by, dir),
		f.Q, f.Category, f.Active, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, internal("Could not list materials", err)
	}
	defer rows.Close()
	out := []domain.Material{}
	for rows.Next() {
		m, err := scanMaterial(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (s *Store) GetMaterial(ctx context.Context, id int64) (domain.Material, error) {
	return getMaterial(ctx, s.pool, id)
}

func getMaterial(ctx context.Context, q queryer, id int64) (domain.Material, error) {
	return scanMaterial(q.QueryRow(ctx, `SELECT `+materialCols+` FROM materials m WHERE m.id = $1`, id))
}

func (s *Store) CreateMaterial(ctx context.Context, m domain.Material) (domain.Material, error) {
	var out domain.Material
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO materials (sku, name, category, unit, purchase_price, purchase_qty, active, notes)
			VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8) RETURNING id`,
			m.SKU, m.Name, m.Category, m.Unit, m.PurchasePrice, domain.FormatRat(m.PurchaseQty, 4), m.Active, m.Notes).Scan(&id); err != nil {
			return err
		}
		var err error
		out, err = getMaterial(ctx, tx, id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Materials: {out.ID}})
	}
	return out, wrapMaterial(err, "Could not create the material")
}

// UpdateMaterial replaces the material. The base unit cannot change while a recipe uses the material: its quantities would
// silently mean something else.
func (s *Store) UpdateMaterial(ctx context.Context, m domain.Material) (domain.Material, error) {
	var out domain.Material
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var unit string
		var used, moved int64
		if err := tx.QueryRow(ctx, `SELECT unit, (SELECT count(*) FROM bom_lines WHERE material_id = $1), (SELECT count(*) FROM stock_movements WHERE material_id = $1)
			FROM materials WHERE id = $1 FOR UPDATE`, m.ID).Scan(&unit, &used, &moved); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("Material not found")
			}
			return err
		}
		if unit != m.Unit && moved > 0 {
			return apperr.Conflict("The unit cannot change once the material has stock movements")
		}
		if unit != m.Unit && used > 0 {
			return apperr.Conflict(fmt.Sprintf("The unit cannot change while the material is in %d recipe lines", used))
		}
		if _, err := tx.Exec(ctx, `
			UPDATE materials SET sku=$2, name=$3, category=$4, unit=$5, purchase_price=$6, purchase_qty=$7::numeric, active=$8, notes=$9, updated_at=now()
			WHERE id=$1`, m.ID, m.SKU, m.Name, m.Category, m.Unit, m.PurchasePrice, domain.FormatRat(m.PurchaseQty, 4), m.Active, m.Notes); err != nil {
			return err
		}
		var err error
		out, err = getMaterial(ctx, tx, m.ID)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Materials: {out.ID}})
	}
	return out, wrapMaterial(err, "Could not update the material")
}

// DeleteMaterial refuses while any recipe line uses the material, naming up to five owners.
func (s *Store) DeleteMaterial(ctx context.Context, id int64) error {
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM materials WHERE id = $1 FOR UPDATE)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return apperr.NotFound("Material not found")
		}
		var total, moved int64
		if err := tx.QueryRow(ctx, `SELECT count(*), (SELECT count(*) FROM stock_movements WHERE material_id = $1) FROM bom_lines WHERE material_id = $1`, id).Scan(&total, &moved); err != nil {
			return err
		}
		if moved > 0 {
			return apperr.Conflict("The material has stock history and cannot be deleted; make it inactive instead")
		}
		if total > 0 {
			names, err := bomOwnerNames(ctx, tx, id, 5)
			if err != nil {
				return err
			}
			return apperr.Conflict(domain.UsedByMessage(names, int(total)))
		}
		_, err := tx.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Materials: {id}})
	}
	return wrapMaterial(err, "Could not delete the material")
}

// bomOwnerNames names the owners of a material's recipe lines: a service's name, or "<group> / <option>".
func bomOwnerNames(ctx context.Context, q queryer, materialID int64, limit int) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT CASE b.owner_type WHEN 'service' THEN s.name
		                         WHEN 'variant_option' THEN vg.name || ' / ' || vo.name
		                         ELSE mg.name || ' / ' || mo.name END AS owner
		FROM bom_lines b
		LEFT JOIN services s ON b.owner_type = 'service' AND s.id = b.owner_id
		LEFT JOIN variant_options vo ON b.owner_type = 'variant_option' AND vo.id = b.owner_id
		LEFT JOIN variant_groups vg ON vg.id = vo.group_id
		LEFT JOIN modifier_options mo ON b.owner_type = 'modifier_option' AND mo.id = b.owner_id
		LEFT JOIN modifier_groups mg ON mg.id = mo.group_id
		WHERE b.material_id = $1 ORDER BY owner, b.id LIMIT $2`, materialID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n *string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		if n != nil {
			out = append(out, *n)
		} else {
			out = append(out, "an item that no longer exists")
		}
	}
	return out, rows.Err()
}

func wrapMaterial(err error, msg string) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return apperr.Conflict("A material with that SKU already exists")
		case "23503":
			return apperr.Conflict("The material is still used by a recipe")
		}
	}
	return apperr.Internal(msg).WithCause(err)
}

// ---- recipes ----

func ownerExists(ctx context.Context, q queryer, ownerType string, id int64) error {
	table, label := "", ""
	switch ownerType {
	case domain.OwnerService:
		table, label = "services", "Service"
	case domain.OwnerVariantOption:
		table, label = "variant_options", "Variant option"
	case domain.OwnerModifierOption:
		table, label = "modifier_options", "Modifier option"
	default:
		return apperr.Invalid("The owner type must be service, variant_option or modifier_option")
	}
	var ok bool
	if err := q.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1)`, table), id).Scan(&ok); err != nil {
		return internal("Could not look up the owner", err)
	}
	if !ok {
		return apperr.NotFound(label + " not found")
	}
	return nil
}

// loadBOM reads the recipe lines of the given owners of one type, with each material's exact unit cost.
func loadBOM(ctx context.Context, q queryer, ownerType string, ownerIDs []int64) (map[int64][]domain.BOMLine, error) {
	out := map[int64][]domain.BOMLine{}
	if len(ownerIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT b.owner_id, b.material_id, m.sku, m.name, m.unit, b.qty::text, b.per, b.loss_percent::text, b.sort_index, m.purchase_price, m.purchase_qty::text
		FROM bom_lines b JOIN materials m ON m.id = b.material_id
		WHERE b.owner_type = $1 AND b.owner_id = ANY($2) ORDER BY b.owner_id, b.sort_index, b.id`, ownerType, ownerIDs)
	if err != nil {
		return nil, internal("Could not read the recipe", err)
	}
	defer rows.Close()
	for rows.Next() {
		var owner, price int64
		var qty, loss, buyQty string
		l := domain.BOMLine{}
		if err := rows.Scan(&owner, &l.MaterialID, &l.SKU, &l.Name, &l.Unit, &qty, &l.Per, &loss, &l.SortIndex, &price, &buyQty); err != nil {
			return nil, internal("Could not read the recipe", err)
		}
		var errs [3]error
		l.Qty, errs[0] = ratFromText(qty)
		l.LossPercent, errs[1] = ratFromText(loss)
		bq, e := ratFromText(buyQty)
		errs[2] = e
		for _, e := range errs {
			if e != nil {
				return nil, internal("Could not read the recipe", e)
			}
		}
		l.UnitCost = domain.Material{PurchasePrice: price, PurchaseQty: bq}.UnitCost()
		out[owner] = append(out[owner], l)
	}
	return out, rows.Err()
}

func (s *Store) GetBOM(ctx context.Context, ownerType string, ownerID int64) ([]domain.BOMLine, error) {
	if err := ownerExists(ctx, s.pool, ownerType, ownerID); err != nil {
		return nil, err
	}
	m, err := loadBOM(ctx, s.pool, ownerType, []int64{ownerID})
	return m[ownerID], err
}

// SetBOM replaces the owner's recipe. A material that is inactive may stay on a line it is already on but cannot be newly added.
func (s *Store) SetBOM(ctx context.Context, ownerType string, ownerID int64, lines []domain.BOMLine) ([]domain.BOMLine, error) {
	var out []domain.BOMLine
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := ownerExists(ctx, tx, ownerType, ownerID); err != nil {
			return err
		}
		ids := make([]int64, 0, len(lines))
		for _, l := range lines {
			ids = append(ids, l.MaterialID)
		}
		active := map[int64]bool{}
		skus := map[int64]string{}
		rows, err := tx.Query(ctx, `SELECT id, sku, active FROM materials WHERE id = ANY($1)`, ids)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var sku string
			var a bool
			if err := rows.Scan(&id, &sku, &a); err != nil {
				rows.Close()
				return err
			}
			active[id], skus[id] = a, sku
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		had := map[int64]bool{}
		old, err := tx.Query(ctx, `SELECT material_id FROM bom_lines WHERE owner_type = $1 AND owner_id = $2`, ownerType, ownerID)
		if err != nil {
			return err
		}
		for old.Next() {
			var id int64
			if err := old.Scan(&id); err != nil {
				old.Close()
				return err
			}
			had[id] = true
		}
		old.Close()
		for _, l := range lines {
			a, found := active[l.MaterialID]
			switch {
			case !found:
				return apperr.Invalid(fmt.Sprintf("Material %d does not exist", l.MaterialID))
			case !a && !had[l.MaterialID]:
				return apperr.Invalid(fmt.Sprintf("Material %s is inactive and cannot be added to a recipe", skus[l.MaterialID]))
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM bom_lines WHERE owner_type = $1 AND owner_id = $2`, ownerType, ownerID); err != nil {
			return err
		}
		for i, l := range lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO bom_lines (owner_type, owner_id, material_id, qty, per, loss_percent, sort_index)
				VALUES ($1, $2, $3, $4::numeric, $5, $6::numeric, $7)`,
				ownerType, ownerID, l.MaterialID, domain.FormatRat(l.Qty, 4), l.Per, domain.FormatRat(l.LossPercent, 2), i+1); err != nil {
				return err
			}
		}
		m, err := loadBOM(ctx, tx, ownerType, []int64{ownerID})
		out = m[ownerID]
		return err
	})
	return out, wrapMaterial(err, "Could not save the recipe")
}

// LoadCostingContext reads a service with its linked groups, its recipe and the recipe of every option it offers.
func (s *Store) LoadCostingContext(ctx context.Context, serviceID int64) (domain.CostingContext, error) {
	var c domain.CostingContext
	svc, err := scanService(s.pool.QueryRow(ctx, `SELECT `+serviceCols+` FROM services WHERE id = $1`, serviceID))
	if err != nil {
		return c, err
	}
	items := []domain.ServiceItem{svc}
	if err := attachGroups(ctx, s.pool, items); err != nil {
		return c, err
	}
	c.Service = items[0]
	base, err := loadBOM(ctx, s.pool, domain.OwnerService, []int64{serviceID})
	if err != nil {
		return c, err
	}
	c.Base = base[serviceID]
	var vIDs, mIDs []int64
	for _, g := range c.Service.VariantGroups {
		for _, o := range g.Options {
			vIDs = append(vIDs, o.ID)
		}
	}
	for _, g := range c.Service.ModifierGroups {
		for _, o := range g.Options {
			mIDs = append(mIDs, o.ID)
		}
	}
	if c.Variant, err = loadBOM(ctx, s.pool, domain.OwnerVariantOption, vIDs); err != nil {
		return c, err
	}
	c.Modifier, err = loadBOM(ctx, s.pool, domain.OwnerModifierOption, mIDs)
	return c, err
}

// deleteOptionBOM removes the recipe lines of options that are going away; an option's id is not a foreign key anywhere.
func deleteOptionBOM(ctx context.Context, q queryer, ownerType string, optionIDs []int64) error {
	if len(optionIDs) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `DELETE FROM bom_lines WHERE owner_type = $1 AND owner_id = ANY($2)`, ownerType, optionIDs)
	return err
}
