// Package postgres adapts catalog's Store port to Postgres via pgx. Every write
// appends its event to the outbox in the same transaction.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/events"
	"github.com/koobam/wuzz-backend/internal/pkg/outbox"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type Store struct{ pool *postgres.Pool }

func NewStore(pool *postgres.Pool) *Store { return &Store{pool: pool} }

const outletCols = `id, name, address, phone, latitude, longitude, active, version, created_at, updated_at`
const serviceCols = `id, name, unit, price, active, version, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanOutlet(r scanner) (domain.Outlet, error) {
	var o domain.Outlet
	err := r.Scan(&o.ID, &o.Name, &o.Address, &o.Phone, &o.Latitude, &o.Longitude, &o.Active, &o.Version, &o.CreatedAt, &o.UpdatedAt)
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
	err := r.Scan(&s.ID, &s.Name, &s.Unit, &s.Price, &s.Active, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, apperr.NotFound("Service not found")
	}
	if err != nil {
		return s, apperr.Internal("Could not load the service").WithCause(err)
	}
	return s, nil
}

func (s *Store) ListOutlets(ctx context.Context, ids []int64, includeInactive bool) ([]domain.Outlet, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+outletCols+` FROM outlets
		WHERE ($1 OR active) AND (coalesce(cardinality($2::bigint[]), 0) = 0 OR id = ANY($2))
		ORDER BY name`, includeInactive, ids)
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
	return out, rows.Err()
}

func (s *Store) GetOutlet(ctx context.Context, id int64) (domain.Outlet, error) {
	return scanOutlet(s.pool.QueryRow(ctx, `SELECT `+outletCols+` FROM outlets WHERE id = $1`, id))
}

func (s *Store) CreateOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error) {
	var created domain.Outlet
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		created, err = scanOutlet(tx.QueryRow(ctx, `
			INSERT INTO outlets (name, address, phone, latitude, longitude, active)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+outletCols,
			o.Name, o.Address, o.Phone, o.Latitude, o.Longitude, o.Active))
		if err != nil {
			return err
		}
		return emitOutlet(ctx, tx, events.TypeOutletCreated, created)
	})
	return created, wrap(err, "Could not create the outlet")
}

func (s *Store) UpdateOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error) {
	var updated domain.Outlet
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		updated, err = scanOutlet(tx.QueryRow(ctx, `
			UPDATE outlets SET name=$2, address=$3, phone=$4, latitude=$5, longitude=$6, active=$7,
			       version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+outletCols,
			o.ID, o.Name, o.Address, o.Phone, o.Latitude, o.Longitude, o.Active))
		if err != nil {
			return err
		}
		return emitOutlet(ctx, tx, events.TypeOutletUpdated, updated)
	})
	return updated, wrap(err, "Could not update the outlet")
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
	return out, rows.Err()
}

func (s *Store) CreateService(ctx context.Context, it domain.ServiceItem) (domain.ServiceItem, error) {
	var created domain.ServiceItem
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		created, err = scanService(tx.QueryRow(ctx, `
			INSERT INTO services (name, unit, price, active) VALUES ($1, $2, $3, $4)
			RETURNING `+serviceCols, it.Name, it.Unit, it.Price, it.Active))
		if err != nil {
			return err
		}
		return emitService(ctx, tx, events.TypeServiceCreated, created)
	})
	return created, wrap(err, "Could not create the service")
}

func (s *Store) UpdateService(ctx context.Context, it domain.ServiceItem) (domain.ServiceItem, error) {
	var updated domain.ServiceItem
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		updated, err = scanService(tx.QueryRow(ctx, `
			UPDATE services SET name=$2, unit=$3, price=$4, active=$5, version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+serviceCols, it.ID, it.Name, it.Unit, it.Price, it.Active))
		if err != nil {
			return err
		}
		return emitService(ctx, tx, events.TypeServiceUpdated, updated)
	})
	return updated, wrap(err, "Could not update the service")
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
	return apperr.Internal(msg).WithCause(err)
}
