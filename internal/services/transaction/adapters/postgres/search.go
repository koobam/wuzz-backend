package postgres

import (
	"context"
	"log/slog"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/transaction/adapters/searchindex"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// The order list through the Typesense index. Postgres stays the source of truth: the index answers with the ids of the page and the
// total, and the orders themselves are read from Postgres in that order. With no index the list is answered by Postgres; with one, a
// search that cannot reach it is an error (503), never a quiet switch back to Postgres.

// WithIndex gives the store a search index; nil keeps listing from Postgres.
func (s *Store) WithIndex(ix *searchindex.Index, log *slog.Logger) *Store {
	s.ix, s.log = ix, log
	return s
}

func (s *Store) searchOrders(ctx context.Context, f domain.ListFilter) ([]domain.Order, int64, error) {
	ids, total, err := s.ix.Search(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := []domain.Order{}
	if len(ids) == 0 {
		return out, total, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM orders WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, 0, apperr.Internal("Could not list orders").WithCause(err)
	}
	defer rows.Close()
	byID := map[string]domain.Order{}
	for rows.Next() {
		o, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		byID[o.ID] = o
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for _, id := range ids {
		if o, ok := byID[id]; ok {
			out = append(out, o)
		} else if s.log != nil {
			s.log.Warn("search index has an order with no row", slog.String("id", id))
		}
	}
	return out, total, nil
}

// summaryScope is the WHERE clause of a summary and its arguments. The list and its summary must mean the same orders. Without a text
// search the filters are exact in Postgres and in the index alike; with one, the index decides what matches (it forgives a typo, which
// Postgres does not), so the summary takes the orders it found.
func (s *Store) summaryScope(ctx context.Context, f domain.ListFilter) (string, []any, error) {
	if !s.ix.Enabled() || f.Query == "" {
		return orderWhere, filterArgs(f), nil
	}
	ids, err := s.ix.AllIDs(ctx, f)
	if err != nil {
		return "", nil, err
	}
	// The search found the orders; the remaining filters already hold for them, and are applied once more by the same clause.
	g := f
	g.Query = ""
	return orderWhere + ` AND id = ANY($6::uuid[])`, append(filterArgs(g), ids), nil
}

// sync puts an order in the index right after the commit. Best effort — the order is already saved — but a failure is logged
// loudly by the index and repaired by the reconcile.
func (s *Store) sync(ctx context.Context, o domain.Order) {
	if !s.ix.Enabled() {
		return
	}
	_ = s.ix.Put(context.WithoutCancel(ctx), o)
}

// Everything is the searchindex.Source: every order, read from Postgres, as documents.
func (s *Store) Everything(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM orders`)
	if err != nil {
		return nil, apperr.Internal("Could not read the orders").WithCause(err)
	}
	defer rows.Close()
	var docs []map[string]any
	for rows.Next() {
		o, err := scan(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, searchindex.Doc(o))
	}
	return docs, rows.Err()
}
