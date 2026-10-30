package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/adapters/searchindex"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/command"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Vouchers and the wallets that hold them. The back office's list is answered by the index when there is one (search.go); the customer's
// wallet, the grants and the audit are answered by Postgres alone: they are per customer or per voucher, and must never be behind a write.

const voucherCols = `v.id, v.code, v.title, v.description, v.internal_notes, v.discount_type, v.value, v.max_discount, v.min_spend, v.max_claims, v.usage_limit,
	v.status, v.color, v.image_url, v.starts_at, v.ends_at, v.created_at, v.updated_at`

func scanVoucher(r pgx.Row) (domain.Voucher, error) {
	var v domain.Voucher
	err := r.Scan(&v.ID, &v.Code, &v.Title, &v.Description, &v.InternalNotes, &v.DiscountType, &v.Value, &v.MaxDiscount, &v.MinSpend, &v.MaxClaims, &v.UsageLimit,
		&v.Status, &v.Color, &v.ImageURL, &v.StartsAt, &v.EndsAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, apperr.NotFound("Voucher not found")
	}
	if err != nil {
		return v, internal("Could not read the voucher", err)
	}
	return v, nil
}

// attachVoucherRefs fills the outlet and service limits and the claim count of vouchers, in place.
func attachVoucherRefs(ctx context.Context, q queryer, items []domain.Voucher) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	at := make(map[int64]int, len(items))
	for i, v := range items {
		ids[i], at[v.ID] = v.ID, i
		items[i].OutletIDs, items[i].Outlets, items[i].ServiceIDs, items[i].Services, items[i].ClaimsCount = nil, nil, nil, nil, 0
	}
	rows, err := q.Query(ctx, `SELECT vo.voucher_id, o.id, o.name FROM voucher_outlets vo JOIN outlets o ON o.id = vo.outlet_id
		WHERE vo.voucher_id = ANY($1) ORDER BY lower(o.name), o.id`, ids)
	if err != nil {
		return internal("Could not read the vouchers' outlets", err)
	}
	for rows.Next() {
		var voucher int64
		var o domain.OutletRef
		if err := rows.Scan(&voucher, &o.ID, &o.Name); err != nil {
			rows.Close()
			return internal("Could not read the vouchers' outlets", err)
		}
		i := at[voucher]
		items[i].OutletIDs = append(items[i].OutletIDs, o.ID)
		items[i].Outlets = append(items[i].Outlets, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return internal("Could not read the vouchers' outlets", err)
	}
	rows, err = q.Query(ctx, `SELECT vs.voucher_id, s.id, s.name FROM voucher_services vs JOIN services s ON s.id = vs.service_id
		WHERE vs.voucher_id = ANY($1) ORDER BY lower(s.name), s.id`, ids)
	if err != nil {
		return internal("Could not read the vouchers' services", err)
	}
	for rows.Next() {
		var voucher int64
		var s domain.ServiceRef
		if err := rows.Scan(&voucher, &s.ID, &s.Name); err != nil {
			rows.Close()
			return internal("Could not read the vouchers' services", err)
		}
		i := at[voucher]
		items[i].ServiceIDs = append(items[i].ServiceIDs, s.ID)
		items[i].Services = append(items[i].Services, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return internal("Could not read the vouchers' services", err)
	}
	rows, err = q.Query(ctx, `SELECT voucher_id, count(*) FROM voucher_grants WHERE voucher_id = ANY($1) GROUP BY voucher_id`, ids)
	if err != nil {
		return internal("Could not count the vouchers' claims", err)
	}
	defer rows.Close()
	for rows.Next() {
		var voucher, n int64
		if err := rows.Scan(&voucher, &n); err != nil {
			return internal("Could not count the vouchers' claims", err)
		}
		items[at[voucher]].ClaimsCount = n
	}
	return rows.Err()
}

func readVouchers(ctx context.Context, q queryer, rows pgx.Rows) ([]domain.Voucher, error) {
	defer rows.Close()
	out := []domain.Voucher{}
	for rows.Next() {
		v, err := scanVoucher(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, internal("Could not read the vouchers", err)
	}
	rows.Close()
	return out, attachVoucherRefs(ctx, q, out)
}

var voucherOrder = map[string]string{
	"id": "v.id", "title": "lower(v.title)", "code": "v.code", "status": "v.status", "value": "v.value",
	"claims":     "(SELECT count(*) FROM voucher_grants g WHERE g.voucher_id = v.id)",
	"starts_at":  "COALESCE(extract(epoch FROM v.starts_at), 0)",
	"ends_at":    "COALESCE(extract(epoch FROM v.ends_at), 253402300799)",
	"created_at": "v.created_at", "updated_at": "v.updated_at",
}

// voucherLiveSQL is the test for "usable at $n": the same one as domain.Voucher.IsLive.
func voucherLiveSQL(n int) string {
	return fmt.Sprintf(`(v.status = 'active' AND (v.starts_at IS NULL OR v.starts_at <= $%[1]d) AND (v.ends_at IS NULL OR v.ends_at > $%[1]d))`, n)
}

func (s *Store) ListVouchers(ctx context.Context, q domain.VoucherQuery) ([]domain.Voucher, int64, error) {
	if s.ix.Enabled() {
		res, err := s.ix.Search(ctx, searchindex.Vouchers, searchindex.VoucherQuery(q))
		if err != nil {
			return nil, 0, err
		}
		out, err := s.vouchersByIDs(ctx, res.IDs)
		return out, res.Total, err
	}
	return s.listVouchersSQL(ctx, q)
}

func (s *Store) listVouchersSQL(ctx context.Context, q domain.VoucherQuery) ([]domain.Voucher, int64, error) {
	where := `($1 = '' OR strpos(lower(v.title), lower($1)) > 0 OR strpos(lower(v.code), lower($1)) > 0 OR strpos(lower(v.description), lower($1)) > 0 OR strpos(lower(v.internal_notes), lower($1)) > 0)
		AND ($2 = '' OR v.status = $2)
		AND ($3 = '' OR ` + voucherLiveSQL(4) + ` = ($3 = 'true'))
		AND ($5 = '' OR v.discount_type = $5)
		AND ($6 = '' OR CASE $6
			WHEN 'running' THEN (v.starts_at IS NULL OR v.starts_at <= $4) AND (v.ends_at IS NULL OR v.ends_at > $4)
			WHEN 'upcoming' THEN v.starts_at > $4
			WHEN 'ended' THEN v.ends_at <= $4
			WHEN 'unlimited' THEN v.starts_at IS NULL AND v.ends_at IS NULL
			END)
		AND ($7::bigint = 0 OR NOT EXISTS (SELECT 1 FROM voucher_outlets x WHERE x.voucher_id = v.id)
			OR EXISTS (SELECT 1 FROM voucher_outlets x WHERE x.voucher_id = v.id AND x.outlet_id = $7))`
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM vouchers v WHERE `+where, q.Q, q.Status, q.Live, q.Now, q.DiscountType, q.Period, q.OutletID).Scan(&total); err != nil {
		return nil, 0, internal("Could not list vouchers", err)
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM vouchers v WHERE %s ORDER BY %s, v.id ASC LIMIT $8 OFFSET $9`, voucherCols, where, orderBy(voucherOrder, q.SortBy, q.SortDir)),
		q.Q, q.Status, q.Live, q.Now, q.DiscountType, q.Period, q.OutletID, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, internal("Could not list vouchers", err)
	}
	out, err := readVouchers(ctx, s.pool, rows)
	return out, total, err
}

