package postgres

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/command"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// These need CATALOG_TEST_DSN (a catalog database with the migrations applied), like the other store tests.

var testActor = command.Actor{ID: "a:1", Name: "Tester"}

func voucherOf(t *testing.T, s *Store, title string, mod func(*domain.VoucherInput)) domain.Voucher {
	t.Helper()
	in := domain.VoucherInput{Title: unique(title), DiscountType: domain.DiscountAmount, Value: 10000, Status: domain.VoucherActive}
	if mod != nil {
		mod(&in)
	}
	parsed, err := domain.ParseVoucher(in)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.CreateVoucher(context.Background(), parsed)
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM voucher_grants WHERE voucher_id = $1`, v.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM voucher_injections WHERE voucher_id = $1`, v.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM vouchers WHERE id = $1`, v.ID)
	})
	return v
}

// A test code: unique per run, since a code is unique across vouchers.
func testCode(prefix string) string {
	return strings.ToUpper(prefix + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000_000, 36))
}

// A customer id that no real customer has, so the grants of these tests stand alone.
var nextCustomer atomic.Int64

func testCustomer() domain.InjectCustomer {
	id := 9_000_000_000 + time.Now().UnixNano()%1_000_000_000 + nextCustomer.Add(1)
	return domain.InjectCustomer{ID: id, Name: "Cust " + strconv.FormatInt(id, 10), Phone: "62" + strconv.FormatInt(id, 10), Active: true}
}

func wantKind(t *testing.T, what string, err error, kind apperr.Kind, mention string) {
	t.Helper()
	if kindOf(err) != kind || !strings.Contains(err.Error(), mention) {
		t.Errorf("%s: err = %v (%s), want %s mentioning %q", what, err, kindOf(err), kind, mention)
	}
}

// A voucher keeps its limits and its claim count, and its code is unique.
func TestVoucherCreateReadUpdate(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	o1 := testOutlet(t, s, "ZZ vou a")
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("ZZ vou svc"), Unit: domain.UnitKg, Price: 5000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID) })

	code := testCode("zzv")
	v := voucherOf(t, s, "ZZ kode", func(i *domain.VoucherInput) {
		i.Code, i.OutletIDs, i.ServiceIDs, i.MaxClaims, i.UsageLimit = code, []int64{o1.ID}, []int64{svc.ID}, 5, 3
		i.DiscountType, i.Value, i.MaxDiscount, i.MinSpend = domain.DiscountPercentage, 15, 30000, 50000
	})
	if v.Code != strings.ToUpper(code) || v.Type() != domain.VoucherCoded || v.ClaimsCount != 0 || v.UsageLimit != 3 || v.MaxDiscount != 30000 || v.MinSpend != 50000 {
		t.Errorf("created = %+v", v)
	}
	if len(v.Outlets) != 1 || v.Outlets[0].Name != o1.Name || len(v.Services) != 1 || v.Services[0].Name != svc.Name {
		t.Errorf("limits not named: %+v %+v", v.Outlets, v.Services)
	}

	// Another voucher cannot take the code; a public one has no code to take.
	in, _ := domain.ParseVoucher(domain.VoucherInput{Code: code, Title: "dup", DiscountType: domain.DiscountAmount, Value: 1})
	_, err = s.CreateVoucher(ctx, in)
	wantKind(t, "a taken code", err, apperr.KindConflict, "code")
	pub1 := voucherOf(t, s, "ZZ publik 1", nil)
	pub2 := voucherOf(t, s, "ZZ publik 2", nil)
	if pub1.Code != "" || pub2.Code != "" {
		t.Error("public vouchers have no code")
	}

	// A limit that is not there is refused.
	bad, _ := domain.ParseVoucher(domain.VoucherInput{Title: "x", DiscountType: domain.DiscountAmount, Value: 1, OutletIDs: []int64{987654321}})
	_, err = s.CreateVoucher(ctx, bad)
	wantKind(t, "a missing outlet", err, apperr.KindInvalid, "does not exist")
	bad, _ = domain.ParseVoucher(domain.VoucherInput{Title: "x", DiscountType: domain.DiscountAmount, Value: 1, ServiceIDs: []int64{987654321}})
	_, err = s.CreateVoucher(ctx, bad)
	wantKind(t, "a missing service", err, apperr.KindInvalid, "does not exist")

	// An update replaces it, limits included; no limits means everywhere.
	up, _ := domain.ParseVoucher(domain.VoucherInput{Code: code, Title: "ZZ baru", DiscountType: domain.DiscountAmount, Value: 20000, Status: domain.VoucherInactive})
	up.ID = v.ID
	got, err := s.UpdateVoucher(ctx, up)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "ZZ baru" || got.Value != 20000 || got.Status != domain.VoucherInactive || len(got.OutletIDs) != 0 || len(got.ServiceIDs) != 0 || got.MaxDiscount != 0 {
		t.Errorf("updated = %+v", got)
	}
	up.ID = 987654321
	_, err = s.UpdateVoucher(ctx, up)
	wantKind(t, "an unknown voucher", err, apperr.KindNotFound, "not found")
	if _, err := s.SetVoucherStatus(ctx, v.ID, domain.VoucherActive); err != nil {
		t.Error(err)
	}
	if err := s.DeleteVoucher(ctx, 987654321); kindOf(err) != apperr.KindNotFound {
		t.Errorf("delete unknown: %v", err)
	}
}

// The SQL list answers what the index answers: status, live, period, kind, outlet, search and sort.
func TestVoucherListSQL(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	o1, o2 := testOutlet(t, s, "ZZ vls a"), testOutlet(t, s, "ZZ vls b")
	now := time.Now().Truncate(time.Second)
	past, soon := now.Add(-time.Hour), now.Add(time.Hour)
	tag := strconv.FormatInt(now.UnixNano(), 36)

	everywhere := voucherOf(t, s, "ZZ"+tag+" semua", nil)
	onlyO1 := voucherOf(t, s, "ZZ"+tag+" satu", func(i *domain.VoucherInput) {
		i.OutletIDs, i.DiscountType, i.Value = []int64{o1.ID}, domain.DiscountPercentage, 10
	})
	notYet := voucherOf(t, s, "ZZ"+tag+" nanti", func(i *domain.VoucherInput) { i.StartsAt = &soon })
	over := voucherOf(t, s, "ZZ"+tag+" usai", func(i *domain.VoucherInput) { i.EndsAt = &past })
	off := voucherOf(t, s, "ZZ"+tag+" mati", func(i *domain.VoucherInput) { i.Status = domain.VoucherInactive })
	mine := []int64{everywhere.ID, onlyO1.ID, notYet.ID, over.ID, off.ID}

	list := func(q domain.VoucherQuery) []int64 {
		t.Helper()
		q.Q, q.Now, q.Limit, q.SortBy, q.SortDir = "ZZ"+tag, now, 100, "id", "asc"
		items, total, err := s.listVouchersSQL(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if int(total) != len(items) {
			t.Errorf("total %d for %d items", total, len(items))
		}
		return idsOfVouchers(items)
	}
	eq := func(what string, got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s = %v, want %v", what, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s = %v, want %v", what, got, want)
				return
			}
		}
	}
	eq("everything of mine", list(domain.VoucherQuery{}), mine...)
	eq("live", list(domain.VoucherQuery{Live: "true"}), everywhere.ID, onlyO1.ID)
	eq("not live", list(domain.VoucherQuery{Live: "false"}), notYet.ID, over.ID, off.ID)
	eq("inactive", list(domain.VoucherQuery{Status: "inactive"}), off.ID)
	eq("running", list(domain.VoucherQuery{Period: "running"}), everywhere.ID, onlyO1.ID, off.ID)
	eq("upcoming", list(domain.VoucherQuery{Period: "upcoming"}), notYet.ID)
	eq("ended", list(domain.VoucherQuery{Period: "ended"}), over.ID)
	eq("unlimited", list(domain.VoucherQuery{Period: "unlimited"}), everywhere.ID, onlyO1.ID, off.ID)
	eq("percentage", list(domain.VoucherQuery{DiscountType: "percentage"}), onlyO1.ID)
	eq("outlet 1", list(domain.VoucherQuery{OutletID: o1.ID}), mine...)
	eq("outlet 2", list(domain.VoucherQuery{OutletID: o2.ID}), everywhere.ID, notYet.ID, over.ID, off.ID)
	// Search also finds a code.
	code := testCode("zzs")
	coded := voucherOf(t, s, "ZZ"+tag+" kode", func(i *domain.VoucherInput) { i.Code = code })
	items, _, err := s.listVouchersSQL(ctx, domain.VoucherQuery{Q: strings.ToLower(code), SortBy: "id", SortDir: "asc", Limit: 10, Now: now})
	if err != nil || len(items) != 1 || items[0].ID != coded.ID {
		t.Errorf("search by code = %v, %v", idsOfVouchers(items), err)
	}
}

func idsOfVouchers(items []domain.Voucher) []int64 {
	out := make([]int64, 0, len(items))
	for _, v := range items {
		out = append(out, v.ID)
	}
	return out
}

// An injection hands the voucher to customers once each, within the claim limit, writes its audit, and the numbers add up.
func TestVoucherInject(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	now := time.Now()
	v := voucherOf(t, s, "ZZ inject", func(i *domain.VoucherInput) { i.Code, i.MaxClaims, i.UsageLimit = testCode("zzi"), 3, 2 })
	a, b, c, d := testCustomer(), testCustomer(), testCustomer(), testCustomer()
	off := testCustomer()
	off.Active = false

	res, err := s.Inject(ctx, domain.InjectInput{VoucherID: v.ID, Customers: []domain.InjectCustomer{a, b, off}, NotFound: 2}, testActor, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Target != 5 || res.Injected != 2 || res.Inactive != 1 || res.NotFound != 2 || res.VoucherTitle != v.Title || res.ActorName != "Tester" || res.ID == 0 {
		t.Errorf("first injection = %+v", res)
	}
	// Again: a and b already hold it; c fits (3rd claim); d does not.
	res, err = s.Inject(ctx, domain.InjectInput{VoucherID: v.ID, Customers: []domain.InjectCustomer{a, b, c, d}}, testActor, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Target != 4 || res.Injected != 1 || res.AlreadyHeld != 2 || res.OverLimit != 1 {
		t.Errorf("second injection = %+v", res)
	}
	got, _ := s.GetVoucher(ctx, v.ID)
	if got.ClaimsCount != 3 {
		t.Errorf("claims = %d, want 3", got.ClaimsCount)
	}
	grants, total, err := s.ListVoucherGrants(ctx, domain.GrantQuery{VoucherID: v.ID, Limit: 10})
	if err != nil || total != 3 || len(grants) != 3 {
		t.Fatalf("grants = %d/%d, %v", len(grants), total, err)
	}
	for _, g := range grants {
		if g.UsesRemaining != 2 || g.Source != domain.GrantInject || g.GrantedBy != "Tester" || g.ExpiresAt != nil {
			t.Errorf("grant = %+v", g)
		}
	}
	found, total, _ := s.ListVoucherGrants(ctx, domain.GrantQuery{VoucherID: v.ID, Q: a.Name, Limit: 10})
	if total != 1 || found[0].CustomerID != a.ID {
		t.Errorf("search by name = %+v", found)
	}
	found, total, _ = s.ListVoucherGrants(ctx, domain.GrantQuery{VoucherID: v.ID, Q: b.Phone[len(b.Phone)-6:], Limit: 10})
	if total != 1 || found[0].CustomerID != b.ID {
		t.Errorf("search by phone = %+v", found)
	}
	inj, total, err := s.ListInjections(ctx, domain.InjectionQuery{VoucherID: v.ID, Limit: 10})
	if err != nil || total != 2 || len(inj) != 2 || inj[0].Target != 4 || inj[1].Target != 5 {
		t.Errorf("injections = %+v / %d, %v (newest first)", inj, total, err)
	}

	// A voucher that cannot be sent refuses, and writes nothing.
	pub := voucherOf(t, s, "ZZ inject pub", nil)
	_, err = s.Inject(ctx, domain.InjectInput{VoucherID: pub.ID, Customers: []domain.InjectCustomer{a}}, testActor, now)
	wantKind(t, "a public voucher", err, apperr.KindConflict, "public voucher")
	_, err = s.Inject(ctx, domain.InjectInput{VoucherID: 987654321, Customers: []domain.InjectCustomer{a}}, testActor, now)
	wantKind(t, "an unknown voucher", err, apperr.KindNotFound, "not found")
	if _, total, _ := s.ListInjections(ctx, domain.InjectionQuery{VoucherID: pub.ID, Limit: 10}); total != 0 {
		t.Errorf("a refused injection left %d audit rows", total)
	}

	// With an expiry the grant carries it.
	exp := now.Add(48 * time.Hour).Truncate(time.Second)
	v2 := voucherOf(t, s, "ZZ inject exp", func(i *domain.VoucherInput) { i.Code = testCode("zze") })
	e := testCustomer()
	if _, err := s.Inject(ctx, domain.InjectInput{VoucherID: v2.ID, Customers: []domain.InjectCustomer{e}, ExpiresAt: &exp}, testActor, now); err != nil {
		t.Fatal(err)
	}
	g, _, _ := s.ListVoucherGrants(ctx, domain.GrantQuery{VoucherID: v2.ID, Limit: 10})
	if len(g) != 1 || g[0].ExpiresAt == nil || !g[0].ExpiresAt.Equal(exp) {
		t.Errorf("expiry = %+v", g)
	}
}

// A voucher that customers hold cannot be deleted, change its code or take a claim limit below its holders; switching it off keeps it
// out of the wallets.
func TestVoucherHeldGuards(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	now := time.Now()
	code := testCode("zzg")
	v := voucherOf(t, s, "ZZ guard", func(i *domain.VoucherInput) { i.Code, i.MaxClaims = code, 5 })
	a, b := testCustomer(), testCustomer()
	if _, err := s.Inject(ctx, domain.InjectInput{VoucherID: v.ID, Customers: []domain.InjectCustomer{a, b}}, testActor, now); err != nil {
		t.Fatal(err)
	}
	wantKind(t, "delete", s.DeleteVoucher(ctx, v.ID), apperr.KindConflict, "2 customer(s)")

	up, _ := domain.ParseVoucher(domain.VoucherInput{Code: testCode("zzh"), Title: v.Title, DiscountType: domain.DiscountAmount, Value: 1, MaxClaims: 5})
	up.ID = v.ID
	_, err := s.UpdateVoucher(ctx, up)
	wantKind(t, "a new code", err, apperr.KindConflict, "code cannot change")
	up, _ = domain.ParseVoucher(domain.VoucherInput{Code: code, Title: v.Title, DiscountType: domain.DiscountAmount, Value: 1, MaxClaims: 1})
	up.ID = v.ID
	_, err = s.UpdateVoucher(ctx, up)
	wantKind(t, "a lower claim limit", err, apperr.KindConflict, "claim limit")
	up, _ = domain.ParseVoucher(domain.VoucherInput{Code: code, Title: "ZZ guard new title", DiscountType: domain.DiscountAmount, Value: 99, MaxClaims: 2, Status: domain.VoucherActive})
	up.ID = v.ID
	if got, err := s.UpdateVoucher(ctx, up); err != nil || got.Value != 99 || got.ClaimsCount != 2 {
		t.Errorf("an update that keeps the code and the limit: %+v, %v", got, err)
	}

	// The wallet follows the status.
	w, err := s.ListWallet(ctx, a.ID, now)
	if err != nil || len(walletOf(w, v.ID)) != 1 {
		t.Fatalf("wallet = %v, %v", w, err)
	}
	if _, err := s.SetVoucherStatus(ctx, v.ID, domain.VoucherInactive); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.ListWallet(ctx, a.ID, now); len(walletOf(w, v.ID)) != 0 {
		t.Error("a voucher that is switched off is still in the wallet")
	}
	// Once the grants are gone (the test's cleanup does that), a delete works.
	if _, err := s.pool.Exec(ctx, `DELETE FROM voucher_grants WHERE voucher_id = $1`, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVoucher(ctx, v.ID); err != nil {
		t.Errorf("delete with no holders: %v", err)
	}
}

func walletOf(w []domain.WalletVoucher, id int64) []domain.WalletVoucher {
	var out []domain.WalletVoucher
	for _, x := range w {
		if x.ID == id {
			out = append(out, x)
		}
	}
	return out
}

// The wallet: live public vouchers, own grants of live vouchers with uses and time left, nobody else's.
func TestVoucherWalletAndClaim(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	now := time.Now().Truncate(time.Second)
	past, soon := now.Add(-time.Hour), now.Add(time.Hour)
	me, other := testCustomer(), testCustomer()

	pub := voucherOf(t, s, "ZZ w publik", func(i *domain.VoucherInput) { i.EndsAt = &soon })
	pubOff := voucherOf(t, s, "ZZ w publik mati", func(i *domain.VoucherInput) { i.Status = domain.VoucherInactive })
	pubOver := voucherOf(t, s, "ZZ w publik usai", func(i *domain.VoucherInput) { i.EndsAt = &past })
	code := testCode("zzw")
	coded := voucherOf(t, s, "ZZ w kode", func(i *domain.VoucherInput) { i.Code, i.UsageLimit, i.EndsAt = code, 4, &soon })
	codedLater := voucherOf(t, s, "ZZ w kode nanti", func(i *domain.VoucherInput) { i.Code, i.StartsAt = testCode("zzx"), &soon })

	ids := func(w []domain.WalletVoucher) map[int64]domain.WalletVoucher {
		m := map[int64]domain.WalletVoucher{}
		for _, x := range w {
			m[x.ID] = x
		}
		return m
	}
	w, err := s.ListWallet(ctx, me.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	m := ids(w)
	if _, ok := m[pub.ID]; !ok {
		t.Error("a live public voucher is in every wallet")
	}
	if m[pub.ID].Source != domain.WalletPublic || m[pub.ID].UsesRemaining != 1 || m[pub.ID].ExpiresAt() == nil {
		t.Errorf("public entry = %+v", m[pub.ID])
	}
	for name, id := range map[string]int64{"off": pubOff.ID, "ended": pubOver.ID, "coded, not held": coded.ID} {
		if _, ok := m[id]; ok {
			t.Errorf("%s voucher is in the wallet", name)
		}
	}

	// Claiming: a code works once, in any case, and the voucher is then the customer's with all its uses.
	typed := domain.ClaimInput{Code: strings.ToUpper(code), CustomerID: me.ID, CustomerName: me.Name, CustomerPhone: me.Phone}
	got, err := s.ClaimVoucher(ctx, typed, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != coded.ID || got.Source != domain.GrantClaim || got.UsesRemaining != 4 || got.GrantedAt == nil || got.ClaimsCount != 1 {
		t.Errorf("claimed = %+v", got)
	}
	_, err = s.ClaimVoucher(ctx, typed, now)
	wantKind(t, "claiming twice", err, apperr.KindConflict, "already")
	_, err = s.ClaimVoucher(ctx, domain.ClaimInput{Code: "NOSUCH1234", CustomerID: me.ID}, now)
	wantKind(t, "an unknown code", err, apperr.KindNotFound, "not valid")
	later := domain.ClaimInput{Code: codedLater.Code, CustomerID: me.ID}
	_, err = s.ClaimVoucher(ctx, later, now)
	wantKind(t, "a voucher that has not started", err, apperr.KindConflict, "not available")
	if _, err := s.SetVoucherStatus(ctx, pubOff.ID, domain.VoucherActive); err != nil {
		t.Fatal(err)
	}

	w, _ = s.ListWallet(ctx, me.ID, now)
	m = ids(w)
	if m[coded.ID].Source != domain.GrantClaim || m[coded.ID].UsesRemaining != 4 || len(m[coded.ID].Outlets) != 0 {
		t.Errorf("held entry = %+v", m[coded.ID])
	}
	if _, ok := m[pubOff.ID]; !ok {
		t.Error("a public voucher that was switched on is in the wallet")
	}
	// Somebody else's wallet does not have it; and the voucher stops showing once its window closes or the uses are gone.
	if w, _ := s.ListWallet(ctx, other.ID, now); len(walletOf(w, coded.ID)) != 0 {
		t.Error("another customer sees a voucher they never claimed")
	}
	if w, _ := s.ListWallet(ctx, me.ID, soon.Add(time.Second)); len(walletOf(w, coded.ID)) != 0 {
		t.Error("the voucher outlives its window in the wallet")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE voucher_grants SET uses_remaining = 0 WHERE voucher_id = $1 AND customer_id = $2`, coded.ID, me.ID); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.ListWallet(ctx, me.ID, now); len(walletOf(w, coded.ID)) != 0 {
		t.Error("a spent voucher is still in the wallet")
	}
	// A grant's own expiry hides it before the voucher ends.
	exp := now.Add(time.Minute)
	if _, err := s.pool.Exec(ctx, `UPDATE voucher_grants SET uses_remaining = 4, expires_at = $3 WHERE voucher_id = $1 AND customer_id = $2`, coded.ID, me.ID, exp); err != nil {
		t.Fatal(err)
	}
	w, _ = s.ListWallet(ctx, me.ID, now)
	if e := walletOf(w, coded.ID); len(e) != 1 || e[0].ExpiresAt() == nil || !e[0].ExpiresAt().Equal(exp) {
		t.Errorf("the grant's expiry = %+v", e)
	}
	if w, _ := s.ListWallet(ctx, me.ID, now.Add(2*time.Minute)); len(walletOf(w, coded.ID)) != 0 {
		t.Error("an expired grant is still in the wallet")
	}
}

