package domain

import (
	"strings"
	"testing"
	"time"
)

func bannerTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func TestBannerIsLive(t *testing.T) {
	noon := *bannerTime("2026-10-28T12:00:00Z")
	for _, c := range []struct {
		name   string
		b      Banner
		now    time.Time
		isLive bool
	}{
		{"open on both ends", Banner{Status: BannerActive}, noon, true},
		{"inactive is never live", Banner{Status: BannerInactive}, noon, false},
		{"before the start", Banner{Status: BannerActive, StartsAt: bannerTime("2026-10-28T12:00:01Z")}, noon, false},
		{"exactly at the start", Banner{Status: BannerActive, StartsAt: bannerTime("2026-10-28T12:00:00Z")}, noon, true},
		{"exactly at the end is over", Banner{Status: BannerActive, EndsAt: bannerTime("2026-10-28T12:00:00Z")}, noon, false},
		{"a second before the end", Banner{Status: BannerActive, EndsAt: bannerTime("2026-10-28T12:00:01Z")}, noon, true},
		{"open start, ended", Banner{Status: BannerActive, EndsAt: bannerTime("2026-10-27T00:00:00Z")}, noon, false},
		{"open end, started", Banner{Status: BannerActive, StartsAt: bannerTime("2026-10-01T00:00:00Z")}, noon, true},
		{"inside a window", Banner{Status: BannerActive, StartsAt: bannerTime("2026-10-28T00:00:00Z"), EndsAt: bannerTime("2026-10-29T00:00:00Z")}, noon, true},
		{"the window as another zone writes it", Banner{Status: BannerActive, StartsAt: bannerTime("2026-10-28T19:00:00+07:00")}, noon, true},
	} {
		if got := c.b.IsLive(c.now); got != c.isLive {
			t.Errorf("%s: IsLive = %v, want %v", c.name, got, c.isLive)
		}
	}
}

func TestBannerAppliesTo(t *testing.T) {
	all := Banner{}
	some := Banner{OutletIDs: []int64{3, 5}}
	for _, c := range []struct {
		name   string
		b      Banner
		outlet int64
		want   bool
	}{
		{"no limit, any outlet", all, 9, true},
		{"no limit, no outlet known", all, 0, true},
		{"limited, a listed outlet", some, 5, true},
		{"limited, another outlet", some, 4, false},
		{"limited, no outlet known", some, 0, false},
	} {
		if got := c.b.AppliesTo(c.outlet); got != c.want {
			t.Errorf("%s: AppliesTo(%d) = %v, want %v", c.name, c.outlet, got, c.want)
		}
	}
}

func TestBannerDeeplink(t *testing.T) {
	for _, c := range []struct{ typ, val, want string }{
		{TargetNone, "", ""},
		{TargetService, "12", "wuzz://service/12"},
		{TargetOutlet, "3", "wuzz://outlet/3"},
		{TargetScreen, "history", "wuzz://screen/history"},
		{TargetScreen, "menu", "wuzz://screen/menu"},
		{TargetExternal, "https://wuzz.care/promo", "https://wuzz.care/promo"},
	} {
		if got := (Banner{TargetType: c.typ, TargetValue: c.val}).Deeplink(); got != c.want {
			t.Errorf("%s %q: Deeplink = %q, want %q", c.typ, c.val, got, c.want)
		}
	}
}

func TestBannerIndexWindow(t *testing.T) {
	open := Banner{}
	if open.StartUnix() != 0 || open.EndUnix() != openEnd {
		t.Errorf("an open window is %d..%d", open.StartUnix(), open.EndUnix())
	}
	w := Banner{StartsAt: bannerTime("2026-10-28T12:00:00Z"), EndsAt: bannerTime("2026-10-29T12:00:00Z")}
	if w.StartUnix() != 1793188800 || w.EndUnix() != 1793275200 {
		t.Errorf("window = %d..%d", w.StartUnix(), w.EndUnix())
	}
}

