package domain

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Voucher is money off an order, held in a customer's wallet. A voucher with a code is claimed by typing the code in the app, or put in
// a wallet by the back office; one without a code is public and sits in every wallet while it is live. Whether it is live is never
// stored: see IsLive. Money is whole rupiah.
type Voucher struct {
	ID int64
	// Code is upper-case letters and digits, or empty for a public voucher.
	Code          string
	Title         string
	Description   string
	InternalNotes string // head office's private note; never sent to the customer app
	// DiscountType is percentage (Value is 1..100) or amount (Value is rupiah).
	DiscountType string
	Value        int64
	// MaxDiscount caps what a percentage takes off, in rupiah; 0 is no cap. MinSpend is what an order must come to; 0 is none.
	MaxDiscount, MinSpend int64
	// MaxClaims is how many customers may hold the voucher (a coded voucher only); 0 is no limit. ClaimsCount is how many do (derived).
	MaxClaims   int32
	ClaimsCount int64
	// UsageLimit is how many times one customer may spend it.
	UsageLimit int32
	Status     string // active | inactive: the human decision to publish
	Color      string // "#RRGGBB" of the coupon in the app, one of VoucherColors
	ImageURL   string
	// StartsAt and EndsAt are the window, to the second; nil at either end is open-ended there.
	StartsAt, EndsAt *time.Time
	// OutletIDs and ServiceIDs limit where the voucher can be spent; empty is everywhere. Outlets and Services name them (filled on reads).
	OutletIDs            []int64
	Outlets              []OutletRef
	ServiceIDs           []int64
	Services             []ServiceRef
	CreatedAt, UpdatedAt time.Time
}

const (
	VoucherActive   = "active"
	VoucherInactive = "inactive"

	DiscountPercentage = "percentage"
	DiscountAmount     = "amount"

	// A voucher's type, derived from whether it has a code.
	VoucherPublic = "public"
	VoucherCoded  = "code"

	// Where a grant came from.
	GrantClaim  = "claim"
	GrantInject = "inject"
	// A wallet entry that is not a grant: a public voucher.
	WalletPublic = "public"

	maxVoucherTitle  = 120
	maxVoucherText   = 500
	maxVoucherSites  = 200
	maxVoucherValue  = 1_000_000_000
	maxVoucherClaims = 1_000_000
	maxVoucherUsage  = 1000

	// MaxInject is how many customers one injection may name.
	MaxInject = 500
)

// VoucherColors are the colours a coupon may have in the app, "#RRGGBB"; the first is the default.
var VoucherColors = []string{"#0009FF", "#FF9500", "#16A34A", "#DC2626", "#7C3AED", "#0891B2", "#DB2777", "#111827"}

var voucherCode = regexp.MustCompile(`^[A-Z0-9]{4,20}$`)

// Type is public for a voucher with no code, and code otherwise.
func (v Voucher) Type() string {
	if v.Code == "" {
		return VoucherPublic
	}
	return VoucherCoded
}

// IsLive says whether the voucher can be used at now: published, and now inside the window. A window is half open: it opens at
// StartsAt and closes the instant EndsAt is reached.
func (v Voucher) IsLive(now time.Time) bool {
	if v.Status != VoucherActive {
		return false
	}
	return v.InWindow(now)
}

// InWindow says whether now is inside the window, whatever the status.
func (v Voucher) InWindow(now time.Time) bool {
	if v.StartsAt != nil && now.Before(*v.StartsAt) {
		return false
	}
	return v.EndsAt == nil || now.Before(*v.EndsAt)
}

// Ended says whether the window has closed.
func (v Voucher) Ended(now time.Time) bool { return v.EndsAt != nil && !now.Before(*v.EndsAt) }

// AppliesTo says whether the voucher can be spent at this outlet: it has no outlet limit, or the outlet is one of them. outletID 0
// (no outlet known) matches only a voucher with no limit.
func (v Voucher) AppliesTo(outletID int64) bool {
	if len(v.OutletIDs) == 0 {
		return true
	}
	for _, id := range v.OutletIDs {
		if id == outletID {
			return true
		}
	}
	return false
}

// StartUnix and EndUnix are the window as the index holds it: an absent start is 0 and an absent end is far away, so the tests are two
// plain comparisons.
func (v Voucher) StartUnix() int64 {
	if v.StartsAt == nil {
		return 0
	}
	return v.StartsAt.Unix()
}

func (v Voucher) EndUnix() int64 {
	if v.EndsAt == nil {
		return openEnd
	}
	return v.EndsAt.Unix()
}

