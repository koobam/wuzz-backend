package query

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
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

// Accounts is the read side of the admin and staff tables.
type Accounts interface {
	ByID(ctx context.Context, id string) (domain.Account, error)
}

// UserList and AccountList are what the back-office listings need of the store. A listing is newest first by default, and says how
// many rows match as well as returning one page.
type UserList interface {
	List(ctx context.Context, f domain.ListFilter) ([]domain.User, int64, error)
}

// UserResolver finds customers by id or by phone number, in one query; what is not found is absent from the answer.
type UserResolver interface {
	Resolve(ctx context.Context, ids []int64, phones []string) ([]domain.User, error)
}

type AccountList interface {
	// List pages the accounts of one kind (domain.KindAdmin or KindStaff).
	List(ctx context.Context, kind string, f domain.ListFilter) ([]domain.Account, int64, error)
}

// ---------- GetUser ----------

type GetUser struct {
	UserID string
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

// ---------- ListUsers ----------

type ListUsers struct {
	Query, Status   string
	SortBy, SortDir string
	Limit, Offset   int
}

type ListUsersResult struct {
	Users []domain.User
	// Total is how many users the filter matches, not how many are in this page.
	Total int64
}

type ListUsersHandler struct{ users UserList }

func NewListUsersHandler(users UserList) *ListUsersHandler { return &ListUsersHandler{users: users} }

func (h *ListUsersHandler) Handle(ctx context.Context, q ListUsers) (ListUsersResult, error) {
	if q.Status != "" && !domain.ValidStatus(q.Status) {
		return ListUsersResult{}, apperr.Invalid("status must be active, inactive or banned")
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 25
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	by, dir, err := sortOf(userSorts, q.SortBy, q.SortDir)
	if err != nil {
		return ListUsersResult{}, err
	}
	users, total, err := h.users.List(ctx, domain.ListFilter{Query: q.Query, Status: q.Status, SortBy: by, SortDir: dir, Limit: q.Limit, Offset: q.Offset})
	return ListUsersResult{Users: users, Total: total}, err
}

// ---------- ListAccounts ----------

type ListAccounts struct {
	// Kind is domain.KindAdmin or domain.KindStaff.
	Kind            string
	Query, Status   string
	OutletID        int64
	SortBy, SortDir string
	Limit, Offset   int
}

type ListAccountsResult struct {
	Accounts []domain.Account
	Total    int64
}

type ListAccountsHandler struct{ accounts AccountList }

func NewListAccountsHandler(accounts AccountList) *ListAccountsHandler {
	return &ListAccountsHandler{accounts: accounts}
}

func (h *ListAccountsHandler) Handle(ctx context.Context, q ListAccounts) (ListAccountsResult, error) {
	if q.Kind != domain.KindAdmin && q.Kind != domain.KindStaff {
		return ListAccountsResult{}, apperr.Invalid("kind must be admin or staff")
	}
	if q.Status != "" && !domain.ValidStatus(q.Status) {
		return ListAccountsResult{}, apperr.Invalid("status must be active, inactive or banned")
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 25
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	if q.OutletID < 0 {
		return ListAccountsResult{}, apperr.Invalid("outlet_id must not be negative")
	}
	by, dir, err := sortOf(accountSorts, q.SortBy, q.SortDir)
	if err != nil {
		return ListAccountsResult{}, err
	}
	accounts, total, err := h.accounts.List(ctx, q.Kind, domain.ListFilter{Query: q.Query, Status: q.Status, OutletID: q.OutletID, SortBy: by, SortDir: dir, Limit: q.Limit, Offset: q.Offset})
	return ListAccountsResult{Accounts: accounts, Total: total}, err
}

// The columns each listing may be ordered by. The store maps each name to its own SQL, so nothing a caller sends reaches a query.
var (
	userSorts    = map[string]bool{"id": true, "name": true, "created_at": true, "points": true}
	accountSorts = map[string]bool{"id": true, "name": true, "created_at": true}
)

// sortOf checks a requested order against the whitelist and fills in the defaults: with nothing asked it is newest first (id,
// descending), which is how the lists have always come; a column on its own is ascending; a direction on its own applies to id.
func sortOf(allowed map[string]bool, by, dir string) (string, string, error) {
	if by != "" && !allowed[by] {
		return "", "", apperr.Invalid("Unknown sort column")
	}
	if dir != "" && dir != "asc" && dir != "desc" {
		return "", "", apperr.Invalid("Sort direction must be asc or desc")
	}
	switch {
	case by == "" && dir == "":
		return "id", "desc", nil
	case by == "":
		return "id", dir, nil
	case dir == "":
		return by, "asc", nil
	}
	return by, dir, nil
}

// ---------- ResolveCustomers ----------

// maxResolve is how many ids and how many numbers one call may ask for: the most one injection of a voucher can name.
const maxResolve = 500

type ResolveCustomers struct {
	// IDs are customer ids, Phones are phone numbers as typed; both may be messy and are cleaned here.
	IDs, Phones []string
}

type ResolveCustomersHandler struct{ users UserResolver }

func NewResolveCustomersHandler(users UserResolver) *ResolveCustomersHandler {
	return &ResolveCustomersHandler{users: users}
}

type ResolveCustomersResult struct {
	Users []domain.User
	// Unmatched are the ids and numbers, as asked for, that found nobody: not a number, not a phone number, or no such customer.
	Unmatched []string
}

// Handle answers the customers found, each once.
func (h *ResolveCustomersHandler) Handle(ctx context.Context, q ResolveCustomers) (ResolveCustomersResult, error) {
	res := ResolveCustomersResult{Users: []domain.User{}, Unmatched: []string{}}
	if len(q.IDs) > maxResolve || len(q.Phones) > maxResolve {
		return res, apperr.Invalid(fmt.Sprintf("Ask for at most %d ids and %d phone numbers at once", maxResolve, maxResolve))
	}
	type asked struct {
		raw   string
		id    int64
		phone string
	}
	var ids []int64
	var phones []string
	var all []asked
	for _, raw := range q.IDs {
		a := asked{raw: raw}
		if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && n > 0 {
			a.id = n
			ids = append(ids, n)
		}
		all = append(all, a)
	}
	for _, raw := range q.Phones {
		a := asked{raw: raw}
		if p, err := domain.NormalisePhone(raw); err == nil {
			a.phone = p
			phones = append(phones, p)
		}
		all = append(all, a)
	}
	if len(ids) > 0 || len(phones) > 0 {
		users, err := h.users.Resolve(ctx, ids, phones)
		if err != nil {
			return res, err
		}
		res.Users = users
	}
	byID, byPhone := map[int64]bool{}, map[string]bool{}
	for _, u := range res.Users {
		if n, err := strconv.ParseInt(u.ID, 10, 64); err == nil {
			byID[n] = true
		}
		byPhone[u.Phone] = true
	}
	for _, a := range all {
		if !(a.id > 0 && byID[a.id]) && !(a.phone != "" && byPhone[a.phone]) {
			res.Unmatched = append(res.Unmatched, a.raw)
		}
	}
	return res, nil
}
