package activity

import (
	"context"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

// Reader is what the list endpoint needs from the log; Store is the real one.
type Reader interface {
	List(ctx context.Context, f Filter) ([]Entry, int, error)
}

type Handler struct{ Log Reader }

var day = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

const (
	defaultLimit = 25
	maxLimit     = 100
)

// List answers GET /backoffice/v1/activity: one page of the log, newest first unless sorted otherwise, and how many entries match in all.
func (h *Handler) List(c *gin.Context) {
	f := Filter{
		EntityType: c.Query("entity_type"), EntityID: c.Query("entity_id"), ActorID: c.Query("actor_id"),
		Action: c.Query("action"), Outcome: c.Query("outcome"), Audience: c.Query("audience"),
		Query: c.Query("q"), From: c.Query("from"), To: c.Query("to"),
		SortBy:   c.Query("sort_by"),
		SortDesc: c.Query("sort_dir") != "asc",
		Limit:    httpx.IntQuery(c, "limit", defaultLimit),
		Offset:   httpx.IntQuery(c, "offset", 0),
	}
	if f.Limit < 1 || f.Limit > maxLimit {
		f.Limit = defaultLimit
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	for _, d := range []struct{ name, v string }{{"from", f.From}, {"to", f.To}} {
		if d.v == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d.v); err != nil || !day.MatchString(d.v) {
			httpx.Fail(c, apperr.Invalid(d.name+" must be a day, YYYY-MM-DD"))
			return
		}
	}
	items, total, err := h.Log.List(c.Request.Context(), f)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": total})
}

// Log is the activity log as the router needs it: where entries go and where they are read back. A nil *Log turns the log off.
type Log struct {
	Recorder *Recorder
	Reader   Reader
}
