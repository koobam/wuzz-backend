// Package redis adapts the user service's session and pending-registration
// ports to Redis. Neither belongs in Postgres: a refresh token is a
// capability with a TTL, not a record worth keeping once it expires or is
// used, and a pending registration is deliberately throwaway.
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
)

type Sessions struct {
	rdb *redisx.Client
	ttl time.Duration
}

func NewSessions(rdb *redisx.Client, ttl time.Duration) *Sessions {
	return &Sessions{rdb: rdb, ttl: ttl}
}

func (s *Sessions) key(token string) string { return s.rdb.Key("session", token) }

func (s *Sessions) Issue(ctx context.Context, userID string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", apperr.Internal("Could not start a session").WithCause(err)
	}
	if err := s.rdb.Set(ctx, s.key(token), userID, s.ttl).Err(); err != nil {
		return "", apperr.Internal("Could not start a session").WithCause(err)
	}
	return token, nil
}

// Consume returns the user id a refresh token was issued for and deletes it
// in the same round trip: a refresh token is single use, so a stolen one
// replayed after the legitimate client already rotated it is rejected rather
// than honoured.
func (s *Sessions) Consume(ctx context.Context, token string) (string, error) {
	userID, err := s.rdb.GetDel(ctx, s.key(token)).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return "", apperr.Unauthorized("This session is no longer valid")
		}
		return "", apperr.Internal("Could not verify the session").WithCause(err)
	}
	return userID, nil
}

func (s *Sessions) Revoke(ctx context.Context, token string) error {
	if err := s.rdb.Del(ctx, s.key(token)).Err(); err != nil {
		return apperr.Internal("Could not end the session").WithCause(err)
	}
	return nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
