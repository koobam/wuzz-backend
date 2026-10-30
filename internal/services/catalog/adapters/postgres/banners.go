package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/adapters/searchindex"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Banners of the customer app. The back office's list is answered by the index when there is one (search.go), the customer app's by
// Postgres alone: it is a handful of rows and must never be behind a write.

const bannerCols = `b.id, b.title, b.internal_notes, b.image_url, b.target_type, b.target_value, b.status, b.sort_index, b.starts_at, b.ends_at, b.created_at, b.updated_at`

// bannerOrderLock serialises the writes that decide the carousel's order (a create takes the last place; a reorder rewrites all).
const bannerOrderLock = `SELECT pg_advisory_xact_lock(hashtext('banners_order'))`

func scanBanner(r pgx.Row) (domain.Banner, error) {
	var b domain.Banner
	err := r.Scan(&b.ID, &b.Title, &b.InternalNotes, &b.ImageURL, &b.TargetType, &b.TargetValue, &b.Status, &b.SortIndex, &b.StartsAt, &b.EndsAt, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, apperr.NotFound("Banner not found")
	}
	if err != nil {
		return b, internal("Could not read the banner", err)
	}
	return b, nil
}

// attachBannerOutlets fills the outlet limits of banners, in place.
func attachBannerOutlets(ctx context.Context, q queryer, items []domain.Banner) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	at := make(map[int64]int, len(items))
	for i, b := range items {
		ids[i], at[b.ID] = b.ID, i
		items[i].OutletIDs, items[i].Outlets = nil, nil
	}
	rows, err := q.Query(ctx, `SELECT bo.banner_id, o.id, o.name FROM banner_outlets bo JOIN outlets o ON o.id = bo.outlet_id
		WHERE bo.banner_id = ANY($1) ORDER BY lower(o.name), o.id`, ids)
	if err != nil {
		return internal("Could not read the banners' outlets", err)
	}
	defer rows.Close()
	for rows.Next() {
		var banner int64
		var o domain.OutletRef
		if err := rows.Scan(&banner, &o.ID, &o.Name); err != nil {
			return internal("Could not read the banners' outlets", err)
		}
		i := at[banner]
		items[i].OutletIDs = append(items[i].OutletIDs, o.ID)
		items[i].Outlets = append(items[i].Outlets, o)
	}
	return rows.Err()
}

func readBanners(ctx context.Context, q queryer, rows pgx.Rows) ([]domain.Banner, error) {
	defer rows.Close()
	out := []domain.Banner{}
	for rows.Next() {
		b, err := scanBanner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, internal("Could not read the banners", err)
	}
	rows.Close()
	return out, attachBannerOutlets(ctx, q, out)
}

var bannerOrder = map[string]string{
	"id": "b.id", "title": "lower(b.title)", "status": "b.status", "sort_index": "b.sort_index",
	"starts_at":  "COALESCE(extract(epoch FROM b.starts_at), 0)",
	"ends_at":    "COALESCE(extract(epoch FROM b.ends_at), 253402300799)",
	"created_at": "b.created_at", "updated_at": "b.updated_at",
}

// liveSQL is the test for "on a phone at $n": the same one as domain.Banner.IsLive.
func liveSQL(n int) string {
	return fmt.Sprintf(`(b.status = 'active' AND (b.starts_at IS NULL OR b.starts_at <= $%[1]d) AND (b.ends_at IS NULL OR b.ends_at > $%[1]d))`, n)
}

func (s *Store) ListBanners(ctx context.Context, q domain.BannerQuery) ([]domain.Banner, int64, error) {
	if s.ix.Enabled() {
		res, err := s.ix.Search(ctx, searchindex.Banners, searchindex.BannerQuery(q))
		if err != nil {
			return nil, 0, err
		}
		out, err := s.bannersByIDs(ctx, res.IDs)
		return out, res.Total, err
	}
	return s.listBannersSQL(ctx, q)
}

func (s *Store) listBannersSQL(ctx context.Context, q domain.BannerQuery) ([]domain.Banner, int64, error) {
	where := `($1 = '' OR strpos(lower(b.title), lower($1)) > 0 OR strpos(lower(b.internal_notes), lower($1)) > 0)
		AND ($2 = '' OR b.status = $2)
		AND ($3 = '' OR ` + liveSQL(4) + ` = ($3 = 'true'))
		AND ($5::bigint = 0 OR NOT EXISTS (SELECT 1 FROM banner_outlets x WHERE x.banner_id = b.id)
			OR EXISTS (SELECT 1 FROM banner_outlets x WHERE x.banner_id = b.id AND x.outlet_id = $5))`
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM banners b WHERE `+where, q.Q, q.Status, q.Live, q.Now, q.OutletID).Scan(&total); err != nil {
		return nil, 0, internal("Could not list banners", err)
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM banners b WHERE %s ORDER BY %s, b.id ASC LIMIT $6 OFFSET $7`, bannerCols, where, orderBy(bannerOrder, q.SortBy, q.SortDir)),
		q.Q, q.Status, q.Live, q.Now, q.OutletID, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, internal("Could not list banners", err)
	}
	out, err := readBanners(ctx, s.pool, rows)
	return out, total, err
}

