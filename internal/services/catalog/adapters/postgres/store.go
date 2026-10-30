// Package postgres adapts catalog's Store port to Postgres via pgx. Every write
// appends its event to the outbox in the same transaction.
package postgres

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/events"
	"github.com/koobam/wuzz-backend/internal/pkg/outbox"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/adapters/searchindex"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type Store struct {
	pool *postgres.Pool
	// ix is the search index; nil lists straight from Postgres (see search.go).
	ix  *searchindex.Index
	log *slog.Logger
}

func NewStore(pool *postgres.Pool) *Store { return &Store{pool: pool} }

const outletCols = `id, name, address, phone, email, latitude, longitude, active, images, category, delivery_types, open_time, close_time, timezone, version, created_at, updated_at`
const serviceCols = `id, name, unit, price, active, COALESCE(category_id, 0), icon, description, tax_type, internal_notes, discount_kind, discount_value, version, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanOutlet(r scanner) (domain.Outlet, error) {
	var o domain.Outlet
	err := r.Scan(&o.ID, &o.Name, &o.Address, &o.Phone, &o.Email, &o.Latitude, &o.Longitude, &o.Active, &o.Images, &o.Category, &o.DeliveryTypes, &o.OpenTime, &o.CloseTime, &o.Timezone, &o.Version, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, apperr.NotFound("Outlet not found")
	}
	if err != nil {
		return o, apperr.Internal("Could not load the outlet").WithCause(err)
	}
	return o, nil
}

func scanService(r scanner) (domain.ServiceItem, error) {
	var s domain.ServiceItem
	err := r.Scan(&s.ID, &s.Name, &s.Unit, &s.Price, &s.Active, &s.CategoryID, &s.Icon, &s.Description, &s.TaxType, &s.InternalNotes, &s.DiscountType, &s.DiscountValue, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, apperr.NotFound("Service not found")
	}
	if err != nil {
		return s, apperr.Internal("Could not load the service").WithCause(err)
	}
	return s, nil
}

func (s *Store) ListOutlets(ctx context.Context, ids []int64, includeInactive bool, within *domain.Bounds) ([]domain.Outlet, error) {
	// A nil box is "everywhere": the four bounds are then NULL and the last condition holds for every row.
	var south, west, north, east *float64
	if within != nil {
		south, west, north, east = &within.South, &within.West, &within.North, &within.East
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+outletCols+` FROM outlets
		WHERE ($1 OR active) AND (coalesce(cardinality($2::bigint[]), 0) = 0 OR id = ANY($2))
		  AND ($3::float8 IS NULL OR (latitude BETWEEN $3 AND $5 AND longitude BETWEEN $4 AND $6))
		ORDER BY name`, includeInactive, ids, south, west, north, east)
	if err != nil {
		return nil, apperr.Internal("Could not list outlets").WithCause(err)
	}
	defer rows.Close()
	var out []domain.Outlet
	for rows.Next() {
		o, err := scanOutlet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return s.attachHours(ctx, out)
}

func (s *Store) GetOutlet(ctx context.Context, id int64) (domain.Outlet, error) {
	o, err := scanOutlet(s.pool.QueryRow(ctx, `SELECT `+outletCols+` FROM outlets WHERE id = $1`, id))
	if err != nil {
		return o, err
	}
	withHours, err := s.attachHours(ctx, []domain.Outlet{o})
	if err != nil {
		return o, err
	}
	return withHours[0], nil
}

// attachHours fills each outlet's week (Monday to Sunday) from outlet_hours, in one query. An outlet that has no rows (none should
// be: the migration filled them and every write keeps seven) gets its single daily range on all seven days.
func (s *Store) attachHours(ctx context.Context, outlets []domain.Outlet) ([]domain.Outlet, error) {
	if len(outlets) == 0 {
		return outlets, nil
	}
	ids := make([]int64, len(outlets))
	for i, o := range outlets {
		ids[i] = o.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT outlet_id, weekday, closed, open_24h, open_time, close_time FROM outlet_hours WHERE outlet_id = ANY($1) ORDER BY outlet_id, weekday`, ids)
	if err != nil {
		return nil, apperr.Internal("Could not load the opening hours").WithCause(err)
	}
	defer rows.Close()
	byOutlet := map[int64][]domain.DayHours{}
	for rows.Next() {
		var id int64
		var h domain.DayHours
		var weekday int16
		if err := rows.Scan(&id, &weekday, &h.Closed, &h.Open24h, &h.OpenTime, &h.CloseTime); err != nil {
			return nil, apperr.Internal("Could not load the opening hours").WithCause(err)
		}
		h.Weekday = int(weekday)
		byOutlet[id] = append(byOutlet[id], h)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("Could not load the opening hours").WithCause(err)
	}
	for i := range outlets {
		if hours := byOutlet[outlets[i].ID]; len(hours) == 7 {
			outlets[i].Hours = hours
		} else {
			outlets[i].Hours = domain.SameEveryDay(outlets[i].OpenTime, outlets[i].CloseTime)
		}
	}
	return outlets, nil
}

