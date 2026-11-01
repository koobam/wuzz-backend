package command

import (
	"context"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

type fakePins struct {
	hashes     map[string]string
	reverify   map[string]bool
	setCalls   int
	clearCalls int
}

func (f *fakePins) GetPinHash(_ context.Context, id string) (string, error) { return f.hashes[id], nil }
func (f *fakePins) SetPin(_ context.Context, id, hash string) error {
	f.setCalls++
	f.hashes[id] = hash
	delete(f.reverify, id)
	return nil
}
func (f *fakePins) ClearReverify(_ context.Context, id string) error {
	f.clearCalls++
	delete(f.reverify, id)
	return nil
}

// fakeLockout mirrors the Redis adapter's rule: 5 failures lock for 15 minutes.
type fakeLockout struct{ fails map[string]int }

func (f *fakeLockout) Locked(_ context.Context, phone string) (time.Duration, error) {
	if f.fails[phone] >= 5 {
		return 15 * time.Minute, nil
	}
	return 0, nil
}
func (f *fakeLockout) Fail(_ context.Context, phone string) error { f.fails[phone]++; return nil }
func (f *fakeLockout) Reset(_ context.Context, phone string) error {
	delete(f.fails, phone)
	return nil
}

type fakeSessions struct{}

func (fakeSessions) Issue(context.Context, string) (string, error)   { return "refresh", nil }
func (fakeSessions) Consume(context.Context, string) (string, error) { return "", nil }
func (fakeSessions) Revoke(context.Context, string) error            { return nil }

func newTestSigner(t *testing.T) *authn.Signer {
	t.Helper()
	signer, err := authn.NewSigner(config.Auth{Issuer: "test", AccessTokenTTL: time.Hour})
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return signer
}

func TestLoginWithPin(t *testing.T) {
	const phone = "6281234500001"
	hash, err := bcrypt.GenerateFromPassword([]byte("482916"), pinCost)
	if err != nil {
		t.Fatal(err)
	}
	customers := fakeCustomers{byPhone: map[string]domain.User{
		phone:           {ID: "1", Status: domain.StatusActive, HasPin: true},
		"6281234500002": {ID: "2", Status: domain.StatusActive},
		"6281234500003": {ID: "3", Status: domain.StatusBanned, HasPin: true},
		"6281234500004": {ID: "4", Status: domain.StatusActive, HasPin: true, ReverifyRequired: true},
	}}
	newHandler := func() (*LoginWithPinHandler, *fakeLockout) {
		pins := &fakePins{hashes: map[string]string{"1": string(hash), "3": string(hash), "4": string(hash)}}
		lockout := &fakeLockout{fails: map[string]int{}}
		return NewLoginWithPinHandler(customers, pins, lockout, fakeSessions{}, newTestSigner(t), time.Hour), lockout
	}
	login := func(h *LoginWithPinHandler, p, pin string) (Session, error) {
		return h.Handle(context.Background(), LoginWithPin{Phone: p, Pin: pin})
	}

	t.Run("the right PIN signs in and resets the counter", func(t *testing.T) {
		h, lockout := newHandler()
		lockout.fails[phone] = 3
		s, err := login(h, "081234500001", "482916")
		if err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if s.AccessToken == "" || s.RefreshToken == "" || s.User.ID != "1" {
			t.Errorf("incomplete session: %+v", s)
		}
		if lockout.fails[phone] != 0 {
			t.Errorf("failures not reset: %d", lockout.fails[phone])
		}
	})

	t.Run("a wrong PIN, an unknown number and a customer with no PIN share one error", func(t *testing.T) {
		h, lockout := newHandler()
		for _, p := range []string{"081234500001", "081234500099", "081234500002"} {
			_, err := login(h, p, "111222")
			if !apperr.IsKind(err, apperr.KindUnauthorized) || err.Error() != errWrongPin {
				t.Errorf("%s: got %v, want the generic unauthorized error", p, err)
			}
		}
		if lockout.fails[phone] != 1 || lockout.fails["6281234500099"] != 1 {
			t.Errorf("failures not counted: %v", lockout.fails)
		}
	})

	t.Run("five wrong PINs lock the number, even for the right PIN", func(t *testing.T) {
		h, _ := newHandler()
		for i := 0; i < 5; i++ {
			if _, err := login(h, "081234500001", "111222"); !apperr.IsKind(err, apperr.KindUnauthorized) {
				t.Fatalf("attempt %d: %v", i, err)
			}
		}
		_, err := login(h, "081234500001", "482916")
		if !apperr.IsKind(err, apperr.KindRateLimited) {
			t.Fatalf("got %v, want a rate limit error", err)
		}
		if want := "Too many attempts, try again in 15 minutes"; err.Error() != want {
			t.Errorf("message %q, want %q", err.Error(), want)
		}
	})

	t.Run("a disabled customer is refused after the PIN is right", func(t *testing.T) {
		h, _ := newHandler()
		if _, err := login(h, "081234500003", "482916"); !apperr.IsKind(err, apperr.KindForbidden) {
			t.Errorf("got %v, want forbidden", err)
		}
	})

	t.Run("a customer flagged to verify again cannot use the PIN", func(t *testing.T) {
		h, _ := newHandler()
		if _, err := login(h, "081234500004", "482916"); !apperr.IsKind(err, apperr.KindForbidden) {
			t.Errorf("got %v, want forbidden", err)
		}
	})
}

func TestSetPin(t *testing.T) {
	pins := &fakePins{hashes: map[string]string{}, reverify: map[string]bool{"7": true}}
	h := NewSetPinHandler(pins)

	if _, err := h.Handle(context.Background(), SetPin{UserID: "7", Pin: "123456"}); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Errorf("a weak PIN: got %v, want invalid", err)
	}
	if pins.setCalls != 0 {
		t.Error("a refused PIN was stored")
	}

	if _, err := h.Handle(context.Background(), SetPin{UserID: "7", Pin: "482916"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(pins.hashes["7"]), []byte("482916")) != nil {
		t.Error("the stored hash does not match the PIN")
	}
	if pins.hashes["7"] == "482916" {
		t.Error("the PIN was stored in the clear")
	}
	if pins.reverify["7"] {
		t.Error("reverify_required was not cleared")
	}
}