// vouchersByIDs reads the vouchers with these ids, in the order given. An id with no row (the index is a moment behind) is skipped.
func (s *Store) vouchersByIDs(ctx context.Context, ids []int64) ([]domain.Voucher, error) {
	if len(ids) == 0 {
		return []domain.Voucher{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+voucherCols+` FROM vouchers v WHERE v.id = ANY($1)`, ids)
	if err != nil {
		return nil, internal("Could not list vouchers", err)
	}
	found, err := readVouchers(ctx, s.pool, rows)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]domain.Voucher, len(found))
	for _, v := range found {
		byID[v.ID] = v
	}
	out := make([]domain.Voucher, 0, len(ids))
	for _, id := range ids {
		if v, ok := byID[id]; ok {
			out = append(out, v)
		} else {
			s.stale(searchindex.Vouchers, id)
		}
	}
	return out, nil
}

func (s *Store) GetVoucher(ctx context.Context, id int64) (domain.Voucher, error) {
	return getVoucher(ctx, s.pool, id)
}

func getVoucher(ctx context.Context, q queryer, id int64) (domain.Voucher, error) {
	v, err := scanVoucher(q.QueryRow(ctx, `SELECT `+voucherCols+` FROM vouchers v WHERE v.id = $1`, id))
	if err != nil {
		return v, err
	}
	items := []domain.Voucher{v}
	if err := attachVoucherRefs(ctx, q, items); err != nil {
		return v, err
	}
	return items[0], nil
}

