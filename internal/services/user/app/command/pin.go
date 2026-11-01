package command

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// pinCost is the bcrypt work factor. A PIN has only a million values, so the hash is slowing a leaked database down a little,
// not protecting it; the lockout is what stops guessing in the app.
const pinCost = 10

// PinStore keeps the PIN hash. It is its own port so the hash stays out of domain.User and nothing but this command layer ever
// sees it.
type PinStore interface {
	// GetPinHash returns "" when the customer has no PIN.
	GetPinHash(ctx context.Context, userID string) (string, error)
	// SetPin stores the hash and clears the "verify again" flag in one step.
	SetPin(ctx context.Context, userID, hash string) error
	ClearReverify(ctx context.Context, userID string) error
}

// PinLockout counts wrong PINs per phone number. Backed by Redis: an attempt counter is a throwaway with a TTL.
type PinLockout interface {
	// Locked returns how long the number is refused for, or 0 when it may try.
	Locked(ctx context.Context, phone string) (time.Duration, error)
	// Fail records a wrong PIN.
	Fail(ctx context.Context, phone string) error
	// Reset forgets the failures after a correct PIN.
	Reset(ctx context.Context, phone string) error
}

var errWrongPin = "The phone number or PIN is incorrect"

// dummyPinHash is compared against when there is no real hash, so an unknown number costs the same time as a wrong PIN.
var dummyPinHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("000000"), pinCost)
	return h
})

// ---------- LoginWithPin ----------

// LoginWithPin signs a customer in with the PIN they set. An unknown number, a customer with no PIN and a wrong PIN all get one
// answer, so it cannot be used to find out who has an account or a PIN.
type LoginWithPin struct {
	Phone string
	Pin   string
}

type LoginWithPinHandler struct {
	users     Users
	pins      PinStore
	lockout   PinLockout
	sessions  Sessions
	signer    *authn.Signer
	accessTTL time.Duration
}

func NewLoginWithPinHandler(users Users, pins PinStore, lockout PinLockout, sessions Sessions, signer *authn.Signer, accessTTL time.Duration) *LoginWithPinHandler {
	return &LoginWithPinHandler{users: users, pins: pins, lockout: lockout, sessions: sessions, signer: signer, accessTTL: accessTTL}
}

func (h *LoginWithPinHandler) Handle(ctx context.Context, cmd LoginWithPin) (Session, error) {
	phone, err := domain.NormalisePhone(cmd.Phone)
	if err != nil {
		return Session{}, apperr.Unauthorized(errWrongPin)
	}

	wait, err := h.lockout.Locked(ctx, phone)
	if err != nil {
		return Session{}, err
	}
	if wait > 0 {
		return Session{}, apperr.RateLimited(fmt.Sprintf("Too many attempts, try again in %d minutes", int((wait+time.Minute-1)/time.Minute)))
	}

	user, hash, err := h.lookup(ctx, phone)
	if err != nil {
		return Session{}, err
	}
	if hash == "" {
		// Still pay for a bcrypt compare: a quick answer would tell an unknown number from a wrong PIN.
		_ = bcrypt.CompareHashAndPassword(dummyPinHash(), []byte(cmd.Pin))
		return Session{}, h.wrong(ctx, phone)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(cmd.Pin)) != nil {
		return Session{}, h.wrong(ctx, phone)
	}
	if err := h.lockout.Reset(ctx, phone); err != nil {
		return Session{}, err
	}

	if !user.CanSignIn() {
		if user.IsBanned() {
			return Session{}, apperr.Forbidden("Your account has been disabled. Please contact Customer Service.")
		}
		return Session{}, apperr.Forbidden("Your account is inactive. Please contact Customer Service.")
	}
	// Flagged to verify again: the PIN alone is no longer enough until a code proves the number.
	if user.ReverifyRequired {
		return Session{}, apperr.Forbidden("Please verify your phone number with a code to sign in")
	}

	return issueSession(ctx, h.sessions, h.signer, h.accessTTL, user)
}

// lookup returns the customer and their PIN hash, or an empty hash for an unknown number or one with no PIN.
func (h *LoginWithPinHandler) lookup(ctx context.Context, phone string) (domain.User, string, error) {
	user, err := h.users.ByPhone(ctx, phone)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return domain.User{}, "", nil
		}
		return domain.User{}, "", err
	}
	if !user.HasPin {
		return user, "", nil
	}
	hash, err := h.pins.GetPinHash(ctx, user.ID)
	return user, hash, err
}

// wrong counts a failed attempt and returns the one error every failure shares.
func (h *LoginWithPinHandler) wrong(ctx context.Context, phone string) error {
	if err := h.lockout.Fail(ctx, phone); err != nil {
		return err
	}
	return apperr.Unauthorized(errWrongPin)
}

// ---------- SetPin ----------

// SetPin sets or changes the signed-in customer's PIN. The caller is whoever the access token names; the handler trusts UserID
// the way every authenticated command does.
type SetPin struct {
	UserID string
	Pin    string
}

type SetPinHandler struct{ pins PinStore }

func NewSetPinHandler(pins PinStore) *SetPinHandler { return &SetPinHandler{pins: pins} }

func (h *SetPinHandler) Handle(ctx context.Context, cmd SetPin) (struct{}, error) {
	if err := domain.ValidatePin(cmd.Pin); err != nil {
		return struct{}{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cmd.Pin), pinCost)
	if err != nil {
		return struct{}{}, apperr.Internal("Could not save the PIN").WithCause(err)
	}
	return struct{}{}, h.pins.SetPin(ctx, cmd.UserID, string(hash))
}