// bannersByIDs reads the banners with these ids, in the order given. An id with no row (the index is a moment behind) is skipped.
func (s *Store) bannersByIDs(ctx context.Context, ids []int64) ([]domain.Banner, error) {
	if len(ids) == 0 {
		return []domain.Banner{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+bannerCols+` FROM banners b WHERE b.id = ANY($1)`, ids)
	if err != nil {
		return nil, internal("Could not list banners", err)
	}
	found, err := readBanners(ctx, s.pool, rows)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]domain.Banner, len(found))
	for _, b := range found {
		byID[b.ID] = b
	}
	out := make([]domain.Banner, 0, len(ids))
	for _, id := range ids {
		if b, ok := byID[id]; ok {
			out = append(out, b)
		} else {
			s.stale(searchindex.Banners, id)
		}
	}
	return out, nil
}

func (s *Store) GetBanner(ctx context.Context, id int64) (domain.Banner, error) {
	return getBanner(ctx, s.pool, id)
}

func getBanner(ctx context.Context, q queryer, id int64) (domain.Banner, error) {
	b, err := scanBanner(q.QueryRow(ctx, `SELECT `+bannerCols+` FROM banners b WHERE b.id = $1`, id))
	if err != nil {
		return b, err
	}
	items := []domain.Banner{b}
	if err := attachBannerOutlets(ctx, q, items); err != nil {
		return b, err
	}
	return items[0], nil
}

// ListLiveBanners reads, straight from Postgres, the banners on a phone at now for this outlet. With no outlet known only the banners
// for every outlet apply. A handful is plenty for a carousel.
func (s *Store) ListLiveBanners(ctx context.Context, outletID int64, now time.Time) ([]domain.Banner, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+bannerCols+` FROM banners b WHERE `+liveSQL(1)+`
		AND (NOT EXISTS (SELECT 1 FROM banner_outlets x WHERE x.banner_id = b.id)
			OR ($2::bigint > 0 AND EXISTS (SELECT 1 FROM banner_outlets x WHERE x.banner_id = b.id AND x.outlet_id = $2)))
		ORDER BY b.sort_index, b.id LIMIT 20`, now, outletID)
	if err != nil {
		return nil, internal("Could not list banners", err)
	}
	defer rows.Close()
	out := []domain.Banner{}
	for rows.Next() {
		b, err := scanBanner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// checkBannerRefs refuses a target or an outlet limit that does not exist. The rows are locked FOR SHARE so that the service or outlet
// cannot be deleted between this check and the commit (a delete takes the row FOR UPDATE).
func checkBannerRefs(ctx context.Context, tx pgx.Tx, b domain.Banner) error {
	if b.TargetType == domain.TargetService || b.TargetType == domain.TargetOutlet {
		table, label := "services", "service"
		if b.TargetType == domain.TargetOutlet {
			table, label = "outlets", "outlet"
		}
		id, _ := strconv.ParseInt(b.TargetValue, 10, 64)
		var found int64
		err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = $1 FOR SHARE`, table), id).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.Invalid(fmt.Sprintf("The %s the banner opens does not exist", label))
		}
		if err != nil {
			return internal("Could not check the banner's target", err)
		}
	}
	if len(b.OutletIDs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM outlets WHERE id = ANY($1) ORDER BY id FOR SHARE`, b.OutletIDs)
	if err != nil {
		return internal("Could not check the banner's outlets", err)
	}
	have := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return internal("Could not check the banner's outlets", err)
		}
		have[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return internal("Could not check the banner's outlets", err)
	}
	for _, id := range b.OutletIDs {
		if !have[id] {
			return apperr.Invalid(fmt.Sprintf("Outlet %d does not exist", id))
		}
	}
	return nil
}

func setBannerOutlets(ctx context.Context, tx pgx.Tx, id int64, outletIDs []int64) error {
	if _, err := tx.Exec(ctx, `DELETE FROM banner_outlets WHERE banner_id = $1`, id); err != nil {
		return err
	}
	if len(outletIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO banner_outlets (banner_id, outlet_id) SELECT $1, unnest($2::bigint[])`, id, outletIDs)
	return err
}

// CreateBanner puts the banner last in the carousel.
func (s *Store) CreateBanner(ctx context.Context, b domain.Banner) (domain.Banner, error) {
	var out domain.Banner
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, bannerOrderLock); err != nil {
			return err
		}
		if err := checkBannerRefs(ctx, tx, b); err != nil {
			return err
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO banners (title, internal_notes, image_url, target_type, target_value, status, sort_index, starts_at, ends_at)
			VALUES ($1, $2, $3, $4, $5, $6, (SELECT COALESCE(max(sort_index), 0) + 1 FROM banners), $7, $8) RETURNING id`,
			b.Title, b.InternalNotes, b.ImageURL, b.TargetType, b.TargetValue, b.Status, b.StartsAt, b.EndsAt).Scan(&id); err != nil {
			return err
		}
		if err := setBannerOutlets(ctx, tx, id, b.OutletIDs); err != nil {
			return err
		}
		var err error
		out, err = getBanner(ctx, tx, id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Banners: {out.ID}})
	}
	return out, wrapBanner(err, "Could not create the banner")
}

