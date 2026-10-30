package domain

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVoucherIsLiveAndWindow(t *testing.T) {
	noon := *bannerTime("2026-10-28T12:00:00Z")
	for _, c := range []struct {
		name         string
		v            Voucher
		live, inside bool
	}{
		{"open on both ends", Voucher{Status: VoucherActive}, true, true},
		{"inactive inside its window", Voucher{Status: VoucherInactive}, false, true},
		{"before the start", Voucher{Status: VoucherActive, StartsAt: bannerTime("2026-10-28T12:00:01Z")}, false, false},
		{"exactly at the start", Voucher{Status: VoucherActive, StartsAt: bannerTime("2026-10-28T12:00:00Z")}, true, true},
		{"exactly at the end is over", Voucher{Status: VoucherActive, EndsAt: bannerTime("2026-10-28T12:00:00Z")}, false, false},
		{"a second before the end", Voucher{Status: VoucherActive, EndsAt: bannerTime("2026-10-28T12:00:01Z")}, true, true},
	} {
		if got := c.v.IsLive(noon); got != c.live {
			t.Errorf("%s: IsLive = %v, want %v", c.name, got, c.live)
		}
		if got := c.v.InWindow(noon); got != c.inside {
			t.Errorf("%s: InWindow = %v, want %v", c.name, got, c.inside)
		}
	}
	if (Voucher{EndsAt: bannerTime("2026-10-28T12:00:00Z")}).Ended(noon) != true || (Voucher{}).Ended(noon) {
		t.Error("Ended: the end instant closes the window, no end never does")
	}
}

func TestVoucherTypeAndOutlets(t *testing.T) {
	if (Voucher{}).Type() != VoucherPublic || (Voucher{Code: "WELCOME"}).Type() != VoucherCoded {
		t.Error("a voucher without a code is public")
	}
	v := Voucher{OutletIDs: []int64{3, 7}}
	if !v.AppliesTo(7) || v.AppliesTo(8) || v.AppliesTo(0) || !(Voucher{}).AppliesTo(8) || !(Voucher{}).AppliesTo(0) {
		t.Error("outlet matching")
	}
}

func voucherIn(mod func(*VoucherInput)) VoucherInput {
	in := VoucherInput{Title: "Diskon", DiscountType: DiscountPercentage, Value: 10}
	if mod != nil {
		mod(&in)
	}
	return in
}