// checkVoucherRefs refuses an outlet or a service that does not exist. The rows are locked FOR SHARE so that they cannot be deleted
// between this check and the commit (a delete takes the row FOR UPDATE).
func checkVoucherRefs(ctx context.Context, tx pgx.Tx, v domain.Voucher) error {
	for _, ref := range []struct {
		table, label string
		ids          []int64
	}{{"outlets", "Outlet", v.OutletIDs}, {"services", "Service", v.ServiceIDs}} {
		if len(ref.ids) == 0 {
			continue
		}
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = ANY($1) ORDER BY id FOR SHARE`, ref.table), ref.ids)
		if err != nil {
			return internal("Could not check the voucher's "+ref.table, err)
		}
		have := map[int64]bool{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return internal("Could not check the voucher's "+ref.table, err)
			}
			have[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return internal("Could not check the voucher's "+ref.table, err)
		}
		for _, id := range ref.ids {
			if !have[id] {
				return apperr.Invalid(fmt.Sprintf("%s %d does not exist", ref.label, id))
			}
		}
	}
	return nil
}

func setVoucherRefs(ctx context.Context, tx pgx.Tx, v domain.Voucher) error {
	if _, err := tx.Exec(ctx, `DELETE FROM voucher_outlets WHERE voucher_id = $1`, v.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM voucher_services WHERE voucher_id = $1`, v.ID); err != nil {
		return err
	}
	if len(v.OutletIDs) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO voucher_outlets (voucher_id, outlet_id) SELECT $1, unnest($2::bigint[])`, v.ID, v.OutletIDs); err != nil {
			return err
		}
	}
	if len(v.ServiceIDs) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO voucher_services (voucher_id, service_id) SELECT $1, unnest($2::bigint[])`, v.ID, v.ServiceIDs); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateVoucher(ctx context.Context, v domain.Voucher) (domain.Voucher, error) {
	var out domain.Voucher
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := checkVoucherRefs(ctx, tx, v); err != nil {
			return err
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO vouchers (code, title, description, internal_notes, discount_type, value, max_discount, min_spend, max_claims, usage_limit, status, color, image_url, starts_at, ends_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id`,
			v.Code, v.Title, v.Description, v.InternalNotes, v.DiscountType, v.Value, v.MaxDiscount, v.MinSpend, v.MaxClaims, v.UsageLimit, v.Status, v.Color, v.ImageURL, v.StartsAt, v.EndsAt).Scan(&id); err != nil {
			return err
		}
		v.ID = id
		if err := setVoucherRefs(ctx, tx, v); err != nil {
			return err
		}
		var err error
		out, err = getVoucher(ctx, tx, id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Vouchers: {out.ID}})
	}
	return out, wrapVoucher(err, "Could not create the voucher")
}

// UpdateVoucher replaces everything about the voucher. A voucher that customers hold cannot change its code (they would hold a voucher
// whose code is no longer the one they were given) nor become public (every wallet would get it); the rest can change, and what a
// customer holds follows.
func (s *Store) UpdateVoucher(ctx context.Context, v domain.Voucher) (domain.Voucher, error) {
	var out domain.Voucher
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var code string
		var held int64
		if err := tx.QueryRow(ctx, `SELECT code FROM vouchers WHERE id = $1 FOR UPDATE`, v.ID).Scan(&code); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("Voucher not found")
			}
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM voucher_grants WHERE voucher_id = $1`, v.ID).Scan(&held); err != nil {
			return err
		}
		if held > 0 && code != v.Code {
			return apperr.Conflict(fmt.Sprintf("%d customer(s) hold this voucher, so its code cannot change; make a new voucher instead", held))
		}
		if held > 0 && v.MaxClaims > 0 && int64(v.MaxClaims) < held {
			return apperr.Conflict(fmt.Sprintf("%d customer(s) hold this voucher already; the claim limit cannot be lower than that", held))
		}
		if err := checkVoucherRefs(ctx, tx, v); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE vouchers SET code=$2, title=$3, description=$4, internal_notes=$5, discount_type=$6, value=$7, max_discount=$8, min_spend=$9, max_claims=$10,
				usage_limit=$11, status=$12, color=$13, image_url=$14, starts_at=$15, ends_at=$16, updated_at=now()
			WHERE id=$1`, v.ID, v.Code, v.Title, v.Description, v.InternalNotes, v.DiscountType, v.Value, v.MaxDiscount, v.MinSpend, v.MaxClaims,
			v.UsageLimit, v.Status, v.Color, v.ImageURL, v.StartsAt, v.EndsAt); err != nil {
			return err
		}
		if err := setVoucherRefs(ctx, tx, v); err != nil {
			return err
		}
		var err error
		out, err = getVoucher(ctx, tx, v.ID)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Vouchers: {out.ID}})
	}
	return out, wrapVoucher(err, "Could not update the voucher")
}

func (s *Store) SetVoucherStatus(ctx context.Context, id int64, status string) (domain.Voucher, error) {
	var out domain.Voucher
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var found int64
		if err := tx.QueryRow(ctx, `SELECT id FROM vouchers WHERE id = $1 FOR UPDATE`, id).Scan(&found); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("Voucher not found")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE vouchers SET status = $2, updated_at = now() WHERE id = $1`, id, status); err != nil {
			return err
		}
		var err error
		out, err = getVoucher(ctx, tx, id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Vouchers: {id}})
	}
	return out, wrapVoucher(err, "Could not change the voucher's status")
}