// VoucherInput is a voucher as a form posts it.
type VoucherInput struct {
	Code, Title, Description, InternalNotes, DiscountType, Status, Color, ImageURL string
	Value, MaxDiscount, MinSpend                                                   int64
	MaxClaims, UsageLimit                                                          int32
	StartsAt, EndsAt                                                               *time.Time
	OutletIDs, ServiceIDs                                                          []int64
}

// ParseVoucher checks and trims a voucher. What needs the database (the outlets and services exist, the code is free) is checked by the
// store.
func ParseVoucher(in VoucherInput) (Voucher, error) {
	v := Voucher{Code: strings.ToUpper(strings.TrimSpace(in.Code)), Title: strings.TrimSpace(in.Title), Description: strings.TrimSpace(in.Description),
		InternalNotes: strings.TrimSpace(in.InternalNotes), DiscountType: in.DiscountType, Value: in.Value, MaxDiscount: in.MaxDiscount, MinSpend: in.MinSpend,
		MaxClaims: in.MaxClaims, UsageLimit: in.UsageLimit, Status: in.Status, Color: strings.ToUpper(strings.TrimSpace(in.Color)), ImageURL: strings.TrimSpace(in.ImageURL)}
	if v.Status == "" {
		v.Status = VoucherInactive
	}
	if v.UsageLimit == 0 {
		v.UsageLimit = 1
	}
	if v.Color == "" {
		v.Color = VoucherColors[0]
	}
	switch {
	case v.Title == "":
		return v, apperr.Invalid("A voucher needs a title")
	case len([]rune(v.Title)) > maxVoucherTitle:
		return v, apperr.Invalid(fmt.Sprintf("The title can be at most %d characters", maxVoucherTitle))
	case len([]rune(v.Description)) > maxVoucherText:
		return v, apperr.Invalid(fmt.Sprintf("The description can be at most %d characters", maxVoucherText))
	case len([]rune(v.InternalNotes)) > maxVoucherText:
		return v, apperr.Invalid(fmt.Sprintf("The internal notes can be at most %d characters", maxVoucherText))
	case v.Code != "" && !voucherCode.MatchString(v.Code):
		return v, apperr.Invalid("The code must be 4 to 20 capital letters or digits, such as WELCOME10")
	case v.Status != VoucherActive && v.Status != VoucherInactive:
		return v, apperr.Invalid("The status must be active or inactive")
	case !oneOf(VoucherColors, v.Color):
		return v, apperr.Invalid("The colour must be one of " + strings.Join(VoucherColors, ", "))
	}
	if err := v.checkMoney(); err != nil {
		return v, err
	}
	switch {
	case v.UsageLimit < 1 || v.UsageLimit > maxVoucherUsage:
		return v, apperr.Invalid(fmt.Sprintf("A customer can spend a voucher 1 to %d times", maxVoucherUsage))
	case v.MaxClaims < 0 || v.MaxClaims > maxVoucherClaims:
		return v, apperr.Invalid(fmt.Sprintf("The claim limit must be 0 (no limit) to %d", maxVoucherClaims))
	case v.MaxClaims > 0 && v.Code == "":
		return v, apperr.Invalid("Only a voucher with a code can limit how many customers claim it")
	}
	if v.ImageURL != "" {
		u, err := url.Parse(v.ImageURL)
		if err != nil || !imageScheme(u) || u.Host == "" || len(v.ImageURL) > maxImageURL {
			return v, apperr.Invalid("The image must be an https address, such as the one an upload returns")
		}
	}
	var err error
	if v.StartsAt, err = wholeSecond(in.StartsAt); err != nil {
		return v, err
	}
	if v.EndsAt, err = wholeSecond(in.EndsAt); err != nil {
		return v, err
	}
	if v.StartsAt != nil && v.EndsAt != nil && !v.EndsAt.After(*v.StartsAt) {
		return v, apperr.Invalid("The schedule must end after it starts")
	}
	if v.OutletIDs, err = uniqueIDs(in.OutletIDs, maxVoucherSites, "outlet"); err != nil {
		return v, err
	}
	if v.ServiceIDs, err = uniqueIDs(in.ServiceIDs, maxVoucherSites, "service"); err != nil {
		return v, err
	}
	return v, nil
}