// Many customers claiming the last places at once: exactly the limit gets in.
func TestVoucherClaimRace(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	code := testCode("zzr")
	v := voucherOf(t, s, "ZZ race", func(i *domain.VoucherInput) { i.Code, i.MaxClaims = code, 3 })
	var wg sync.WaitGroup
	var won, full atomic.Int32
	for i := 0; i < 12; i++ {
		c := testCustomer()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ClaimVoucher(ctx, domain.ClaimInput{Code: code, CustomerID: c.ID}, time.Now())
			switch {
			case err == nil:
				won.Add(1)
			case kindOf(err) == apperr.KindConflict:
				full.Add(1)
			default:
				t.Errorf("claim: %v", err)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 3 || full.Load() != 9 {
		t.Errorf("%d got it, %d were turned away; want 3 and 9", won.Load(), full.Load())
	}
	if got, _ := s.GetVoucher(ctx, v.ID); got.ClaimsCount != 3 {
		t.Errorf("claims = %d", got.ClaimsCount)
	}
}

// A service or an outlet that a voucher is limited to cannot be deleted; once the voucher is gone they can.
func TestVouchersBlockDeletes(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("ZZ vou svc2"), Unit: domain.UnitKg, Price: 5000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID) })
	limited := testOutlet(t, s, "ZZ vou limit")
	scoped := voucherOf(t, s, "ZZ terbatas", func(i *domain.VoucherInput) { i.OutletIDs, i.ServiceIDs = []int64{limited.ID}, []int64{svc.ID} })

	for name, del := range map[string]func() error{
		"service limited by a voucher": func() error { return s.DeleteService(ctx, svc.ID) },
		"outlet limited by a voucher":  func() error { return s.DeleteOutlet(ctx, limited.ID) },
	} {
		err := del()
		if kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), "voucher") || !strings.Contains(err.Error(), scoped.Title) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := s.DeleteVoucher(ctx, scoped.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteService(ctx, svc.ID); err != nil {
		t.Errorf("the service after its voucher is gone: %v", err)
	}
	if err := s.DeleteOutlet(ctx, limited.ID); err != nil {
		t.Errorf("the outlet after its voucher is gone: %v", err)
	}
}

