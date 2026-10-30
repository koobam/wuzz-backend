package domain

import (
	"strconv"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Audiences a staff sign-in can ask for. The customer app asks for none.
const (
	AudienceBackoffice = "backoffice"
	AudiencePOS        = "pos"
	// AudienceStation is the Wuzzer app: the technician who is also the driver.
	AudienceStation = "station"
)

// The two kinds of account that are not customers; each has its own table.
const (
	KindAdmin = "admin"
	KindStaff = "staff"
)

// Staff roles that the sign-in rules know by name. Which roles exist at all is data
// (the admin_roles and staff_roles tables); these two are the ones with behaviour.
const (
	RoleFront  = "front"
	RoleWuzzer = "wuzzer"
)

// Account is a back-office admin or a staff member. A customer is a User.
type Account struct {
	// ID carries the kind ("a:7", "s:3") so a session or token can be traced to its table.
	ID    string
	Kind  string
	Name  string
	Phone string
	// Role is the row's role: finance, support... for an admin; front, wuzzer for staff.
	Role string
	// OutletID is set for front staff, and for an admin assigned to an outlet; it is what binds them to a till.
	OutletID  int64
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AccountID builds the prefixed id of a row.
func AccountID(kind string, n int64) string {
	prefix := "s"
	if kind == KindAdmin {
		prefix = "a"
	}
	return prefix + ":" + strconv.FormatInt(n, 10)
}

// ParseAccountID splits "a:7" into its kind and row id. ok is false for anything else,
// which is how a customer's plain numeric id is told apart.
func ParseAccountID(id string) (kind string, n int64, ok bool) {
	prefix, rest, found := strings.Cut(id, ":")
	if !found {
		return "", 0, false
	}
	switch prefix {
	case "a":
		kind = KindAdmin
	case "s":
		kind = KindStaff
	default:
		return "", 0, false
	}
	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return kind, n, true
}

// Roles are the claims the access token carries: "admin" and "admin:<role>" for an admin,
// "outlet:<id>" for front staff, "wuzzer" for a Wuzzer.
func (a Account) Roles() []string {
	if a.Kind == KindAdmin {
		roles := []string{"admin", "admin:" + a.Role}
		if a.OutletID != 0 {
			roles = append(roles, "outlet:"+strconv.FormatInt(a.OutletID, 10))
		}
		return roles
	}
	switch a.Role {
	case RoleFront:
		return []string{"outlet:" + strconv.FormatInt(a.OutletID, 10)}
	case RoleWuzzer:
		return []string{RoleWuzzer}
	}
	return nil
}

// MayUse reports whether the account may sign in to the given audience. An admin may use
// the backoffice and the station app, and the POS only if assigned an outlet; front staff only the POS; a Wuzzer only the station app.
func (a Account) MayUse(audience string) bool {
	switch audience {
	case AudienceBackoffice:
		return a.Kind == KindAdmin
	case AudiencePOS:
		// A till is one outlet's: front staff use it, and an admin who has been assigned an outlet. An admin with none is
		// not any till's, so the POS refuses them.
		return (a.Kind == KindStaff && a.Role == RoleFront) || (a.Kind == KindAdmin && a.OutletID != 0)
	case AudienceStation:
		return a.Kind == KindAdmin || (a.Kind == KindStaff && a.Role == RoleWuzzer)
	default:
		return false
	}
}

func (a Account) IsBanned() bool  { return a.Status == StatusBanned }
func (a Account) CanSignIn() bool { return a.Status == StatusActive }

// Validate checks what the tables cannot: front staff belong to an outlet, others to none.
func (a Account) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return apperr.Invalid("A name is required")
	}
	if a.Kind == KindStaff && (a.Role == RoleFront) != (a.OutletID != 0) {
		return apperr.Invalid("Front staff need an outlet, and only they have one")
	}
	return nil
}
