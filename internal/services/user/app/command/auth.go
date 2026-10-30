package command

import (
	"context"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// Accounts is the persistence port for admins and staff, the two tables that are not
// customers. Sign-in reads them by phone or id.
type Accounts interface {
	ByPhone(ctx context.Context, phone string) (domain.Account, error)
	// ByID takes the prefixed id (see domain.AccountID).
	ByID(ctx context.Context, id string) (domain.Account, error)
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

type Session struct {
	AccessToken  string
	RefreshToken string
	// User is the customer; Account is set instead for an admin or staff session.
	User                 domain.User
	Account              *domain.Account
	AccessTokenExpiresAt time.Time
}

// ---------- Register ----------

// Register carries a full profile but does not create a user: the phone
// number has not been proved yet, and giving a not-yet-verified number the
// username it asked for would let someone squat a handle they cannot yet
// prove they own. The profile is held until VerifyOtp claims it.
type Register struct {
	Username  string
	Name      string
	Phone     string
	Email     string
	Birthdate *time.Time
}

type RegisterResult struct {
	Phone     string
	ExpiresAt time.Time
}

type RegisterHandler struct {
	users         Users
	pending       PendingRegistrations
	verifications Verifications
	codeTTL       time.Duration
}

func NewRegisterHandler(users Users, pending PendingRegistrations, verifications Verifications, codeTTL time.Duration) *RegisterHandler {
	return &RegisterHandler{users: users, pending: pending, verifications: verifications, codeTTL: codeTTL}
}

func (h *RegisterHandler) Handle(ctx context.Context, cmd Register) (RegisterResult, error) {
	username, err := domain.NormaliseUsername(cmd.Username)
	if err != nil {
		return RegisterResult{}, err
	}
	phone, err := domain.NormalisePhone(cmd.Phone)
	if err != nil {
		return RegisterResult{}, err
	}

	// Checked here for a fast, friendly error — but not relied on for
	// correctness. The unique index on users.username is what actually
	// prevents two accounts claiming the same handle; this check only avoids
	// sending an SMS for a registration that would fail at the last step
	// anyway.
	taken, err := h.users.ExistsUsername(ctx, username)
	if err != nil {
		return RegisterResult{}, err
	}
	if taken {
		return RegisterResult{}, apperr.Conflict("This username is already taken")
	}

	if err := h.pending.Save(ctx, phone, PendingProfile{
		Username:  username,
		Name:      cmd.Name,
		Email:     cmd.Email,
		Birthdate: cmd.Birthdate,
	}, h.codeTTL); err != nil {
		return RegisterResult{}, err
	}

	if err := h.verifications.Start(ctx, phone); err != nil {
		return RegisterResult{}, err
	}

	return RegisterResult{Phone: phone, ExpiresAt: time.Now().Add(h.codeTTL)}, nil
}

// ---------- VerifyOtp ----------

// VerifyOtp checks the code, then either creates the account a matching
// Register call staged (first time) or signs in the existing one (the same
// number proving itself again) — one call, because the client cannot tell
// which case it is in ahead of time, and does not need to.
type VerifyOtp struct {
	Phone string
	Code  string
	// Audience is empty for the customer app. For /backoffice, /pos and /station it names the
	// staff audience, and the number is looked up among admins and staff instead of customers.
	Audience string
}

type VerifyOtpHandler struct {
	users         Users
	accounts      Accounts
	pending       PendingRegistrations
	verifications Verifications
	sessions      Sessions
	signer        *authn.Signer
	accessTTL     time.Duration
}

func NewVerifyOtpHandler(users Users, accounts Accounts, pending PendingRegistrations, verifications Verifications, sessions Sessions, signer *authn.Signer, accessTTL time.Duration) *VerifyOtpHandler {
	return &VerifyOtpHandler{users: users, accounts: accounts, pending: pending, verifications: verifications, sessions: sessions, signer: signer, accessTTL: accessTTL}
}

func (h *VerifyOtpHandler) Handle(ctx context.Context, cmd VerifyOtp) (Session, error) {
	phone, err := domain.NormalisePhone(cmd.Phone)
	if err != nil {
		return Session{}, apperr.Unauthorized("The code is incorrect or has expired")
	}

	ok, err := h.verifications.Check(ctx, phone, cmd.Code)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		// One message for a wrong code and an expired one: which of the two it
		// was tells an attacker whether the number is mid-verification.
		return Session{}, apperr.Unauthorized("The code is incorrect or has expired")
	}

	if cmd.Audience != "" {
		return h.signInAccount(ctx, phone, cmd.Audience)
	}

	user, err := h.users.ByPhone(ctx, phone)
	if err != nil {
		if !apperr.IsKind(err, apperr.KindNotFound) {
			return Session{}, err
		}
		user, err = h.createFromPending(ctx, phone)
		if err != nil {
			return Session{}, err
		}
	}

	if !user.CanSignIn() {
		if user.IsBanned() {
			return Session{}, apperr.Forbidden("Your account has been disabled. Please contact Customer Service.")
		}
		return Session{}, apperr.Forbidden("Your account is inactive. Please contact Customer Service.")
	}

	return issueSession(ctx, h.sessions, h.signer, h.accessTTL, user)
}

// signInAccount signs an admin or staff member in to one of the staff audiences. An unknown number,
// or one that may not use the audience, gets the same refusal, so the answer does not say which
// numbers belong to staff.
func (h *VerifyOtpHandler) signInAccount(ctx context.Context, phone, audience string) (Session, error) {
	account, err := h.accounts.ByPhone(ctx, phone)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return Session{}, apperr.Forbidden("This account cannot sign in here")
		}
		return Session{}, err
	}
	if !account.MayUse(audience) {
		return Session{}, apperr.Forbidden("This account cannot sign in here")
	}
	if !account.CanSignIn() {
		return Session{}, apperr.Forbidden("This account is not active. Please contact support.")
	}
	return issueAccountSession(ctx, h.sessions, h.signer, h.accessTTL, account)
}