// checkMoney keeps the amounts to whole rupiah that mean something: a percentage is 1 to 100 and may be capped; an amount is rupiah
// and has no cap (it is the cap).
func (v Voucher) checkMoney() error {
	switch v.DiscountType {
	case DiscountPercentage:
		if v.Value < 1 || v.Value > 100 {
			return apperr.Invalid("A percentage must be between 1 and 100")
		}
	case DiscountAmount:
		if v.Value < 1 || v.Value > maxVoucherValue {
			return apperr.Invalid(fmt.Sprintf("The amount must be between Rp 1 and Rp %d", maxVoucherValue))
		}
		if v.MaxDiscount != 0 {
			return apperr.Invalid("Only a percentage voucher can have a maximum discount")
		}
	default:
		return apperr.Invalid("The discount type must be percentage or amount")
	}
	switch {
	case v.MaxDiscount < 0 || v.MaxDiscount > maxVoucherValue:
		return apperr.Invalid(fmt.Sprintf("The maximum discount must be between 0 (no cap) and Rp %d", maxVoucherValue))
	case v.MinSpend < 0 || v.MinSpend > maxVoucherValue:
		return apperr.Invalid(fmt.Sprintf("The minimum spend must be between 0 (none) and Rp %d", maxVoucherValue))
	}
	return nil
}