// DeleteVoucher refuses a voucher that customers hold: they would lose it without having spent it. Switching it off keeps their copy
// in the books and takes it out of the app.
func (s *Store) DeleteVoucher(ctx context.Context, id int64) error {
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var found int64
		if err := tx.QueryRow(ctx, `SELECT id FROM vouchers WHERE id = $1 FOR UPDATE`, id).Scan(&found); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("Voucher not found")
			}
			return err
		}
		var held int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM voucher_grants WHERE voucher_id = $1`, id).Scan(&held); err != nil {
			return err
		}
		if held > 0 {
			return apperr.Conflict(fmt.Sprintf("%d customer(s) hold this voucher and it cannot be deleted; switch it off instead", held))
		}
		_, err := tx.Exec(ctx, `DELETE FROM vouchers WHERE id = $1`, id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Vouchers: {id}})
	}
	return wrapVoucher(err, "Could not delete the voucher")
}

// ---- who holds a voucher ----

func (s *Store) ListVoucherGrants(ctx context.Context, q domain.GrantQuery) ([]domain.VoucherGrant, int64, error) {
	where := `g.voucher_id = $1 AND ($2 = '' OR strpos(lower(g.customer_name), lower($2)) > 0 OR strpos(g.customer_phone, $2) > 0)`
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM voucher_grants g WHERE `+where, q.VoucherID, q.Q).Scan(&total); err != nil {
		return nil, 0, internal("Could not list the voucher's holders", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT g.id, g.voucher_id, g.customer_id, g.customer_name, g.customer_phone, g.source, g.granted_by, g.granted_at, g.expires_at, g.uses_remaining
		FROM voucher_grants g WHERE `+where+` ORDER BY g.granted_at DESC, g.id DESC LIMIT $3 OFFSET $4`, q.VoucherID, q.Q, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, internal("Could not list the voucher's holders", err)
	}
	defer rows.Close()
	out := []domain.VoucherGrant{}
	for rows.Next() {
		var g domain.VoucherGrant
		if err := rows.Scan(&g.ID, &g.VoucherID, &g.CustomerID, &g.CustomerName, &g.CustomerPhone, &g.Source, &g.GrantedBy, &g.GrantedAt, &g.ExpiresAt, &g.UsesRemaining); err != nil {
			return nil, 0, internal("Could not list the voucher's holders", err)
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}

// Inject puts the voucher in the wallets of the customers, once each, within the claim limit, and keeps the audit of it.
func (s *Store) Inject(ctx context.Context, in domain.InjectInput, actor command.Actor, now time.Time) (domain.Injection, error) {
	var out domain.Injection
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var found int64
		if err := tx.QueryRow(ctx, `SELECT id FROM vouchers WHERE id = $1 FOR UPDATE`, in.VoucherID).Scan(&found); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("Voucher not found")
			}
			return err
		}
		v, err := getVoucher(ctx, tx, in.VoucherID)
		if err != nil {
			return err
		}
		if err := v.CheckInjectable(now); err != nil {
			return err
		}
		ids := make([]int64, len(in.Customers))
		for i, c := range in.Customers {
			ids[i] = c.ID
		}
		held := map[int64]bool{}
		rows, err := tx.Query(ctx, `SELECT customer_id FROM voucher_grants WHERE voucher_id = $1 AND customer_id = ANY($2)`, v.ID, ids)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			held[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		grant, plan := domain.PlanInjection(v, v.ClaimsCount, held, in.Customers, in.NotFound)
		if len(grant) > 0 {
			gid := make([]int64, len(grant))
			names, phones := make([]string, len(grant)), make([]string, len(grant))
			for i, c := range grant {
				gid[i], names[i], phones[i] = c.ID, c.Name, c.Phone
			}
			if _, err := tx.Exec(ctx, `INSERT INTO voucher_grants (voucher_id, customer_id, customer_name, customer_phone, source, granted_by, expires_at, uses_remaining)
				SELECT $1, c.id, c.name, c.phone, 'inject', $2, $3, $4 FROM unnest($5::bigint[], $6::text[], $7::text[]) AS c(id, name, phone)`,
				v.ID, actor.Name, in.ExpiresAt, v.UsageLimit, gid, names, phones); err != nil {
				return err
			}
		}
		plan.VoucherID, plan.VoucherTitle, plan.VoucherCode, plan.ActorID, plan.ActorName = v.ID, v.Title, v.Code, actor.ID, actor.Name
		if err := tx.QueryRow(ctx, `INSERT INTO voucher_injections (voucher_id, voucher_title, voucher_code, actor_id, actor_name, target_count, injected_count,
				skipped_already_held, skipped_over_limit, skipped_not_found, skipped_inactive)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id, created_at`,
			plan.VoucherID, plan.VoucherTitle, plan.VoucherCode, plan.ActorID, plan.ActorName, plan.Target, plan.Injected, plan.AlreadyHeld, plan.OverLimit, plan.NotFound, plan.Inactive).
			Scan(&plan.ID, &plan.CreatedAt); err != nil {
			return err
		}
		out = plan
		return nil
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Vouchers: {in.VoucherID}})
	}
	return out, wrapVoucher(err, "Could not send the voucher")
}

func (s *Store) ListInjections(ctx context.Context, q domain.InjectionQuery) ([]domain.Injection, int64, error) {
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM voucher_injections WHERE ($1::bigint = 0 OR voucher_id = $1)`, q.VoucherID).Scan(&total); err != nil {
		return nil, 0, internal("Could not list injections", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT id, voucher_id, voucher_title, voucher_code, actor_id, actor_name, target_count, injected_count,
			skipped_already_held, skipped_over_limit, skipped_not_found, skipped_inactive, created_at
		FROM voucher_injections WHERE ($1::bigint = 0 OR voucher_id = $1) ORDER BY id DESC LIMIT $2 OFFSET $3`, q.VoucherID, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, internal("Could not list injections", err)
	}
	defer rows.Close()
	out := []domain.Injection{}
	for rows.Next() {
		var i domain.Injection
		if err := rows.Scan(&i.ID, &i.VoucherID, &i.VoucherTitle, &i.VoucherCode, &i.ActorID, &i.ActorName, &i.Target, &i.Injected,
			&i.AlreadyHeld, &i.OverLimit, &i.NotFound, &i.Inactive, &i.CreatedAt); err != nil {
			return nil, 0, internal("Could not list injections", err)
		}
		out = append(out, i)
	}
	return out, total, rows.Err()
}

// ---- the customer's wallet ----

// ClaimVoucher puts the voucher with this code in the customer's wallet.
func (s *Store) ClaimVoucher(ctx context.Context, in domain.ClaimInput, now time.Time) (domain.WalletVoucher, error) {
	var out domain.WalletVoucher
	var voucherID int64
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id FROM vouchers WHERE code = $1 FOR UPDATE`, in.Code).Scan(&voucherID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("This code is not valid")
			}
			return err
		}
		v, err := getVoucher(ctx, tx, voucherID)
		if err != nil {
			return err
		}
		if !v.IsLive(now) {
			return apperr.Conflict("This voucher is not available right now")
		}
		var held bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM voucher_grants WHERE voucher_id = $1 AND customer_id = $2)`, v.ID, in.CustomerID).Scan(&held); err != nil {
			return err
		}
		if held {
			return apperr.Conflict("You already have this voucher")
		}
		if v.MaxClaims > 0 && v.ClaimsCount >= int64(v.MaxClaims) {
			return apperr.Conflict("This voucher has been claimed by everyone it was for")
		}
		var g domain.VoucherGrant
		if err := tx.QueryRow(ctx, `INSERT INTO voucher_grants (voucher_id, customer_id, customer_name, customer_phone, source, uses_remaining)
			VALUES ($1, $2, $3, $4, 'claim', $5) RETURNING granted_at, uses_remaining`, v.ID, in.CustomerID, in.CustomerName, in.CustomerPhone, v.UsageLimit).
			Scan(&g.GrantedAt, &g.UsesRemaining); err != nil {
			return err
		}
		v.ClaimsCount++
		out = domain.WalletVoucher{Voucher: v, Source: domain.GrantClaim, GrantedAt: &g.GrantedAt, UsesRemaining: g.UsesRemaining}
		return nil
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Vouchers: {voucherID}})
	}
	return out, wrapVoucher(err, "Could not claim the voucher")
}

// ListWallet is what a customer holds at now: the public vouchers that are live, and their grants of live vouchers that have not
// expired and still have a use left. Grants come first, newest first.
func (s *Store) ListWallet(ctx context.Context, customerID int64, now time.Time) ([]domain.WalletVoucher, error) {
	var out []domain.WalletVoucher
	rows, err := s.pool.Query(ctx, `SELECT `+voucherCols+`, g.source, g.granted_at, g.expires_at, g.uses_remaining
		FROM voucher_grants g JOIN vouchers v ON v.id = g.voucher_id
		WHERE g.customer_id = $1 AND `+voucherLiveSQL(2)+` AND (g.expires_at IS NULL OR g.expires_at > $2) AND g.uses_remaining > 0
		ORDER BY g.granted_at DESC, g.id DESC`, customerID, now)
	if err != nil {
		return nil, internal("Could not read the wallet", err)
	}
	for rows.Next() {
		var w domain.WalletVoucher
		var granted time.Time
		v := &w.Voucher
		if err := rows.Scan(&v.ID, &v.Code, &v.Title, &v.Description, &v.InternalNotes, &v.DiscountType, &v.Value, &v.MaxDiscount, &v.MinSpend, &v.MaxClaims, &v.UsageLimit,
			&v.Status, &v.Color, &v.ImageURL, &v.StartsAt, &v.EndsAt, &v.CreatedAt, &v.UpdatedAt, &w.Source, &granted, &w.GrantExpires, &w.UsesRemaining); err != nil {
			rows.Close()
			return nil, internal("Could not read the wallet", err)
		}
		w.GrantedAt = &granted
		out = append(out, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, internal("Could not read the wallet", err)
	}
	rows, err = s.pool.Query(ctx, `SELECT `+voucherCols+` FROM vouchers v WHERE v.code = '' AND `+voucherLiveSQL(1)+` ORDER BY v.id DESC`, now)
	if err != nil {
		return nil, internal("Could not read the wallet", err)
	}
	public, err := readVouchers(ctx, s.pool, rows)
	if err != nil {
		return nil, err
	}
	for _, v := range public {
		out = append(out, domain.WalletVoucher{Voucher: v, Source: domain.WalletPublic, UsesRemaining: v.UsageLimit})
	}
	// Outlets and services of the granted ones: read them in one go.
	granted := make([]domain.Voucher, 0, len(out))
	at := make([]int, 0, len(out))
	for i, w := range out {
		if w.Source != domain.WalletPublic {
			granted = append(granted, w.Voucher)
			at = append(at, i)
		}
	}
	if err := attachVoucherRefs(ctx, s.pool, granted); err != nil {
		return nil, err
	}
	for k, i := range at {
		out[i].Voucher = granted[k]
	}
	return out, nil
}

// ---- deleting what vouchers need ----

// vouchersNeeding names (up to five) and counts the vouchers limited to an outlet or a service: deleting it would silently widen them.
func vouchersNeeding(ctx context.Context, q queryer, kind string, id int64) ([]string, int64, error) {
	table, col := "voucher_outlets", "outlet_id"
	if kind == domain.TargetService {
		table, col = "voucher_services", "service_id"
	}
	cond := fmt.Sprintf(`EXISTS (SELECT 1 FROM %s x WHERE x.voucher_id = v.id AND x.%s = $1)`, table, col)
	var total int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM vouchers v WHERE `+cond, id).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.Query(ctx, `SELECT v.title FROM vouchers v WHERE `+cond+` ORDER BY lower(v.title), v.id LIMIT 5`, id)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, 0, err
		}
		names = append(names, n)
	}
	return names, total, rows.Err()
}

// refuseIfVouchersNeed is the delete guard of a service or an outlet: a conflict naming the vouchers that are still limited to it.
func refuseIfVouchersNeed(ctx context.Context, q queryer, kind string, id int64) error {
	names, total, err := vouchersNeeding(ctx, q, kind, id)
	if err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	return apperr.Conflict(fmt.Sprintf("This %s is used by %d voucher(s) and cannot be deleted; change or delete the voucher first. %s", kind, total, domain.UsedByMessage(names, int(total))))
}

func wrapVoucher(err error, msg string) error {
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
			return apperr.Conflict("Another voucher already uses this code")
		case "23503":
			return apperr.Conflict("An outlet or service of the voucher no longer exists, or customers hold the voucher")
		case "23514":
			return apperr.Invalid("The voucher is not valid")
		}
	}
	return apperr.Internal(msg).WithCause(err)
}