// saveHours replaces an outlet's seven days with these.
func saveHours(ctx context.Context, tx pgx.Tx, outletID int64, hours []domain.DayHours) error {
	if _, err := tx.Exec(ctx, `DELETE FROM outlet_hours WHERE outlet_id = $1`, outletID); err != nil {
		return err
	}
	for _, h := range hours {
		if _, err := tx.Exec(ctx, `INSERT INTO outlet_hours (outlet_id, weekday, closed, open_24h, open_time, close_time) VALUES ($1, $2, $3, $4, $5, $6)`,
			outletID, h.Weekday, h.Closed, h.Open24h, h.OpenTime, h.CloseTime); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error) {
	var created domain.Outlet
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		created, err = scanOutlet(tx.QueryRow(ctx, `
			INSERT INTO outlets (name, address, phone, email, latitude, longitude, active, images, category, delivery_types, open_time, close_time, timezone)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING `+outletCols,
			o.Name, o.Address, o.Phone, o.Email, o.Latitude, o.Longitude, o.Active, images(o.Images), o.Category, images(o.DeliveryTypes), o.OpenTime, o.CloseTime, o.Timezone))
		if err != nil {
			return err
		}
		if err := saveHours(ctx, tx, created.ID, o.Hours); err != nil {
			return err
		}
		created.Hours = o.Hours
		return emitOutlet(ctx, tx, events.TypeOutletCreated, created)
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Outlets: {created.ID}})
	}
	return created, wrap(err, "Could not create the outlet")
}