func uniqueIDs(in []int64, max int, what string) ([]int64, error) {
	if len(in) > max {
		return nil, apperr.Invalid(fmt.Sprintf("A voucher can be limited to at most %d %ss", max, what))
	}
	var out []int64
	seen := map[int64]bool{}
	for _, id := range in {
		if id <= 0 {
			return nil, apperr.Invalid(fmt.Sprintf("A %s id is invalid", what))
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// VoucherPeriods are where a voucher's window stands against the clock, whatever its status: running (now is inside it), upcoming (not
// yet open), ended (closed) and unlimited (no start and no end).
var VoucherPeriods = []string{"running", "upcoming", "ended", "unlimited"}

// VoucherQuery filters and orders a page of vouchers.
type VoucherQuery struct {
	// Q matches, ignoring case, part of the title, the code, the description or the internal note.
	Q string
	// Status is "", active or inactive. Live is "", "true" (usable at Now) or "false" (not). Period is "" or one of VoucherPeriods.
	Status, Live, Period string
	// DiscountType is "", percentage or amount.
	DiscountType string
	// OutletID keeps the vouchers that can be spent at this outlet (no limit, or limited to it); 0 is no filter.
	OutletID        int64
	SortBy, SortDir string
	Limit, Offset   int
	// Now is the clock Live and Period are judged by; the query handler sets it.
	Now time.Time
}

// VoucherSortFields are the columns a voucher list may be ordered by.
var VoucherSortFields = map[string]bool{"id": true, "title": true, "code": true, "status": true, "value": true, "claims": true, "starts_at": true, "ends_at": true, "created_at": true, "updated_at": true}

func (q *VoucherQuery) Validate() error {
	if q.SortBy == "" {
		q.SortBy, q.SortDir = "created_at", "desc"
	}
	if err := validateList(&q.Q, &q.SortBy, &q.SortDir, &q.Limit, &q.Offset, VoucherSortFields, "id, title, code, status, value, claims, starts_at, ends_at, created_at, updated_at"); err != nil {
		return err
	}
	if q.Status != "" && q.Status != VoucherActive && q.Status != VoucherInactive {
		return apperr.Invalid("status must be active or inactive")
	}
	if q.Period != "" && !oneOf(VoucherPeriods, q.Period) {
		return apperr.Invalid("period must be " + strings.Join(VoucherPeriods, ", "))
	}
	if q.DiscountType != "" && q.DiscountType != DiscountPercentage && q.DiscountType != DiscountAmount {
		return apperr.Invalid("discount_type must be percentage or amount")
	}
	if q.OutletID < 0 {
		return apperr.Invalid("outlet_id must be a positive number")
	}
	return validateTriState(q.Live, "live")
}

// VoucherGrant is a customer's copy of a voucher.
type VoucherGrant struct {
	ID, VoucherID, CustomerID   int64
	CustomerName, CustomerPhone string
	Source                      string // claim | inject
	GrantedBy                   string // who injected it; empty for a claim
	GrantedAt                   time.Time
	ExpiresAt                   *time.Time // nil: until the voucher ends
	UsesRemaining               int32
}

// GrantQuery pages the holders of one voucher.
type GrantQuery struct {
	VoucherID     int64
	Q             string // part of the customer's name or phone
	Limit, Offset int
}

func (q *GrantQuery) Validate() error {
	q.Q = strings.TrimSpace(q.Q)
	if len([]rune(q.Q)) > 64 {
		return apperr.Invalid("The search can be at most 64 characters")
	}
	if q.Limit <= 0 {
		q.Limit = 10
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return nil
}

// WalletVoucher is a voucher as one customer sees it in the app.
type WalletVoucher struct {
	Voucher
	// Source is public, claim or inject.
	Source        string
	GrantedAt     *time.Time
	GrantExpires  *time.Time
	UsesRemaining int32
}

// ExpiresAt is when the customer can no longer use it: the earlier of the voucher's end and the grant's own expiry; nil if neither.
func (w WalletVoucher) ExpiresAt() *time.Time {
	switch {
	case w.EndsAt == nil:
		return w.GrantExpires
	case w.GrantExpires == nil || w.EndsAt.Before(*w.GrantExpires):
		return w.EndsAt
	}
	return w.GrantExpires
}

// InjectCustomer is a customer an injection names, as the user service resolved them.
type InjectCustomer struct {
	ID          int64
	Name, Phone string
	// Active is false for a customer who is switched off or banned: they get nothing.
	Active bool
}

// Injection is the audit of one bulk send.
type Injection struct {
	ID                                         int64
	VoucherID                                  int64
	VoucherTitle, VoucherCode                  string
	ActorID, ActorName                         string
	Target, Injected                           int32
	AlreadyHeld, OverLimit, NotFound, Inactive int32
	CreatedAt                                  time.Time
}

// InjectInput is what one injection asks for.
type InjectInput struct {
	VoucherID int64
	Customers []InjectCustomer
	// NotFound is how many of the customers the back office named could not be found; they only count.
	NotFound  int32
	ExpiresAt *time.Time
}

// Validate checks the request itself; whether the voucher can take it is CheckInjectable.
func (in *InjectInput) Validate(now time.Time) error {
	if in.NotFound < 0 || len(in.Customers)+int(in.NotFound) > MaxInject {
		return apperr.Invalid(fmt.Sprintf("One injection can name at most %d customers", MaxInject))
	}
	if len(in.Customers)+int(in.NotFound) == 0 {
		return apperr.Invalid("Name at least one customer")
	}
	if in.ExpiresAt != nil {
		t, err := wholeSecond(in.ExpiresAt)
		if err != nil {
			return err
		}
		if !t.After(now) {
			return apperr.Invalid("The grants' expiry must be in the future")
		}
		in.ExpiresAt = t
	}
	return nil
}

// CheckInjectable refuses a voucher that cannot be put in a wallet: a public one is in every wallet already, and one that is switched
// off or has ended would hand out something nobody can use.
func (v Voucher) CheckInjectable(now time.Time) error {
	switch {
	case v.Code == "":
		return apperr.Conflict("A public voucher is in every wallet already; only a voucher with a code can be sent to customers")
	case v.Status != VoucherActive:
		return apperr.Conflict("Switch the voucher on before sending it to customers")
	case v.Ended(now):
		return apperr.Conflict("This voucher has ended and cannot be sent to customers")
	}
	return nil
}

// PlanInjection decides who gets a grant. claimed is how many customers hold the voucher already and held is who they are; a customer
// named twice counts once. A customer who is switched off or banned is skipped, one who holds it is skipped, and once the voucher's
// claim limit is reached the rest are skipped; those who get one are in the order given.
func PlanInjection(v Voucher, claimed int64, held map[int64]bool, customers []InjectCustomer, notFound int32) (grant []InjectCustomer, out Injection) {
	out.NotFound = notFound
	seen := map[int64]bool{}
	for _, c := range customers {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out.Target++
		switch {
		case !c.Active:
			out.Inactive++
		case held[c.ID]:
			out.AlreadyHeld++
		case v.MaxClaims > 0 && claimed+int64(len(grant)) >= int64(v.MaxClaims):
			out.OverLimit++
		default:
			grant = append(grant, c)
		}
	}
	out.Target += notFound
	out.Injected = int32(len(grant))
	return grant, out
}

// InjectionQuery pages the audit of injections, newest first.
type InjectionQuery struct {
	VoucherID     int64 // 0: every voucher
	Limit, Offset int
}

func (q *InjectionQuery) Validate() error {
	if q.VoucherID < 0 {
		return apperr.Invalid("voucher_id must be a positive number")
	}
	if q.Limit <= 0 {
		q.Limit = 10
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return nil
}

// ClaimInput is a customer typing a code.
type ClaimInput struct {
	Code                        string
	CustomerID                  int64
	CustomerName, CustomerPhone string
}

// NormaliseCode trims and upper-cases a code as typed, and says whether it could be a code at all.
func NormaliseCode(raw string) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if !voucherCode.MatchString(code) {
		return "", apperr.Invalid("Enter a voucher code: 4 to 20 letters or digits")
	}
	return code, nil
}
