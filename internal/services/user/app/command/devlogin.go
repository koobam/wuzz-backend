package command

import (
	"context"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// DevLogin signs an existing customer in by phone, with no code. It exists so a
// developer can use the app on a simulator without reading an SMS code out of a
// log. It is only ever constructed when the service runs with APP_ENV=dev and
// the dev verifier (see cmd/user/main.go); everywhere else the handler is nil
// and the RPC answers NotFound.
type DevLogin struct {
	Phone string
}

type DevLoginHandler struct {
	users     Users
	sessions  Sessions
	signer    *authn.Signer
	accessTTL time.Duration
}

func NewDevLoginHandler(users Users, sessions Sessions, signer *authn.Signer, accessTTL time.Duration) *DevLoginHandler {
	return &DevLoginHandler{users: users, sessions: sessions, signer: signer, accessTTL: accessTTL}
}

func (h *DevLoginHandler) Handle(ctx context.Context, cmd DevLogin) (Session, error) {
	phone, err := domain.NormalisePhone(cmd.Phone)
	if err != nil {
		return Session{}, err
	}
	user, err := h.users.ByPhone(ctx, phone)
	if err != nil {
		return Session{}, err
	}
	// Customers only. A customer session carries the account's roles in its
	// token, so signing a staff account in here would hand out its admin or
	// outlet access with no proof at all.
	if user.IsStaff() {
		return Session{}, apperr.Forbidden("Staff accounts cannot use the development sign-in")
	}
	if !user.CanSignIn() {
		return Session{}, apperr.Forbidden("This account cannot sign in")
	}
	return issueSession(ctx, h.sessions, h.signer, h.accessTTL, user)
}