// UpdateBanner replaces everything about the banner but its place in the carousel.
func (s *Store) UpdateBanner(ctx context.Context, b domain.Banner) (domain.Banner, error) {
	var out domain.Banner
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var found int64
		if err := tx.QueryRow(ctx, `SELECT id FROM banners WHERE id = $1 FOR UPDATE`, b.ID).Scan(&found); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("Banner not found")
			}
			return err
		}
		if err := checkBannerRefs(ctx, tx, b); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE banners SET title=$2, internal_notes=$3, image_url=$4, target_type=$5, target_value=$6, status=$7, starts_at=$8, ends_at=$9, updated_at=now()
			WHERE id=$1`, b.ID, b.Title, b.InternalNotes, b.ImageURL, b.TargetType, b.TargetValue, b.Status, b.StartsAt, b.EndsAt); err != nil {
			return err
		}
		if err := setBannerOutlets(ctx, tx, b.ID, b.OutletIDs); err != nil {
			return err
		}
		var err error
		out, err = getBanner(ctx, tx, b.ID)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Banners: {out.ID}})
	}
	return out, wrapBanner(err, "Could not update the banner")
}

// SetBannerStatus switches the banner on or off. A banner with no picture cannot be switched on.
func (s *Store) SetBannerStatus(ctx context.Context, id int64, status string) (domain.Banner, error) {
	var out domain.Banner
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var image string
		if err := tx.QueryRow(ctx, `SELECT image_url FROM banners WHERE id = $1 FOR UPDATE`, id).Scan(&image); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("Banner not found")
			}
			return err
		}
		if status == domain.BannerActive && image == "" {
			return apperr.Invalid("A banner needs a picture before it can be switched on")
		}
		if _, err := tx.Exec(ctx, `UPDATE banners SET status = $2, updated_at = now() WHERE id = $1`, id, status); err != nil {
			return err
		}
		var err error
		out, err = getBanner(ctx, tx, id)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Banners: {id}})
	}
	return out, wrapBanner(err, "Could not change the banner's status")
}

func (s *Store) DeleteBanner(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM banners WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("Banner not found")
	}
	if err == nil {
		s.sync(ctx, touched{searchindex.Banners: {id}})
	}
	return wrapBanner(err, "Could not delete the banner")
}

// ReorderBanners numbers the banners 1..n in the order given; ids must be every banner, once.
func (s *Store) ReorderBanners(ctx context.Context, ids []int64) error {
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, bannerOrderLock); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM banners ORDER BY id FOR UPDATE`)
		if err != nil {
			return err
		}
		var all []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			all = append(all, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if err := domain.ValidateReorder(ids, all); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE banners b SET sort_index = v.ord::int FROM unnest($1::bigint[]) WITH ORDINALITY AS v(id, ord) WHERE b.id = v.id`, ids)
		return err
	})
	if err == nil {
		s.sync(ctx, touched{searchindex.Banners: ids})
	}
	return wrapBanner(err, "Could not reorder the banners")
}

// bannersNeeding names (up to five) and counts the banners that cannot do without a service or an outlet: those that open it and,
// for an outlet, those limited to it. Deleting it would break the first and silently widen the second.
func bannersNeeding(ctx context.Context, q queryer, kind string, id int64) ([]string, int64, error) {
	cond := `(b.target_type = $1 AND b.target_value = $2::bigint::text)`
	if kind == domain.TargetOutlet {
		cond += ` OR EXISTS (SELECT 1 FROM banner_outlets x WHERE x.banner_id = b.id AND x.outlet_id = $2::bigint)`
	}
	var total int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM banners b WHERE `+cond, kind, id).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.Query(ctx, `SELECT b.title FROM banners b WHERE `+cond+` ORDER BY lower(b.title), b.id LIMIT 5`, kind, id)
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

// refuseIfBannersNeed is the delete guard of a service or an outlet: a conflict naming the banners that still need it.
func refuseIfBannersNeed(ctx context.Context, q queryer, kind string, id int64) error {
	names, total, err := bannersNeeding(ctx, q, kind, id)
	if err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	return apperr.Conflict(fmt.Sprintf("This %s is used by %d banner(s) and cannot be deleted; change or delete the banner first. %s", kind, total, domain.UsedByMessage(names, int(total))))
}

func wrapBanner(err error, msg string) error {
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
		case "23503":
			return apperr.Conflict("An outlet of the banner no longer exists")
		case "23514":
			return apperr.Invalid("The banner is not valid")
		}
	}
	return apperr.Internal(msg).WithCause(err)
}