func TestParseVoucher(t *testing.T) {
	v, err := ParseVoucher(voucherIn(func(i *VoucherInput) {
		i.Code, i.Title, i.OutletIDs, i.ServiceIDs = " welcome10 ", "  Selamat datang  ", []int64{2, 2, 5}, []int64{9, 9}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if v.Code != "WELCOME10" || v.Title != "Selamat datang" || v.Status != VoucherInactive || v.UsageLimit != 1 || v.Color != VoucherColors[0] {
		t.Errorf("defaults and cleaning: %+v", v)
	}
	if !reflect.DeepEqual(v.OutletIDs, []int64{2, 5}) || !reflect.DeepEqual(v.ServiceIDs, []int64{9}) {
		t.Errorf("limits = %v %v", v.OutletIDs, v.ServiceIDs)
	}
	at := bannerTime("2026-10-28T12:00:00.9Z")
	v, _ = ParseVoucher(voucherIn(func(i *VoucherInput) { i.StartsAt = at }))
	if v.StartsAt == nil || v.StartsAt.Nanosecond() != 0 {
		t.Errorf("the window is kept in whole seconds: %v", v.StartsAt)
	}

	tooLong := strings.Repeat("a", 121)
	for name, c := range map[string]struct {
		mod  func(*VoucherInput)
		want string
	}{
		"no title":                  {func(i *VoucherInput) { i.Title = " " }, "needs a title"},
		"a long title":              {func(i *VoucherInput) { i.Title = tooLong }, "at most 120"},
		"a long note":               {func(i *VoucherInput) { i.InternalNotes = strings.Repeat("a", 501) }, "internal notes"},
		"a short code":              {func(i *VoucherInput) { i.Code = "AB" }, "4 to 20"},
		"a code with a dash":        {func(i *VoucherInput) { i.Code = "WEL-COME" }, "4 to 20"},
		"a long code":               {func(i *VoucherInput) { i.Code = strings.Repeat("A", 21) }, "4 to 20"},
		"a status":                  {func(i *VoucherInput) { i.Status = "live" }, "active or inactive"},
		"a colour outside the set":  {func(i *VoucherInput) { i.Color = "#123456" }, "colour"},
		"no discount type":          {func(i *VoucherInput) { i.DiscountType = "" }, "percentage or amount"},
		"zero percent":              {func(i *VoucherInput) { i.Value = 0 }, "between 1 and 100"},
		"a hundred and one percent": {func(i *VoucherInput) { i.Value = 101 }, "between 1 and 100"},
		"a negative amount":         {func(i *VoucherInput) { i.DiscountType, i.Value = DiscountAmount, -5 }, "amount must be"},
		"an amount over the cap":    {func(i *VoucherInput) { i.DiscountType, i.Value = DiscountAmount, 1_000_000_001 }, "amount must be"},
		"a cap on an amount":        {func(i *VoucherInput) { i.DiscountType, i.Value, i.MaxDiscount = DiscountAmount, 5000, 100 }, "Only a percentage"},
		"a negative cap":            {func(i *VoucherInput) { i.MaxDiscount = -1 }, "maximum discount"},
		"a negative minimum":        {func(i *VoucherInput) { i.MinSpend = -1 }, "minimum spend"},
		"claims on a public one":    {func(i *VoucherInput) { i.MaxClaims = 5 }, "with a code"},
		"negative claims":           {func(i *VoucherInput) { i.Code, i.MaxClaims = "WELCOME", -1 }, "claim limit"},
		"too many uses":             {func(i *VoucherInput) { i.UsageLimit = 1001 }, "1 to 1000"},
		"a negative use":            {func(i *VoucherInput) { i.UsageLimit = -1 }, "1 to 1000"},
		"an http image":             {func(i *VoucherInput) { i.ImageURL = "http://example.com/a.png" }, "https"},
		"an inverted window": {func(i *VoucherInput) {
			i.StartsAt, i.EndsAt = bannerTime("2026-11-02T00:00:00Z"), bannerTime("2026-11-01T00:00:00Z")
		}, "end after"},
		"an empty window": {func(i *VoucherInput) {
			i.StartsAt, i.EndsAt = bannerTime("2026-11-02T00:00:00Z"), bannerTime("2026-11-02T00:00:00.5Z")
		}, "end after"},
		"a bad outlet id": {func(i *VoucherInput) { i.OutletIDs = []int64{0} }, "outlet id"},
		"a bad service":   {func(i *VoucherInput) { i.ServiceIDs = []int64{-1} }, "service id"},
	} {
		if _, err := ParseVoucher(voucherIn(c.mod)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, c.want)
		}
	}
	// The edges that are allowed.
	for name, mod := range map[string]func(*VoucherInput){
		"a hundred percent":   func(i *VoucherInput) { i.Value = 100 },
		"one rupiah":          func(i *VoucherInput) { i.DiscountType, i.Value = DiscountAmount, 1 },
		"the largest amount":  func(i *VoucherInput) { i.DiscountType, i.Value = DiscountAmount, 1_000_000_000 },
		"a capped percentage": func(i *VoucherInput) { i.MaxDiscount = 25000 },
		"a coded claim limit": func(i *VoucherInput) { i.Code, i.MaxClaims = "WELCOME", 100 },
		"a loopback image":    func(i *VoucherInput) { i.ImageURL = "http://localhost:8092/media/vouchers/a.png" },
	} {
		if _, err := ParseVoucher(voucherIn(mod)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestVoucherQueryValidate(t *testing.T) {
	q := VoucherQuery{}
	if err := q.Validate(); err != nil || q.SortBy != "created_at" || q.SortDir != "desc" || q.Limit != 10 {
		t.Errorf("defaults: %+v %v", q, err)
	}
	q = VoucherQuery{SortBy: "value"}
	if err := q.Validate(); err != nil || q.SortDir != "asc" {
		t.Errorf("a chosen sort goes up unless told otherwise: %+v %v", q, err)
	}
	for name, q := range map[string]VoucherQuery{
		"sort":     {SortBy: "color"},
		"status":   {Status: "live"},
		"period":   {Period: "soon"},
		"discount": {DiscountType: "free"},
		"live":     {Live: "maybe"},
		"outlet":   {OutletID: -1},
	} {
		if err := q.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	for _, p := range VoucherPeriods {
		q := VoucherQuery{Period: p}
		if err := q.Validate(); err != nil {
			t.Errorf("period %s: %v", p, err)
		}
	}
}

func TestWalletVoucherExpiry(t *testing.T) {
	end, grant := bannerTime("2026-12-01T00:00:00Z"), bannerTime("2026-11-01T00:00:00Z")
	for name, c := range map[string]struct {
		w    WalletVoucher
		want *time.Time
	}{
		"neither":                 {WalletVoucher{}, nil},
		"only the voucher's":      {WalletVoucher{Voucher: Voucher{EndsAt: end}}, end},
		"only the grant's":        {WalletVoucher{GrantExpires: grant}, grant},
		"the grant's is sooner":   {WalletVoucher{Voucher: Voucher{EndsAt: end}, GrantExpires: grant}, grant},
		"the voucher's is sooner": {WalletVoucher{Voucher: Voucher{EndsAt: grant}, GrantExpires: end}, grant},
	} {
		got := c.w.ExpiresAt()
		if (got == nil) != (c.want == nil) || (got != nil && !got.Equal(*c.want)) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestCheckInjectable(t *testing.T) {
	now := *bannerTime("2026-10-28T12:00:00Z")
	for name, c := range map[string]struct {
		v    Voucher
		want string
	}{
		"public":   {Voucher{Status: VoucherActive}, "public voucher"},
		"off":      {Voucher{Code: "WELCOME", Status: VoucherInactive}, "Switch the voucher on"},
		"ended":    {Voucher{Code: "WELCOME", Status: VoucherActive, EndsAt: bannerTime("2026-10-28T12:00:00Z")}, "ended"},
		"upcoming": {Voucher{Code: "WELCOME", Status: VoucherActive, StartsAt: bannerTime("2026-11-01T00:00:00Z")}, ""},
		"running":  {Voucher{Code: "WELCOME", Status: VoucherActive}, ""},
	} {
		err := c.v.CheckInjectable(now)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestInjectInputValidate(t *testing.T) {
	now := *bannerTime("2026-10-28T12:00:00Z")
	many := make([]InjectCustomer, MaxInject)
	for name, c := range map[string]struct {
		in   InjectInput
		want string
	}{
		"nobody":              {InjectInput{}, "at least one"},
		"the most":            {InjectInput{Customers: many}, ""},
		"one too many":        {InjectInput{Customers: many, NotFound: 1}, "at most 500"},
		"a negative count":    {InjectInput{Customers: many[:1], NotFound: -1}, "at most"},
		"only not found":      {InjectInput{NotFound: 3}, ""},
		"an expiry in past":   {InjectInput{Customers: many[:1], ExpiresAt: bannerTime("2026-10-28T12:00:00Z")}, "future"},
		"an expiry in future": {InjectInput{Customers: many[:1], ExpiresAt: bannerTime("2026-10-28T12:00:01Z")}, ""},
	} {
		err := c.in.Validate(now)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	in := InjectInput{Customers: many[:1], ExpiresAt: bannerTime("2026-11-01T00:00:00.7Z")}
	if err := in.Validate(now); err != nil || in.ExpiresAt.Nanosecond() != 0 {
		t.Errorf("the expiry is kept in whole seconds: %v %v", in.ExpiresAt, err)
	}
}

func customers(ids ...int64) []InjectCustomer {
	out := make([]InjectCustomer, len(ids))
	for i, id := range ids {
		out[i] = InjectCustomer{ID: id, Active: true}
	}
	return out
}

func customerIDs(cs []InjectCustomer) []int64 {
	out := []int64{}
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func TestPlanInjection(t *testing.T) {
	coded := Voucher{Code: "WELCOME", Status: VoucherActive}
	limited := Voucher{Code: "WELCOME", Status: VoucherActive, MaxClaims: 3}
	off := customers(7)
	off[0].Active = false

	for name, c := range map[string]struct {
		v        Voucher
		claimed  int64
		held     map[int64]bool
		in       []InjectCustomer
		notFound int32
		granted  []int64
		want     Injection
	}{
		"everyone gets one":                  {coded, 0, nil, customers(1, 2, 3), 0, []int64{1, 2, 3}, Injection{Target: 3, Injected: 3}},
		"one already has it":                 {coded, 1, map[int64]bool{2: true}, customers(1, 2, 3), 0, []int64{1, 3}, Injection{Target: 3, Injected: 2, AlreadyHeld: 1}},
		"named twice, counted once":          {coded, 0, nil, customers(1, 1, 2, 1), 0, []int64{1, 2}, Injection{Target: 2, Injected: 2}},
		"a switched-off customer":            {coded, 0, nil, append(off, customers(8)...), 0, []int64{8}, Injection{Target: 2, Injected: 1, Inactive: 1}},
		"some were not found":                {coded, 0, nil, customers(1), 4, []int64{1}, Injection{Target: 5, Injected: 1, NotFound: 4}},
		"the limit has room for two":         {limited, 1, nil, customers(1, 2, 3, 4), 0, []int64{1, 2}, Injection{Target: 4, Injected: 2, OverLimit: 2}},
		"the limit is full":                  {limited, 3, nil, customers(1, 2), 0, []int64{}, Injection{Target: 2, OverLimit: 2}},
		"a holder does not use up the limit": {limited, 3, map[int64]bool{1: true}, customers(1, 2), 0, []int64{}, Injection{Target: 2, AlreadyHeld: 1, OverLimit: 1}},
		"nobody asked":                       {coded, 0, nil, nil, 0, []int64{}, Injection{}},
	} {
		got, plan := PlanInjection(c.v, c.claimed, c.held, c.in, c.notFound)
		if !reflect.DeepEqual(customerIDs(got), c.granted) || plan != c.want {
			t.Errorf("%s: granted %v plan %+v, want %v %+v", name, customerIDs(got), plan, c.granted, c.want)
		}
		if sum := plan.Injected + plan.AlreadyHeld + plan.OverLimit + plan.NotFound + plan.Inactive; sum != plan.Target {
			t.Errorf("%s: the counts add up to %d, not the target %d", name, sum, plan.Target)
		}
	}
}

func TestNormaliseCode(t *testing.T) {
	if c, err := NormaliseCode("  welcome10 "); err != nil || c != "WELCOME10" {
		t.Errorf("= %q, %v", c, err)
	}
	for _, bad := range []string{"", "abc", "has space1", "dash-code", strings.Repeat("A", 21), "kode’"} {
		if _, err := NormaliseCode(bad); err == nil {
			t.Errorf("%q should not be a code", bad)
		}
	}
}

func TestGrantAndInjectionQueries(t *testing.T) {
	g := GrantQuery{Q: "  ani "}
	if err := g.Validate(); err != nil || g.Q != "ani" || g.Limit != 10 {
		t.Errorf("%+v %v", g, err)
	}
	long := GrantQuery{Q: strings.Repeat("a", 65)}
	if err := long.Validate(); err == nil {
		t.Error("a long search")
	}
	i := InjectionQuery{Limit: 500, Offset: -3}
	if err := i.Validate(); err != nil || i.Limit != 100 || i.Offset != 0 {
		t.Errorf("%+v %v", i, err)
	}
	if bad := (InjectionQuery{VoucherID: -1}); bad.Validate() == nil {
		t.Error("a negative voucher id")
	}
}
