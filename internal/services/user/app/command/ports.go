package command

import (
	"context"
	"time"

	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// Users is the persistence port for accounts. Adapters/postgres implements
// it; command handlers depend on this interface, never on pgx directly.
type Users interface {
	ByPhone(ctx context.Context, phone string) (domain.User, error)
	ByID(ctx context.Context, id string) (domain.User, error)
	ExistsUsername(ctx context.Context, username string) (bool, error)
	Create(ctx context.Context, u domain.User) (domain.User, error)
}

// Sessions issues and revokes refresh tokens. Backed by Redis: a refresh
// token is a capability, not something worth a relational row.
type Sessions interface {
	Issue(ctx context.Context, userID string) (refreshToken string, err error)
	// Consume returns the user id a refresh token was issued for and revokes
	// it in the same operation — a refresh token is single use, so a stolen
	// one that gets replayed after the legitimate client already rotated it
	// is rejected rather than honoured.
	Consume(ctx context.Context, refreshToken string) (userID string, err error)
	Revoke(ctx context.Context, refreshToken string) error
}

// Verifications sends and checks a one-time code sent to a phone number.
type Verifications interface {
	Start(ctx context.Context, phone string) error
	Check(ctx context.Context, phone, code string) (bool, error)
}

// PendingProfile is what Register collects before the phone number is
// proved. Held in Redis with the same TTL as the code itself, so a
// registration that is never verified simply expires rather than needing
// cleanup.
type PendingProfile struct {
	Username  string
	Name      string
	Email     string
	Birthdate *time.Time
}

// PendingRegistrations holds a registration's submitted profile between
// Register (which cannot create the account yet — the phone number is
// unproved) and VerifyOtp (which can).
type PendingRegistrations interface {
	Save(ctx context.Context, phone string, profile PendingProfile, ttl time.Duration) error
	// Consume returns the profile and deletes it — a pending registration is
	// claimed by exactly one successful verification.
	Consume(ctx context.Context, phone string) (PendingProfile, bool, error)
}
