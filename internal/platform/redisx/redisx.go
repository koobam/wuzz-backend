// Package redisx wraps go-redis with the one thing every caller needs: a
// consistently namespaced key, so "session" in the user service and
// "ratelimit" in the gateway can never collide.
package redisx

import (
	"context"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
)

type Client struct {
	*redis.Client
}

func New(ctx context.Context, cfg config.Redis) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: cfg.Addr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &Client{Client: rdb}, nil
}

// Key joins parts with ":" into a single namespaced Redis key.
func (c *Client) Key(parts ...string) string {
	return strings.Join(parts, ":")
}
