package query

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strings"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// Users is the read-side view of the same port command.Users defines. Kept
// as its own (smaller) interface per the CQRS split: a query handler should
// not be handed the ability to write just because the two happen to share an
// adapter.
type Users interface {
	ByID(ctx context.Context, id string) (domain.User, error)
	ExistsUsername(ctx context.Context, username string) (bool, error)
}

// ---------- GetUser ----------

type GetUser struct {
	UserID string
}

// Accounts is the read side of the admin and staff tables.
type Accounts interface {
	ByID(ctx context.Context, id string) (domain.Account, error)
}

type GetUserHandler struct {
	users    Users
	accounts Accounts
}

func NewGetUserHandler(users Users, accounts Accounts) *GetUserHandler {
	return &GetUserHandler{users: users, accounts: accounts}
}

// Profile is who the access token names: a customer, or an admin or staff member.
type Profile struct {
	User    domain.User
	Account *domain.Account
}

func (h *GetUserHandler) Handle(ctx context.Context, q GetUser) (Profile, error) {
	if q.UserID == "" {
		return Profile{}, apperr.Unauthorized("Authentication is required")
	}
	if _, _, isAccount := domain.ParseAccountID(q.UserID); isAccount {
		a, err := h.accounts.ByID(ctx, q.UserID)
		return Profile{Account: &a}, err
	}
	u, err := h.users.ByID(ctx, q.UserID)
	return Profile{User: u}, err
}

// ---------- CheckUsername ----------

type CheckUsername struct {
	Username string
}

type CheckUsernameHandler struct{ users Users }

func NewCheckUsernameHandler(users Users) *CheckUsernameHandler {
	return &CheckUsernameHandler{users: users}
}

func (h *CheckUsernameHandler) Handle(ctx context.Context, q CheckUsername) (bool, error) {
	username, err := domain.NormaliseUsername(q.Username)
	if err != nil {
		return false, err
	}
	taken, err := h.users.ExistsUsername(ctx, username)
	if err != nil {
		return false, err
	}
	return !taken, nil
}

// ---------- SuggestUsername ----------

type SuggestUsername struct {
	Name      string
	Username  string
	BirthYear string
}

type SuggestUsernameHandler struct{ users Users }

func NewSuggestUsernameHandler(users Users) *SuggestUsernameHandler {
	return &SuggestUsernameHandler{users: users}
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Handle generates candidate handles from whichever of name/username the
// caller already typed, appending digits until enough of them are free.
//
// Checked one at a time against Postgres rather than generated purely at
// random: a suggestion nobody can actually claim is worse than a slower
// response, and the base word (from the person's own name) matters more to
// them than the app guessing something unrelated but available.
func (h *SuggestUsernameHandler) Handle(ctx context.Context, q SuggestUsername) ([]string, error) {
	base := slugify(q.Username)
	if base == "" {
		base = slugify(firstWord(q.Name))
	}
	if base == "" {
		base = "user"
	}
	if len(base) > 15 {
		base = base[:15]
	}

	const wanted = 5
	suggestions := make([]string, 0, wanted)
	seen := map[string]bool{}

	tryCandidate := func(candidate string) (bool, error) {
		if len(candidate) < 3 || len(candidate) > 20 || seen[candidate] {
			return false, nil
		}
		seen[candidate] = true
		taken, err := h.users.ExistsUsername(ctx, candidate)
		if err != nil {
			return false, err
		}
		if !taken {
			suggestions = append(suggestions, candidate)
		}
		return !taken, nil
	}

	if _, err := tryCandidate(base); err != nil {
		return nil, err
	}

	for attempt := 0; len(suggestions) < wanted && attempt < 40; attempt++ {
		suffix := rand.Intn(9999) //nolint:gosec // suggestions, not a security token
		if _, err := tryCandidate(fmt.Sprintf("%s%d", base, suffix)); err != nil {
			return nil, err
		}
	}

	return suggestions, nil
}

func firstWord(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnum.ReplaceAllString(s, "")
	return s
}
