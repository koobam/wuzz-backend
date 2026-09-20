// Package domain holds user's business rules. It knows nothing about
// Postgres, gRPC or JSON: everything here is testable without a container.
package domain

import (
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Account statuses, mirroring the user_status enum in Postgres. Only Active
// may sign in.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
	StatusBanned   = "banned"
)

// defaultCountryCode is prepended to a number with no country code. Indonesia
// today; the moment that is not true, this constant is the single place that
// knows.
const defaultCountryCode = "62"

var usernamePattern = regexp.MustCompile(`^[a-z0-9._]{3,20}$`)

type User struct {
	ID        string
	Username  string
	Name      string
	// E.164 digits, no leading plus: country code and subscriber number
	// together, e.g. "6281234567890". See NormalisePhone.
	Phone     string
	Email     string
	Points    int64
	Streak    int64
	XP        int64
	Birthdate *time.Time
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (u User) IsBanned() bool  { return u.Status == StatusBanned }
func (u User) CanSignIn() bool { return u.Status == StatusActive }

// ValidStatus reports whether s is one of the known account statuses.
func ValidStatus(s string) bool {
	return s == StatusActive || s == StatusInactive || s == StatusBanned
}

// NewUser validates a newly verified registration before it is persisted.
// Constructing a User any other way is a bug: this is the only place that
// guarantees an invalid user cannot reach the database.
func NewUser(username, name, phone, email string, birthdate *time.Time) (User, error) {
	username, err := NormaliseUsername(username)
	if err != nil {
		return User{}, err
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return User{}, apperr.Invalid("A name is required")
	}

	phone, err = NormalisePhone(phone)
	if err != nil {
		return User{}, err
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil {
			return User{}, apperr.Invalid("Enter a valid email address")
		}
	}

	return User{
		Username:  username,
		Name:      name,
		Phone:     phone,
		Email:     email,
		Birthdate: birthdate,
		Status:    StatusActive,
	}, nil
}

// Rename changes the display name, applying the same rule NewUser does.
func (u *User) Rename(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return apperr.Invalid("A name is required")
	}
	u.Name = name
	return nil
}

// NormaliseUsername lowercases and validates a chosen username: 3-20
// characters, lowercase letters, digits, dot and underscore only. Lowercased
// rather than case-sensitive so "Alice" and "alice" cannot both be claimed —
// the two are the same handle to anyone looking them up.
func NormaliseUsername(raw string) (string, error) {
	username := strings.ToLower(strings.TrimSpace(raw))
	if !usernamePattern.MatchString(username) {
		return "", apperr.Invalid("Username must be 3-20 characters: lowercase letters, numbers, dot or underscore")
	}
	return username, nil
}

// NormalisePhone cleans a phone number into E.164 digits with no leading
// plus, so "0812-3456-7890", "081234567890" and "6281234567890" are all one
// subscriber and can be looked up by exact match.
//
// It is loose about what a country code looks like on purpose: a number that
// already starts with "62" is left alone rather than double-prefixed, and a
// number starting with "0" has that trunk prefix replaced with the default
// country code. This does not generalise to other countries — the day it
// needs to, this is the one function that has to change.
func NormalisePhone(raw string) (string, error) {
	digits, err := digitsOnly(raw)
	if err != nil {
		return "", apperr.Invalid("Enter a valid phone number")
	}
	digits = strings.TrimPrefix(digits, "+")

	switch {
	case strings.HasPrefix(digits, "0"):
		digits = defaultCountryCode + strings.TrimPrefix(digits, "0")
	case strings.HasPrefix(digits, defaultCountryCode):
		// already has the country code
	default:
		digits = defaultCountryCode + digits
	}

	// E.164 caps a full number at 15 digits; below 8 there is no room for a
	// real subscriber number after the two-digit country code.
	if len(digits) < 8 || len(digits) > 15 {
		return "", apperr.Invalid("Enter a valid phone number")
	}
	return digits, nil
}

func digitsOnly(raw string) (string, error) {
	var out strings.Builder
	for i, r := range strings.TrimSpace(raw) {
		switch {
		case r == '+' && i == 0:
			out.WriteRune(r)
		case unicode.IsDigit(r):
			out.WriteRune(r)
		case r == ' ', r == '-', r == '(', r == ')', r == '.':
			// Formatting a human added. Ignored rather than rejected.
		default:
			return "", apperr.Invalid("Enter a valid phone number")
		}
	}
	return out.String(), nil
}
