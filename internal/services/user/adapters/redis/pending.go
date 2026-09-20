package redis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
	"github.com/koobam/wuzz-backend/internal/services/user/app/command"
)

type PendingRegistrations struct{ rdb *redisx.Client }

func NewPendingRegistrations(rdb *redisx.Client) *PendingRegistrations {
	return &PendingRegistrations{rdb: rdb}
}

func (p *PendingRegistrations) key(phone string) string {
	return p.rdb.Key("pending-registration", phone)
}

func (p *PendingRegistrations) Save(ctx context.Context, phone string, profile command.PendingProfile, ttl time.Duration) error {
	payload, err := json.Marshal(profile)
	if err != nil {
		return apperr.Internal("Could not start registration").WithCause(err)
	}
	if err := p.rdb.Set(ctx, p.key(phone), payload, ttl).Err(); err != nil {
		return apperr.Internal("Could not start registration").WithCause(err)
	}
	return nil
}

// Consume returns the pending profile and deletes it — a registration is
// claimed by exactly one successful verification.
func (p *PendingRegistrations) Consume(ctx context.Context, phone string) (command.PendingProfile, bool, error) {
	raw, err := p.rdb.GetDel(ctx, p.key(phone)).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return command.PendingProfile{}, false, nil
		}
		return command.PendingProfile{}, false, apperr.Internal("Could not complete registration").WithCause(err)
	}

	var profile command.PendingProfile
	if err := json.Unmarshal([]byte(raw), &profile); err != nil {
		return command.PendingProfile{}, false, apperr.Internal("Could not complete registration").WithCause(err)
	}
	return profile, true, nil
}