// The table's own constraints hold even when the application's validation is bypassed.
func TestVoucherTableConstraints(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	for name, sql := range map[string]string{
		"a percentage over 100": `INSERT INTO vouchers (title, discount_type, value, color) VALUES ('ZZ c', 'percentage', 101, '#0009FF')`,
		"a cap on an amount":    `INSERT INTO vouchers (title, discount_type, value, max_discount, color) VALUES ('ZZ c', 'amount', 5, 1, '#0009FF')`,
		"claims on a public":    `INSERT INTO vouchers (title, discount_type, value, max_claims, color) VALUES ('ZZ c', 'amount', 5, 3, '#0009FF')`,
		"a lower-case code":     `INSERT INTO vouchers (code, title, discount_type, value, color) VALUES ('abcd', 'ZZ c', 'amount', 5, '#0009FF')`,
		"a colour that is not":  `INSERT INTO vouchers (title, discount_type, value, color) VALUES ('ZZ c', 'amount', 5, 'red')`,
		"no uses":               `INSERT INTO vouchers (title, discount_type, value, usage_limit, color) VALUES ('ZZ c', 'amount', 5, 0, '#0009FF')`,
		"an inverted window":    `INSERT INTO vouchers (title, discount_type, value, color, starts_at, ends_at) VALUES ('ZZ c', 'amount', 5, '#0009FF', now(), now() - interval '1 day')`,
	} {
		if _, err := s.pool.Exec(ctx, sql); err == nil {
			t.Errorf("%s was accepted", name)
			_, _ = s.pool.Exec(ctx, `DELETE FROM vouchers WHERE title = 'ZZ c'`)
		}
	}
}
