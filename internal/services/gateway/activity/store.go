package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store keeps the log in the gateway's Postgres.
type Store struct{ Pool *pgxpool.Pool }

const insertSQL = `INSERT INTO activity_log
	(created_at, audience, actor_id, actor_name, actor_roles, action, entity_type, entity_id, summary_key, summary_params, changes, outcome, status, request_id, ip)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

func (s *Store) Insert(ctx context.Context, entries []Entry) error {
	batch := &pgx.Batch{}
	for _, e := range entries {
		roles := e.Actor.Roles
		if roles == nil {
			roles = []string{}
		}
		params := e.Summary.Params
		if params == nil {
			params = map[string]any{}
		}
		var changes any
		if len(e.Changes) > 0 {
			changes = []byte(e.Changes)
		}
		batch.Queue(insertSQL, e.CreatedAt, e.Audience, e.Actor.ID, e.Actor.Name, roles, e.Action, e.EntityType, e.EntityID,
			e.Summary.Key, params, changes, e.Outcome, e.Status, e.RequestID, e.IP)
	}
	res := s.Pool.SendBatch(ctx, batch)
	defer res.Close()
	for range entries {
		if _, err := res.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// Filter is what GET /activity can narrow the log by. Empty means no narrowing.
type Filter struct {
	EntityType, EntityID, ActorID, Action, Outcome, Audience string
	Query                                                    string
	// From and To are days (YYYY-MM-DD) in Asia/Jakarta, both included.
	From, To string
	SortBy   string
	SortDesc bool
	Limit    int
	Offset   int
}

// sortColumns are the columns the list can be sorted by.
var sortColumns = map[string]string{
	"created_at":  "created_at",
	"action":      "action",
	"entity_type": "entity_type",
	"actor_name":  "actor_name",
	"outcome":     "outcome",
}

const jakarta = "'Asia/Jakarta'"

// where builds the WHERE clause and its arguments from f.
func (f Filter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	if f.EntityType != "" {
		add("entity_type = ?", f.EntityType)
	}
	if f.EntityID != "" {
		add("entity_id = ?", f.EntityID)
	}
	if f.ActorID != "" {
		add("actor_id = ?", f.ActorID)
	}
	if f.Action != "" {
		add("action = ?", f.Action)
	}
	if f.Outcome != "" {
		add("outcome = ?", f.Outcome)
	}
	if f.Audience != "" {
		add("audience = ?", f.Audience)
	}
	if f.From != "" {
		add("created_at >= (?::date)::timestamp AT TIME ZONE "+jakarta, f.From)
	}
	if f.To != "" {
		add("created_at < ((?::date) + 1)::timestamp AT TIME ZONE "+jakarta, f.To)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
		args = append(args, like)
		n := len(args)
		conds = append(conds, fmt.Sprintf("(actor_name ILIKE $%[1]d OR entity_id ILIKE $%[1]d OR summary_params::text ILIKE $%[1]d)", n))
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (f Filter) orderBy() string {
	col, ok := sortColumns[f.SortBy]
	if !ok {
		col, f.SortDesc = "created_at", true
	}
	dir := "ASC"
	if f.SortDesc {
		dir = "DESC"
	}
	return fmt.Sprintf(" ORDER BY %s %s, id %s", col, dir, dir)
}

// List returns one page of the log and how many entries match in all.
func (s *Store) List(ctx context.Context, f Filter) ([]Entry, int, error) {
	where, args := f.where()
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM activity_log"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.Pool.Query(ctx, `SELECT id, created_at, audience, actor_id, actor_name, actor_roles, action, entity_type, entity_id,
		summary_key, summary_params, changes, outcome, status, request_id, ip FROM activity_log`+where+f.orderBy()+
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Entry{}
	for rows.Next() {
		var e Entry
		var params, changes []byte
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.Audience, &e.Actor.ID, &e.Actor.Name, &e.Actor.Roles, &e.Action, &e.EntityType,
			&e.EntityID, &e.Summary.Key, &params, &changes, &e.Outcome, &e.Status, &e.RequestID, &e.IP); err != nil {
			return nil, 0, err
		}
		if e.Actor.Roles == nil {
			e.Actor.Roles = []string{}
		}
		if err := json.Unmarshal(params, &e.Summary.Params); err != nil || e.Summary.Params == nil {
			e.Summary.Params = map[string]any{}
		}
		if len(changes) > 0 {
			e.Changes = changes
		}
		items = append(items, e)
	}
	return items, total, rows.Err()
}
