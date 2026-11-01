package redis

import (
	"context"
	"errors"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
)

const (
	// maxPinFailures wrong PINs lock a phone number out for pinLockout.
	maxPinFailures = 5
	pinLockout     = 15 * time.Minute
)

// PinAttempts counts wrong PINs per phone number. It counts unknown numbers too, so the lockout does not tell them apart from
// real ones.
type PinAttempts struct{ rdb *redisx.Client }

func NewPinAttempts(rdb *redisx.Client) *PinAttempts { return &PinAttempts{rdb: rdb} }

func (p *PinAttempts) key(phone string) string { return p.rdb.Key("pin-attempts", phone) }

// Locked returns how long the number is refused for, or 0 when it may try.
func (p *PinAttempts) Locked(ctx context.Context, phone string) (time.Duration, error) {
	raw, err := p.rdb.Get(ctx, p.key(phone)).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return 0, nil
		}
		return 0, apperr.Internal("Could not check the PIN").WithCause(err)
	}
	if n, _ := strconv.Atoi(raw); n < maxPinFailures {
		return 0, nil
	}
	ttl, err := p.rdb.TTL(ctx, p.key(phone)).Result()
	if err != nil {
		return 0, apperr.Internal("Could not check the PIN").WithCause(err)
	}
	if ttl <= 0 {
		return 0, nil
	}
	return ttl, nil
}

// Fail records a wrong PIN. The counter lives for the lockout window from the first failure; the failure that reaches the limit
// restarts it, so the lock lasts the full window from that moment.
func (p *PinAttempts) Fail(ctx context.Context, phone string) error {
	n, err := p.rdb.Incr(ctx, p.key(phone)).Result()
	if err != nil {
		return apperr.Internal("Could not check the PIN").WithCause(err)
	}
	if n == 1 || n == maxPinFailures {
		if err := p.rdb.Expire(ctx, p.key(phone), pinLockout).Err(); err != nil {
			return apperr.Internal("Could not check the PIN").WithCause(err)
		}
	}
	return nil
}

func (p *PinAttempts) Reset(ctx context.Context, phone string) error {
	if err := p.rdb.Del(ctx, p.key(phone)).Err(); err != nil {
		return apperr.Internal("Could not check the PIN").WithCause(err)
	}
	return nil
}
