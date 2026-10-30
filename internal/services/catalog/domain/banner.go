package domain

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Banner is a picture of the customer app's carousel. Whether it is on a phone right now is never stored: see IsLive.
type Banner struct {
	ID            int64
	Title         string
	InternalNotes string // head office's private note; never sent to the customer app
	ImageURL      string
	// TargetType is what a tap opens: none, service, outlet, screen or external. TargetValue is that target's id, screen key or
	// address, and empty for none.
	TargetType, TargetValue string
	Status                  string // active | inactive: the human decision to publish
	SortIndex               int32  // the carousel's order, written only by a reorder
	// StartsAt and EndsAt are the display window, to the second; nil at either end is open-ended there.
	StartsAt, EndsAt *time.Time
	// OutletIDs limits the banner to these outlets; empty is every outlet. Outlets names them (filled on reads).
	OutletIDs            []int64
	Outlets              []OutletRef
	CreatedAt, UpdatedAt time.Time
}

// OutletRef is an outlet a banner is limited to.
type OutletRef struct {
	ID   int64
	Name string
}

const (
	BannerActive   = "active"
	BannerInactive = "inactive"

	TargetNone     = "none"
	TargetService  = "service"
	TargetOutlet   = "outlet"
	TargetScreen   = "screen"
	TargetExternal = "external"

	maxBannerTitle = 120
	maxBannerNotes = 500
	maxBannerSites = 200
)

// BannerTargets are the kinds of target a banner can have.
var BannerTargets = []string{TargetNone, TargetService, TargetOutlet, TargetScreen, TargetExternal}

// BannerScreens are the screens of the customer app a banner may open (wuzz://screen/<key>). The app maps each key to a route; this is
// the one list of them, and the back office reads it from here.
var BannerScreens = []string{"home", "menu", "history", "support", "profile", "scan", "outlets", "streak", "leaderboard", "help"}

// openEnd is the end of a banner with no end, as a Unix time (the last second of 9999), for the index.
const openEnd = 253402300799

// IsLive says whether the banner is on a customer's phone at now: published, and now inside the window. A window is half open:
// it opens at StartsAt and closes the instant EndsAt is reached.
func (b Banner) IsLive(now time.Time) bool {
	if b.Status != BannerActive {
		return false
	}
	if b.StartsAt != nil && now.Before(*b.StartsAt) {
		return false
	}
	if b.EndsAt != nil && !now.Before(*b.EndsAt) {
		return false
	}
	return true
}

// AppliesTo says whether the banner is for this outlet: it has no outlet limit, or the outlet is one of them. outletID 0 (no
// outlet known) matches only a banner with no limit.
func (b Banner) AppliesTo(outletID int64) bool {
	if len(b.OutletIDs) == 0 {
		return true
	}
	for _, id := range b.OutletIDs {
		if id == outletID {
			return true
		}
	}
	return false
}

// Deeplink is the address the app opens on a tap, or "" for a banner that goes nowhere.
func (b Banner) Deeplink() string {
	switch b.TargetType {
	case TargetService:
		return "wuzz://service/" + b.TargetValue
	case TargetOutlet:
		return "wuzz://outlet/" + b.TargetValue
	case TargetScreen:
		return "wuzz://screen/" + b.TargetValue
	case TargetExternal:
		return b.TargetValue
	}
	return ""
}

// StartUnix and EndUnix are the window as the index holds it: an absent start is 0 and an absent end is far away, so the live
// test is two plain comparisons.
func (b Banner) StartUnix() int64 {
	if b.StartsAt == nil {
		return 0
	}
	return b.StartsAt.Unix()
}

func (b Banner) EndUnix() int64 {
	if b.EndsAt == nil {
		return openEnd
	}
	return b.EndsAt.Unix()
}

// BannerInput is a banner as a form posts it.
type BannerInput struct {
	Title, InternalNotes, ImageURL, TargetType, TargetValue, Status string
	StartsAt, EndsAt                                                *time.Time
	OutletIDs                                                       []int64
}

