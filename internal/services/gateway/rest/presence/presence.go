// Package presence tells which staff are using the apps right now. An app pings every few seconds
// while it is open; whoever pinged within the window is online. Nothing is stored beyond Redis, and
// nothing remembers who was online before: a closed tab simply stops pinging and drops out.
package presence

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
)

// window is how long after its last ping someone still counts as online. Apps ping every 20 seconds.
const window = 45 * time.Second

type Handler struct {
	Rdb *redisx.Client
}

type person struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

func (h *Handler) scores() string { return h.Rdb.Key("presence", "staff") }
func (h *Handler) people() string { return h.Rdb.Key("presence", "people") }

// Ping marks the caller as online now.
func (h *Handler) Ping(c *gin.Context) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		httpx.Fail(c, apperr.Unauthorized("Authentication is required"))
		return
	}
	body, _ := json.Marshal(person{ID: id.UserID, Name: id.Name, Roles: id.Roles})
	now := time.Now()
	ctx := c.Request.Context()
	pipe := h.Rdb.Pipeline()
	pipe.ZAdd(ctx, h.scores(), redis.Z{Score: float64(now.UnixMilli()), Member: id.UserID})
	pipe.HSet(ctx, h.people(), id.UserID, body)
	// Anyone silent for longer than the window is forgotten, so the two keys do not grow.
	cutoff := strconv.FormatInt(now.Add(-window).UnixMilli(), 10)
	pipe.ZRemRangeByScore(ctx, h.scores(), "-inf", "("+cutoff)
	if _, err := pipe.Exec(ctx); err != nil {
		httpx.Fail(c, apperr.Internal("Could not record presence").WithCause(err))
		return
	}
	httpx.OK(c, gin.H{"window_seconds": int(window.Seconds())})
}

// List returns everyone who pinged within the window, by name.
func (h *Handler) List(c *gin.Context) {
	ctx := c.Request.Context()
	cutoff := strconv.FormatInt(time.Now().Add(-window).UnixMilli(), 10)
	ids, err := h.Rdb.ZRangeByScore(ctx, h.scores(), &redis.ZRangeBy{Min: cutoff, Max: "+inf"}).Result()
	if err != nil {
		httpx.Fail(c, apperr.Internal("Could not read presence").WithCause(err))
		return
	}
	out := make([]person, 0, len(ids))
	if len(ids) > 0 {
		raw, err := h.Rdb.HMGet(ctx, h.people(), ids...).Result()
		if err != nil {
			httpx.Fail(c, apperr.Internal("Could not read presence").WithCause(err))
			return
		}
		for _, v := range raw {
			s, ok := v.(string)
			if !ok {
				continue
			}
			var p person
			if json.Unmarshal([]byte(s), &p) == nil {
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	httpx.OK(c, gin.H{"items": out})
}
