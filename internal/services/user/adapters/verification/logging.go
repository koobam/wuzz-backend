package verification

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
)

// Logging is the Verifications implementation used when no Twilio
// credentials are configured (dev, or a missing-credentials safety net). It
// never sends an SMS — the code is written to the log instead, so a
// developer can complete registration/login locally without a phone number
// that can actually receive one.
type Logging struct {
	rdb *redisx.Client
	log *slog.Logger
	ttl time.Duration
	// fixed, when not empty, is the code every number gets (dev and staging only; config.Load refuses it
	// anywhere else), so nobody has to dig the code out of a log.
	fixed string
}

func NewLogging(rdb *redisx.Client, log *slog.Logger, ttl time.Duration, fixed string) *Logging {
	return &Logging{rdb: rdb, log: log, ttl: ttl, fixed: fixed}
}

func (l *Logging) key(phone string) string { return l.rdb.Key("verification-code", phone) }

func (l *Logging) Start(ctx context.Context, phone string) error {
	code, err := l.fixed, error(nil)
	if code == "" {
		code, err = randomCode()
	}
	if err != nil {
		return apperr.Internal("Could not start verification").WithCause(err)
	}
	if err := l.rdb.Set(ctx, l.key(phone), code, l.ttl).Err(); err != nil {
		return apperr.Internal("Could not start verification").WithCause(err)
	}
	l.log.Warn("verification code (dev mode — no SMS sent)",
		slog.String("phone", phone), slog.String("code", code))
	return nil
}

func (l *Logging) Check(ctx context.Context, phone, code string) (bool, error) {
	stored, err := l.rdb.Get(ctx, l.key(phone)).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return false, nil
		}
		return false, apperr.Internal("Could not verify the code").WithCause(err)
	}
	if stored != code {
		return false, nil
	}
	// Single use, same as a real SMS code: once checked, it cannot be
	// replayed even though it is still sitting in the terminal's scrollback.
	_ = l.rdb.Del(ctx, l.key(phone)).Err()
	return true, nil
}

func randomCode() (string, error) {
	buf := make([]byte, 1)
	digits := make([]byte, 6)
	for i := range digits {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		digits[i] = '0' + buf[0]%10
	}
	return string(digits), nil
}
