// Package domain holds catalog's business rules: outlets and the laundry
// services sold. No Postgres, gRPC or JSON in here.
package domain

import (
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Bounds is a box on the map, in degrees: what a map shows on screen.
type Bounds struct {
	South, West, North, East float64
}

// Validate refuses a box that is not on the globe or is inside out. A box across the date line is not
// supported (west must be less than east); nothing Wuzz serves is near it.
func (b Bounds) Validate() error {
	switch {
	case b.South < -90 || b.North > 90 || b.West < -180 || b.East > 180:
		return apperr.Invalid("The map area is outside the world")
	case b.South > b.North || b.West > b.East:
		return apperr.Invalid("The map area is the wrong way round: south must not exceed north, nor west exceed east")
	}
	return nil
}

type Outlet struct {
	ID      int64
	Name    string
	Address string
	Phone   string
	// Email is the outlet's contact address, lower-cased; empty when it has none.
	Email     string
	Latitude  float64
	Longitude float64
	Active    bool
	// Images are the addresses (https; http only for a loopback host in dev) of the outlet's pictures; the first is the primary one.
	Images []string
	// Category is what the outlet is for: laundry, caffe or all. DeliveryTypes is how an order reaches the customer: pickup,
	// delivery or both. OpenTime and CloseTime are wall-clock "HH:MM" in Asia/Jakarta; a CloseTime before the OpenTime means the
	// outlet closes after midnight (22:00-02:00). Left empty on a new outlet they take the table's defaults (see Defaults).
	Category      string
	DeliveryTypes []string
	// OpenTime and CloseTime are the single daily range an outlet is written with when its hours do not differ by day; once the
	// outlet is validated they are the first open day's hours. Hours is the week (Monday = 0 to Sunday = 6), and Timezone is the
	// zone every one of those clock times is in: Asia/Jakarta (WIB), Asia/Makassar (WITA) or Asia/Jayapura (WIT).
	OpenTime  string
	CloseTime string
	Timezone  string
	Hours     []DayHours
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate normalises and checks an outlet before it is stored.
func (o *Outlet) Validate() error {
	o.Name = strings.TrimSpace(o.Name)
	o.Address = strings.TrimSpace(o.Address)
	o.Phone = strings.TrimSpace(o.Phone)
	var err error
	if o.Email, err = NormalizeEmail(o.Email); err != nil {
		return err
	}
	for i := range o.Images {
		o.Images[i] = strings.TrimSpace(o.Images[i])
	}
	if o.Name == "" {
		return apperr.Invalid("An outlet name is required")
	}
	if o.Latitude < -90 || o.Latitude > 90 || o.Longitude < -180 || o.Longitude > 180 {
		return apperr.Invalid("Latitude or longitude is out of range")
	}
	if err := o.validateDiscovery(); err != nil {
		return err
	}
	if len(o.Images) > maxImages {
		return apperr.Invalid("An outlet can have at most 10 pictures")
	}
	for _, img := range o.Images {
		u, err := url.Parse(img)
		if err != nil || !imageScheme(u) || u.Host == "" || len(img) > maxImageURL {
			return apperr.Invalid("Every picture must be an https address of at most 500 characters")
		}
	}
	return nil
}

// phonePattern is what a contact number may be made of: digits, spaces, + - ( ), 6 to 20 characters in all.
var phonePattern = regexp.MustCompile(`^[0-9+()\-\s]{6,20}$`)

// RequireContact is what a NEW outlet must state before defaults are applied: a phone number, an email address and its category.
// (Validate accepts an outlet without them, so the seeded outlets that have no email still load and can be switched on or off.)
func (o Outlet) RequireContact() error {
	if err := RequirePhone(o.Phone); err != nil {
		return err
	}
	if err := RequireEmail(o.Email); err != nil {
		return err
	}
	if strings.TrimSpace(o.Category) == "" {
		return apperr.Invalid("The category is required")
	}
	return nil
}

// RequirePhone refuses a phone number that is empty or is not made of digits, spaces, + - ( ) in 6 to 20 characters.
func RequirePhone(phone string) error {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return apperr.Invalid("The phone number is required")
	}
	if !phonePattern.MatchString(phone) {
		return apperr.Invalid("The phone number is not valid: use digits, spaces, + - ( ) and 6 to 20 characters")
	}
	return nil
}

// RequireEmail refuses an email address that is empty or is not one plain address (the same check NormalizeEmail makes).
func RequireEmail(email string) error {
	if strings.TrimSpace(email) == "" {
		return apperr.Invalid("The email address is required")
	}
	_, err := NormalizeEmail(email)
	return err
}

// MaxEmailLength is the longest address the catalog stores (RFC 5321's limit for a path).
const MaxEmailLength = 254

// NormalizeEmail trims and lower-cases an optional contact email and refuses what is not one plain address: no display name
// ("Wuzz <a@b.co>"), no list, no spaces, a dot in the domain. Empty means none and is fine.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", nil
	}
	if len(email) > MaxEmailLength {
		return "", apperr.Invalid("The email address is too long: at most 254 characters")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || strings.ContainsAny(email, " \t\r\n,;<>") {
		return "", apperr.Invalid("The email address is not valid")
	}
	at := strings.LastIndex(email, "@")
	if at < 1 || !strings.Contains(email[at+1:], ".") || strings.HasPrefix(email[at+1:], ".") || strings.HasSuffix(email[at+1:], ".") {
		return "", apperr.Invalid("The email address is not valid")
	}
	return email, nil
}

