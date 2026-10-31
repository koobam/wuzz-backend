// Package waitlist collects the emails people leave on the public landing page. POST /app/v1/waitlist is open to anyone (and rate
// limited); GET /backoffice/v1/waitlist lets an administrator read the list back.
package waitlist

import (
	"context"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

const maxEmail = 254

// Entry is one sign-up as the back office sees it.
type Entry struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Language  string    `json:"language"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// Repo is what the handler needs from the database; Store is the real one.
type Repo interface {
	Add(ctx context.Context, email, language, source string) error
	List(ctx context.Context, limit, offset int) ([]Entry, int, error)
	Count(ctx context.Context) (int, error)
}

// Store keeps the list in the gateway's Postgres.
type Store struct{ Pool *pgxpool.Pool }

// Add saves an address. One that is already there is left alone, and that is not an error.
func (s *Store) Add(ctx context.Context, email, language, source string) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO waitlist (email, language, source) VALUES ($1, $2, $3) ON CONFLICT (email) DO NOTHING`, email, language, source)
	return err
}

// Count is how many addresses are on the list.
func (s *Store) Count(ctx context.Context) (int, error) {
	var total int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM waitlist`).Scan(&total)
	return total, err
}

func (s *Store) List(ctx context.Context, limit, offset int) ([]Entry, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM waitlist`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT id, email, language, source, created_at FROM waitlist ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Email, &e.Language, &e.Source, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, e)
	}
	return items, total, rows.Err()
}

type Handler struct{ Repo Repo }

type signUp struct {
	Email    string `json:"email"`
	Language string `json:"language"`
}

// Normalize checks an address and returns it lower-cased. Only a bare address counts: "Name <a@b.c>" is refused.
func Normalize(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxEmail {
		return "", false
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Address != raw {
		return "", false
	}
	at := strings.LastIndex(raw, "@")
	if !strings.Contains(raw[at+1:], ".") {
		return "", false
	}
	return strings.ToLower(raw), true
}

// Create answers POST /app/v1/waitlist. A new address and one already on the list get the same answer, so the endpoint cannot be used
// to find out who has signed up.
func (h *Handler) Create(c *gin.Context) {
	body, ok := httpx.Bind[signUp](c)
	if !ok {
		return
	}
	email, ok := Normalize(body.Email)
	if !ok {
		httpx.Fail(c, apperr.Invalid("Enter a valid email address"))
		return
	}
	lang := body.Language
	if lang != "id" && lang != "en" {
		lang = "id"
	}
	if err := h.Repo.Add(c.Request.Context(), email, lang, "landing"); err != nil {
		httpx.Fail(c, apperr.Internal("Could not save the sign-up").WithCause(err))
		return
	}
	httpx.Created(c, gin.H{"saved": true})
}

// Count answers GET /app/v1/waitlist/count: how many people are waiting, for the landing page. Only the number is public, never an address.
func (h *Handler) Count(c *gin.Context) {
	total, err := h.Repo.Count(c.Request.Context())
	if err != nil {
		httpx.Fail(c, apperr.Internal("Could not read the waitlist").WithCause(err))
		return
	}
	httpx.OK(c, gin.H{"total": total})
}

// List answers GET /backoffice/v1/waitlist: one page, newest first, and the total.
func (h *Handler) List(c *gin.Context) {
	limit, offset := httpx.IntQuery(c, "limit", 50), httpx.IntQuery(c, "offset", 0)
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	items, total, err := h.Repo.List(c.Request.Context(), limit, offset)
	if err != nil {
		httpx.Fail(c, apperr.Internal("Could not read the waitlist").WithCause(err))
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": total})
}