func TestParseBanner(t *testing.T) {
	const img = "https://assets.wuzz.care/banners/a.png"
	ok := func(mod func(*BannerInput)) BannerInput {
		in := BannerInput{Title: " Promo ", ImageURL: img, Status: BannerActive, TargetType: TargetNone}
		if mod != nil {
			mod(&in)
		}
		return in
	}
	for _, c := range []struct {
		name string
		in   BannerInput
		want string // a part of the refusal; "" = accepted
	}{
		{"plain", ok(nil), ""},
		{"no title", ok(func(i *BannerInput) { i.Title = "  " }), "needs a title"},
		{"long title", ok(func(i *BannerInput) { i.Title = strings.Repeat("a", 121) }), "at most 120"},
		{"title of 120 multibyte characters", ok(func(i *BannerInput) { i.Title = strings.Repeat("é", 120) }), ""},
		{"long note", ok(func(i *BannerInput) { i.InternalNotes = strings.Repeat("a", 501) }), "internal notes"},
		{"unknown status", ok(func(i *BannerInput) { i.Status = "live" }), "active or inactive"},
		{"active without a picture", ok(func(i *BannerInput) { i.ImageURL = "" }), "picture"},
		{"inactive without a picture", ok(func(i *BannerInput) { i.ImageURL, i.Status = "", BannerInactive }), ""},
		{"http picture", ok(func(i *BannerInput) { i.ImageURL = "http://assets.wuzz.care/a.png" }), "https"},
		{"loopback http picture", ok(func(i *BannerInput) { i.ImageURL = "http://localhost:8092/media/a.png" }), ""},
		{"not an address", ok(func(i *BannerInput) { i.ImageURL = "javascript:alert(1)" }), "https"},
		{"none with a value", ok(func(i *BannerInput) { i.TargetValue = "5" }), "nowhere"},
		{"service without an id", ok(func(i *BannerInput) { i.TargetType = TargetService }), "Choose the service"},
		{"service, not a number", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetService, "abc" }), "Choose the service"},
		{"service, zero", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetService, "0" }), "Choose the service"},
		{"id of 18 digits", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetService, "123456789012345678" }), ""},
		{"id of 19 digits", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetService, "1234567890123456789" }), "Choose the service"},
		{"id with a sign", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetService, "+5" }), ""},
		{"outlet", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetOutlet, "7" }), ""},
		{"screen", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetScreen, "history" }), ""},
		{"unknown screen", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetScreen, "admin" }), "screen must be one of"},
		{"external", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetExternal, "https://wuzz.care/promo" }), ""},
		{"external over http", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetExternal, "http://wuzz.care/promo" }), "https"},
		{"external without a host", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetExternal, "https://localhost" }), "https"},
		{"external, a custom scheme", ok(func(i *BannerInput) { i.TargetType, i.TargetValue = TargetExternal, "wuzz://screen/home" }), "https"},
		{"unknown target type", ok(func(i *BannerInput) { i.TargetType = "bundle" }), "target must be one of"},
		{"window ends before it starts", ok(func(i *BannerInput) {
			i.StartsAt, i.EndsAt = bannerTime("2026-10-29T00:00:00Z"), bannerTime("2026-10-28T00:00:00Z")
		}), "end after it starts"},
		{"window of zero length", ok(func(i *BannerInput) {
			i.StartsAt, i.EndsAt = bannerTime("2026-10-29T00:00:00Z"), bannerTime("2026-10-29T00:00:00Z")
		}), "end after it starts"},
		{"window", ok(func(i *BannerInput) {
			i.StartsAt, i.EndsAt = bannerTime("2026-10-28T00:00:00Z"), bannerTime("2026-10-29T00:00:00Z")
		}), ""},
		{"outlet id zero", ok(func(i *BannerInput) { i.OutletIDs = []int64{0} }), "outlet id"},
	} {
		_, err := ParseBanner(c.in)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.want != "" && err == nil:
			t.Errorf("%s: accepted, want a refusal containing %q", c.name, c.want)
		case c.want != "" && !strings.Contains(err.Error(), c.want):
			t.Errorf("%s: refused with %q, want it to contain %q", c.name, err, c.want)
		}
	}

	b, err := ParseBanner(BannerInput{Title: " Promo ", TargetType: TargetService, TargetValue: "007", OutletIDs: []int64{3, 3, 5},
		StartsAt: func() *time.Time { t := time.Date(2026, 10, 28, 12, 0, 0, 900, time.UTC); return &t }()})
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "Promo" || b.TargetValue != "7" || b.Status != BannerInactive {
		t.Errorf("not normalised: %+v", b)
	}
	if len(b.OutletIDs) != 2 {
		t.Errorf("outlets = %v, want the duplicate dropped", b.OutletIDs)
	}
	if b.StartsAt.Nanosecond() != 0 {
		t.Errorf("the window keeps a fraction of a second: %v", b.StartsAt)
	}
}

func TestBannerQueryValidate(t *testing.T) {
	q := BannerQuery{}
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	if q.SortBy != "sort_index" || q.SortDir != "asc" || q.Limit != 10 {
		t.Errorf("defaults = %+v", q)
	}
	for _, bad := range []BannerQuery{{Status: "live"}, {Live: "yes"}, {OutletID: -1}, {SortBy: "image_url"}, {SortDir: "up"}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestValidateReorder(t *testing.T) {
	all := []int64{1, 2, 3}
	if err := ValidateReorder([]int64{3, 1, 2}, all); err != nil {
		t.Errorf("a permutation was refused: %v", err)
	}
	for name, ids := range map[string][]int64{"missing one": {1, 2}, "extra": {1, 2, 3, 4}, "unknown id": {1, 2, 9}, "duplicate": {1, 1, 2}, "empty": nil} {
		if err := ValidateReorder(ids, all); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