// imageScheme is https, or http for a loopback host (localhost, 127.0.0.1, ::1): in dev the uploaded pictures are served from this
// machine over plain http. A loopback address is never reachable by a customer, so it cannot be used to point the app at somewhere else.
func imageScheme(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

const (
	maxImageURL = 500
	maxImages   = 10
)

// Tax types of a service: how tax is added to its price.
const (
	TaxNone = "none" // the price is all the customer pays
	TaxPPN  = "ppn"  // value-added tax is added on top of the price
)

func validTaxType(t string) bool { return t == TaxNone || t == TaxPPN }

// Units a service can be priced in.
const (
	UnitKg      = "kg"
	UnitPcs     = "pcs"
	UnitSession = "sesi" // a machine session: a washer or a dryer
	UnitUnit    = "unit" // a vehicle
)

// ServiceCategory is a group services are listed under.
type ServiceCategory struct {
	ID        int64
	Name      string
	Icon      string
	SortOrder int32
}

type ServiceItem struct {
	ID   int64
	Name string
	Unit string
	// Price is the price BEFORE tax, whole rupiah: what is stored and what the staff keys in. What the customer pays is PriceAfterTax.
	Price  int64
	Active bool
	// CategoryID is the category it is listed under; 0 = none.
	CategoryID  int64
	Icon        string
	Description string
	// TaxType is how tax is added to the price: TaxNone or TaxPPN. The rate is not stored; the checkout reads it from configuration.
	TaxType string
	// InternalNotes is a note for staff, never shown to customers.
	InternalNotes string
	// DiscountType is DiscountNone, DiscountValue or DiscountPercent, and DiscountValue its size: whole rupiah for a value,
	// basis points for a percentage (1250 = 12.5%). The discount comes off Price (the list price) before tax is added.
	DiscountType  string
	DiscountValue int64
	// DiscountAmount, PriceAfterDiscount, TaxRateBps and PriceAfterTax are derived from Price, the discount and TaxType
	// (ApplyPricing) on reads; writes ignore them. Tax is added to PriceAfterDiscount, not to Price.
	DiscountAmount     int64
	PriceAfterDiscount int64
	TaxRateBps         int32
	PriceAfterTax      int64
	// VariantGroups and ModifierGroups are the linked groups, in the order they are shown, with their options. Filled on
	// reads only.
	VariantGroups  []VariantGroup
	ModifierGroups []ModifierGroup
	// VariantGroupIDs and ModifierGroupIDs are what a create or update links, in order. nil = not given: an update keeps
	// the stored links, while an empty list clears them.
	VariantGroupIDs  []int64
	ModifierGroupIDs []int64
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (s *ServiceItem) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return apperr.Invalid("A service name is required")
	}
	switch s.Unit {
	case UnitKg, UnitPcs, UnitSession, UnitUnit:
	default:
		return apperr.Invalid("unit must be kg, pcs, sesi or unit")
	}
	if s.Price < 0 {
		return apperr.Invalid("price cannot be negative")
	}
	if !validTaxType(s.TaxType) {
		return apperr.Invalid("tax_type must be none or ppn")
	}
	if err := ValidateDiscount(s.DiscountType, s.DiscountValue, s.Price); err != nil {
		return err
	}
	s.InternalNotes = strings.TrimSpace(s.InternalNotes)
	if len([]rune(s.InternalNotes)) > maxNotes {
		return apperr.Invalid("internal notes are limited to 500 characters")
	}
	if err := validateLinks(s.VariantGroupIDs, "variant"); err != nil {
		return err
	}
	return validateLinks(s.ModifierGroupIDs, "modifier")
}