// ParseBanner checks and trims a banner. What needs the database (the target and the outlets exist) is checked by the store.
func ParseBanner(in BannerInput) (Banner, error) {
	b := Banner{Title: strings.TrimSpace(in.Title), InternalNotes: strings.TrimSpace(in.InternalNotes), ImageURL: strings.TrimSpace(in.ImageURL),
		TargetType: in.TargetType, TargetValue: strings.TrimSpace(in.TargetValue), Status: in.Status}
	if b.Status == "" {
		b.Status = BannerInactive
	}
	if b.TargetType == "" {
		b.TargetType = TargetNone
	}
	switch {
	case b.Title == "":
		return b, apperr.Invalid("A banner needs a title")
	case len([]rune(b.Title)) > maxBannerTitle:
		return b, apperr.Invalid(fmt.Sprintf("The title can be at most %d characters", maxBannerTitle))
	case len([]rune(b.InternalNotes)) > maxBannerNotes:
		return b, apperr.Invalid(fmt.Sprintf("The internal notes can be at most %d characters", maxBannerNotes))
	case b.Status != BannerActive && b.Status != BannerInactive:
		return b, apperr.Invalid("The status must be active or inactive")
	}
	if b.ImageURL != "" {
		u, err := url.Parse(b.ImageURL)
		if err != nil || !imageScheme(u) || u.Host == "" || len(b.ImageURL) > maxImageURL {
			return b, apperr.Invalid("The image must be an https address, such as the one an upload returns")
		}
	}
	if b.Status == BannerActive && b.ImageURL == "" {
		return b, apperr.Invalid("A banner needs a picture before it can be switched on")
	}
	if err := b.checkTarget(); err != nil {
		return b, err
	}
	var err error
	if b.StartsAt, err = wholeSecond(in.StartsAt); err != nil {
		return b, err
	}
	if b.EndsAt, err = wholeSecond(in.EndsAt); err != nil {
		return b, err
	}
	if b.StartsAt != nil && b.EndsAt != nil && !b.EndsAt.After(*b.StartsAt) {
		return b, apperr.Invalid("The schedule must end after it starts")
	}
	if len(in.OutletIDs) > maxBannerSites {
		return b, apperr.Invalid(fmt.Sprintf("A banner can be limited to at most %d outlets", maxBannerSites))
	}
	seen := map[int64]bool{}
	for _, id := range in.OutletIDs {
		if id <= 0 {
			return b, apperr.Invalid("An outlet id is invalid")
		}
		if !seen[id] {
			seen[id] = true
			b.OutletIDs = append(b.OutletIDs, id)
		}
	}
	return b, nil
}

func (b *Banner) checkTarget() error {
	switch b.TargetType {
	case TargetNone:
		if b.TargetValue != "" {
			return apperr.Invalid("A banner that goes nowhere cannot have a target")
		}
	case TargetService, TargetOutlet:
		// 1 to 18 digits: the app reads the id out of the deeplink with that shape.
		id, err := strconv.ParseInt(b.TargetValue, 10, 64)
		if err != nil || id <= 0 || len(strconv.FormatInt(id, 10)) > 18 {
			return apperr.Invalid(fmt.Sprintf("Choose the %s the banner opens", b.TargetType))
		}
		b.TargetValue = strconv.FormatInt(id, 10) // 007 and 7 are one target
	case TargetScreen:
		if !oneOf(BannerScreens, b.TargetValue) {
			return apperr.Invalid("The screen must be one of " + strings.Join(BannerScreens, ", "))
		}
	case TargetExternal:
		u, err := url.Parse(b.TargetValue)
		if b.TargetValue == "" || err != nil || u.Scheme != "https" || !strings.Contains(u.Hostname(), ".") || len(b.TargetValue) > 500 {
			return apperr.Invalid("The link must be a full https address, such as https://wuzz.care/promo")
		}
	default:
		return apperr.Invalid("The target must be one of " + strings.Join(BannerTargets, ", "))
	}
	return nil
}

// wholeSecond drops the fraction of a second: the window is kept and indexed in whole seconds, so the two always agree on the edge.
func wholeSecond(t *time.Time) (*time.Time, error) {
	if t == nil {
		return nil, nil
	}
	if t.IsZero() {
		return nil, apperr.Invalid("A date is not valid")
	}
	s := t.UTC().Truncate(time.Second)
	return &s, nil
}

// BannerQuery filters and orders a page of banners.
type BannerQuery struct {
	// Q matches, ignoring case, part of the title or the internal note.
	Q string
	// Status is "", active or inactive. Live is "", "true" (on a phone at Now) or "false" (not).
	Status, Live string
	// OutletID keeps the banners that apply to this outlet (no limit, or limited to it); 0 is no filter.
	OutletID        int64
	SortBy, SortDir string
	Limit, Offset   int
	// Now is the clock Live is judged by; the query handler sets it.
	Now time.Time
}

// BannerSortFields are the columns a banner list may be ordered by.
var BannerSortFields = map[string]bool{"id": true, "title": true, "status": true, "sort_index": true, "starts_at": true, "ends_at": true, "created_at": true, "updated_at": true}

func (q *BannerQuery) Validate() error {
	if q.SortBy == "" {
		q.SortBy = "sort_index"
	}
	if err := validateList(&q.Q, &q.SortBy, &q.SortDir, &q.Limit, &q.Offset, BannerSortFields, "id, title, status, sort_index, starts_at, ends_at, created_at, updated_at"); err != nil {
		return err
	}
	if q.Status != "" && q.Status != BannerActive && q.Status != BannerInactive {
		return apperr.Invalid("status must be active or inactive")
	}
	if q.OutletID < 0 {
		return apperr.Invalid("outlet_id must be a positive number")
	}
	return validateTriState(q.Live, "live")
}

// ValidateReorder checks that ids lists every banner (all) exactly once, and no other.
func ValidateReorder(ids, all []int64) error {
	if len(ids) != len(all) {
		return apperr.Invalid("The order must list every banner exactly once")
	}
	want := make(map[int64]bool, len(all))
	for _, id := range all {
		want[id] = true
	}
	for _, id := range ids {
		if !want[id] {
			return apperr.Invalid(fmt.Sprintf("Banner %d is not in the list, or is listed twice", id))
		}
		delete(want, id)
	}
	return nil
}