func (s *Store) UpdateOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error) {
	var updated domain.Outlet
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		updated, err = scanOutlet(tx.QueryRow(ctx, `
			UPDATE outlets SET name=$2, address=$3, phone=$4, email=$14, latitude=$5, longitude=$6, active=$7, images=$8,
			       category=$9, delivery_types=$10, open_time=$11, close_time=$12, timezone=$13,
			       version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+outletCols,
			o.ID, o.Name, o.Address, o.Phone, o.Latitude, o.Longitude, o.Active, images(o.Images), o.Category, images(o.DeliveryTypes), o.OpenTime, o.CloseTime, o.Timezone, o.Email))
		if err != nil {
			return err
		}
		if err := saveHours(ctx, tx, updated.ID, o.Hours); err != nil {
			return err
		}
		updated.Hours = o.Hours
		return emitOutlet(ctx, tx, events.TypeOutletUpdated, updated)
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Outlets: {updated.ID}})
	}
	return updated, wrap(err, "Could not update the outlet")
}

// DeleteOutlet removes an outlet and its opening hours. An outlet that has stock records (balances or movements: the ledger is
// immutable and keeps pointing at it) is refused with a conflict; the caller switches it off instead. Staff, admins and orders live in
// other services; the gateway checks those before it asks.
func (s *Store) DeleteOutlet(ctx context.Context, id int64) error {
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outlets WHERE id = $1 FOR UPDATE)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return apperr.NotFound("Outlet not found")
		}
		var stock bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stock_balances WHERE outlet_id = $1) OR EXISTS (SELECT 1 FROM stock_movements WHERE outlet_id = $1)`, id).Scan(&stock); err != nil {
			return err
		}
		if stock {
			return apperr.Conflict("This outlet has stock records and cannot be deleted; switch it off instead")
		}
		if err := refuseIfBannersNeed(ctx, tx, domain.TargetOutlet, id); err != nil {
			return err
		}
		if err := refuseIfVouchersNeed(ctx, tx, domain.TargetOutlet, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM outlets WHERE id = $1`, id); err != nil {
			return err
		}
		env, err := events.New(events.TypeOutletDeleted, events.AggregateOutlet, itoa(id), 0, events.OutletDeleted{ID: id})
		if err != nil {
			return err
		}
		return outbox.Append(ctx, tx, env)
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Outlets: {id}})
	}
	return wrap(err, "Could not delete the outlet")
}

func emitOutlet(ctx context.Context, tx pgx.Tx, typ string, o domain.Outlet) error {
	env, err := events.New(typ, events.AggregateOutlet, itoa(o.ID), o.Version, events.OutletChanged{
		ID: o.ID, Name: o.Name, Address: o.Address, Phone: o.Phone,
		Latitude: o.Latitude, Longitude: o.Longitude, Active: o.Active,
	})
	if err != nil {
		return err
	}
	return outbox.Append(ctx, tx, env)
}

// images is never nil, so a database that has no pictures for an outlet stores an empty list rather than NULL.
func images(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func (s *Store) ListServiceCategories(ctx context.Context) ([]domain.ServiceCategory, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, icon, sort_order FROM service_categories ORDER BY sort_order, name`)
	if err != nil {
		return nil, apperr.Internal("Could not list service categories").WithCause(err)
	}
	defer rows.Close()
	var out []domain.ServiceCategory
	for rows.Next() {
		var c domain.ServiceCategory
		if err := rows.Scan(&c.ID, &c.Name, &c.Icon, &c.SortOrder); err != nil {
			return nil, apperr.Internal("Could not list service categories").WithCause(err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListServices(ctx context.Context, includeInactive bool) ([]domain.ServiceItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+serviceCols+` FROM services WHERE ($1 OR active) ORDER BY name`, includeInactive)
	if err != nil {
		return nil, apperr.Internal("Could not list services").WithCause(err)
	}
	defer rows.Close()
	var out []domain.ServiceItem
	for rows.Next() {
		it, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("Could not list services").WithCause(err)
	}
	rows.Close()
	if err := applyTax(ctx, s.pool, out); err != nil {
		return nil, err
	}
	return out, attachGroups(ctx, s.pool, out)
}

func (s *Store) CreateService(ctx context.Context, it domain.ServiceItem) (domain.ServiceItem, error) {
	var created domain.ServiceItem
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		created, err = scanService(tx.QueryRow(ctx, `
			INSERT INTO services (name, unit, price, active, category_id, tax_type, internal_notes, discount_kind, discount_value) VALUES ($1, $2, $3, $4, NULLIF($5::bigint, 0), COALESCE(NULLIF($6, ''), 'none'), $7, COALESCE(NULLIF($8, ''), 'none'), $9)
			RETURNING `+serviceCols, it.Name, it.Unit, it.Price, it.Active, it.CategoryID, it.TaxType, it.InternalNotes, it.DiscountType, it.DiscountValue))
		if err != nil {
			return err
		}
		if err := setServiceLinks(ctx, tx, created.ID, it.VariantGroupIDs, it.ModifierGroupIDs); err != nil {
			return err
		}
		items := []domain.ServiceItem{created}
		if err := applyTax(ctx, tx, items); err != nil {
			return err
		}
		if err := attachGroups(ctx, tx, items); err != nil {
			return err
		}
		created = items[0]
		return emitService(ctx, tx, events.TypeServiceCreated, created)
	})
	if err == nil {
		s.syncService(ctx, created.ID, nil, nil)
	}
	return created, wrap(err, "Could not create the service")
}

func (s *Store) UpdateService(ctx context.Context, it domain.ServiceItem) (domain.ServiceItem, error) {
	var updated domain.ServiceItem
	// The groups it offered before: when the links change, those groups' "used by" changes too.
	oldVariants, oldModifiers := s.linkedGroupIDs(ctx, it.ID)
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		updated, err = scanService(tx.QueryRow(ctx, `
			UPDATE services SET name=$2, unit=$3, price=$4, active=$5, category_id=NULLIF($6::bigint, 0), tax_type=$7, internal_notes=$8, discount_kind=$9, discount_value=$10, version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+serviceCols, it.ID, it.Name, it.Unit, it.Price, it.Active, it.CategoryID, it.TaxType, it.InternalNotes, it.DiscountType, it.DiscountValue))
		if err != nil {
			return err
		}
		if err := setServiceLinks(ctx, tx, updated.ID, it.VariantGroupIDs, it.ModifierGroupIDs); err != nil {
			return err
		}
		items := []domain.ServiceItem{updated}
		if err := applyTax(ctx, tx, items); err != nil {
			return err
		}
		if err := attachGroups(ctx, tx, items); err != nil {
			return err
		}
		updated = items[0]
		return emitService(ctx, tx, events.TypeServiceUpdated, updated)
	})
	if err == nil {
		s.syncService(ctx, updated.ID, oldVariants, oldModifiers)
	}
	return updated, wrap(err, "Could not update the service")
}

// DeleteService removes a service with its links to variant and modifier groups (foreign keys) and its own recipe lines (the owner
// id is not a foreign key). The groups stay, the other recipes stay, and orders keep their own copy of what they sold, so history is untouched.
func (s *Store) DeleteService(ctx context.Context, id int64) error {
	// The groups it offered: their "used by" changes with it.
	oldVariants, oldModifiers := s.linkedGroupIDs(ctx, id)
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM services WHERE id = $1 FOR UPDATE)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return apperr.NotFound("Service not found")
		}
		if err := refuseIfBannersNeed(ctx, tx, domain.TargetService, id); err != nil {
			return err
		}
		if err := refuseIfVouchersNeed(ctx, tx, domain.TargetService, id); err != nil {
			return err
		}
		if err := deleteOptionBOM(ctx, tx, "service", []int64{id}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM services WHERE id = $1`, id); err != nil {
			return err
		}
		env, err := events.New(events.TypeServiceDeleted, events.AggregateService, itoa(id), 0, events.ServiceDeleted{ID: id})
		if err != nil {
			return err
		}
		return outbox.Append(ctx, tx, env)
	})
	if err == nil {
		s.syncService(ctx, id, oldVariants, oldModifiers)
	}
	return wrap(err, "Could not delete the service")
}

func emitService(ctx context.Context, tx pgx.Tx, typ string, it domain.ServiceItem) error {
	env, err := events.New(typ, events.AggregateService, itoa(it.ID), it.Version, events.ServiceChanged{
		ID: it.ID, Name: it.Name, Unit: it.Unit, Price: it.Price, Active: it.Active,
	})
	if err != nil {
		return err
	}
	return outbox.Append(ctx, tx, env)
}

// wrap keeps an *apperr.Error as is and turns anything else into a conflict
// (unique violation) or an internal error.
func wrap(err error, msg string) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return apperr.Conflict("That name is already in use")
	}
	if errors.As(err, &pg) && pg.Code == "23503" {
		return apperr.Invalid("That category does not exist")
	}
	return apperr.Internal(msg).WithCause(err)
}