func (h *VerifyOtpHandler) createFromPending(ctx context.Context, phone string) (domain.User, error) {
	profile, found, err := h.pending.Consume(ctx, phone)
	if err != nil {
		return domain.User{}, err
	}
	if !found {
		// The code was valid — Twilio proved the number — but nobody ever
		// called Register for it, or the pending profile's TTL beat them to
		// VerifyOtp. Either way there is no profile to create an account from.
		return domain.User{}, apperr.Invalid("Start registration again before verifying this number")
	}

	user, err := domain.NewUser(profile.Username, profile.Name, phone, profile.Email, profile.Birthdate)
	if err != nil {
		return domain.User{}, err
	}
	return h.users.Create(ctx, user)
}

// ---------- Refresh ----------

type Refresh struct {
	RefreshToken string
}

type RefreshHandler struct {
	users     Users
	accounts  Accounts
	sessions  Sessions
	signer    *authn.Signer
	accessTTL time.Duration
}

func NewRefreshHandler(users Users, accounts Accounts, sessions Sessions, signer *authn.Signer, accessTTL time.Duration) *RefreshHandler {
	return &RefreshHandler{users: users, accounts: accounts, sessions: sessions, signer: signer, accessTTL: accessTTL}
}

func (h *RefreshHandler) Handle(ctx context.Context, cmd Refresh) (Session, error) {
	userID, err := h.sessions.Consume(ctx, cmd.RefreshToken)
	if err != nil {
		return Session{}, err
	}

	if _, _, isAccount := domain.ParseAccountID(userID); isAccount {
		account, err := h.accounts.ByID(ctx, userID)
		if err != nil || !account.CanSignIn() {
			return Session{}, apperr.Unauthorized("This session is no longer valid")
		}
		return issueAccountSession(ctx, h.sessions, h.signer, h.accessTTL, account)
	}

	user, err := h.users.ByID(ctx, userID)
	if err != nil {
		return Session{}, apperr.Unauthorized("This session is no longer valid")
	}
	if !user.CanSignIn() {
		return Session{}, apperr.Unauthorized("This session is no longer valid")
	}
	return issueSession(ctx, h.sessions, h.signer, h.accessTTL, user)
}

// ---------- Logout ----------

type Logout struct {
	RefreshToken string
}

type LogoutHandler struct{ sessions Sessions }

func NewLogoutHandler(sessions Sessions) *LogoutHandler { return &LogoutHandler{sessions: sessions} }

func (h *LogoutHandler) Handle(ctx context.Context, cmd Logout) (struct{}, error) {
	return struct{}{}, h.sessions.Revoke(ctx, cmd.RefreshToken)
}

// ---------- SendLoginCode ----------

// SendLoginCode starts sign-in for an existing admin or staff account. It answers the
// same whether or not the number belongs to staff: a different answer would let
// anyone probe which phone numbers are back-office accounts.
type SendLoginCode struct{ Phone string }

type SendLoginCodeHandler struct {
	accounts      Accounts
	verifications Verifications
	codeTTL       time.Duration
}

func NewSendLoginCodeHandler(accounts Accounts, verifications Verifications, codeTTL time.Duration) *SendLoginCodeHandler {
	return &SendLoginCodeHandler{accounts: accounts, verifications: verifications, codeTTL: codeTTL}
}

func (h *SendLoginCodeHandler) Handle(ctx context.Context, cmd SendLoginCode) (time.Time, error) {
	phone, err := domain.NormalisePhone(cmd.Phone)
	if err != nil {
		return time.Time{}, err
	}
	expires := time.Now().Add(h.codeTTL)

	account, err := h.accounts.ByPhone(ctx, phone)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return expires, nil
		}
		return time.Time{}, err
	}
	if !account.CanSignIn() {
		return expires, nil
	}
	if err := h.verifications.Start(ctx, phone); err != nil {
		return time.Time{}, err
	}
	return expires, nil
}

// ---------- DevLogin ----------

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
	// Customers only: admins and staff live in their own tables, which this never reads.
	if !user.CanSignIn() {
		return Session{}, apperr.Forbidden("This account cannot sign in")
	}
	return issueSession(ctx, h.sessions, h.signer, h.accessTTL, user)
}

func issueSession(ctx context.Context, sessions Sessions, signer *authn.Signer, accessTTL time.Duration, user domain.User) (Session, error) {
	s, err := issue(ctx, sessions, signer, accessTTL, authn.Identity{UserID: user.ID, Email: user.Email, Name: user.Name})
	s.User = user
	return s, err
}

func issueAccountSession(ctx context.Context, sessions Sessions, signer *authn.Signer, accessTTL time.Duration, account domain.Account) (Session, error) {
	s, err := issue(ctx, sessions, signer, accessTTL, authn.Identity{UserID: account.ID, Name: account.Name, Roles: account.Roles()})
	s.Account = &account
	return s, err
}

func issue(ctx context.Context, sessions Sessions, signer *authn.Signer, accessTTL time.Duration, id authn.Identity) (Session, error) {
	accessToken, err := signer.Issue(id)
	if err != nil {
		return Session{}, err
	}

	refreshToken, err := sessions.Issue(ctx, id.UserID)
	if err != nil {
		return Session{}, err
	}

	return Session{
		AccessToken:          accessToken,
		RefreshToken:         refreshToken,
		AccessTokenExpiresAt: time.Now().Add(accessTTL),
	}, nil
}
