// Package postgres keeps media's rows.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/media/domain"
)

type Store struct{ pool *postgres.Pool }

func NewStore(pool *postgres.Pool) *Store { return &Store{pool: pool} }

const cols = `public_id::text, object_key, url, folder, file_name, content_type, size, width, height, uploaded_by, created_at`

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (domain.Media, error) {
	var m domain.Media
	err := r.Scan(&m.ID, &m.Key, &m.URL, &m.Folder, &m.FileName, &m.ContentType, &m.Size, &m.Width, &m.Height, &m.UploadedBy, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, apperr.NotFound("Media not found")
	}
	if err != nil {
		return m, apperr.Internal("Could not read the media").WithCause(err)
	}
	return m, nil
}

func (s *Store) Insert(ctx context.Context, m domain.Media) (domain.Media, error) {
	return scan(s.pool.QueryRow(ctx, `
		INSERT INTO media (object_key, url, folder, file_name, content_type, size, width, height, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+cols, m.Key, m.URL, m.Folder, m.FileName, m.ContentType, m.Size, m.Width, m.Height, m.UploadedBy))
}

func (s *Store) Get(ctx context.Context, id string) (domain.Media, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM media WHERE public_id = $1::uuid`, id))
}

func (s *Store) BatchGet(ctx context.Context, ids []string) ([]domain.Media, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM media WHERE public_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, apperr.Internal("Could not read the media").WithCause(err)
	}
	defer rows.Close()
	return collect(rows)
}

func (s *Store) List(ctx context.Context, q domain.ListQuery) ([]domain.Media, int64, error) {
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM media WHERE ($1 = '' OR folder = $1)`, q.Folder).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("Could not list the media").WithCause(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM media WHERE ($1 = '' OR folder = $1)
		ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, q.Folder, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, apperr.Internal("Could not list the media").WithCause(err)
	}
	defer rows.Close()
	out, err := collect(rows)
	return out, total, err
}

func (s *Store) Delete(ctx context.Context, id string) (domain.Media, error) {
	return scan(s.pool.QueryRow(ctx, `DELETE FROM media WHERE public_id = $1::uuid RETURNING `+cols, id))
}

func collect(rows pgx.Rows) ([]domain.Media, error) {
	var out []domain.Media
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("Could not read the media").WithCause(err)
	}
	return out, nil
}
