package postgres

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

const bannerImage = "https://assets.wuzz.care/banners/test.png"

func bannerOf(t *testing.T, s *Store, title string, mod func(*domain.BannerInput)) domain.Banner {
	t.Helper()
	in := domain.BannerInput{Title: unique(title), ImageURL: bannerImage, Status: domain.BannerActive}
	if mod != nil {
		mod(&in)
	}
	parsed, err := domain.ParseBanner(in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBanner(context.Background(), parsed)
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(context.Background(), `DELETE FROM banners WHERE id = $1`, b.ID) })
	return b
}

func testOutlet(t *testing.T, s *Store, name string) domain.Outlet {
	t.Helper()
	o, err := s.CreateOutlet(context.Background(), domain.Outlet{Name: unique(name), Latitude: -6.2, Longitude: 106.8, Active: true,
		Category: "laundry", DeliveryTypes: []string{"pickup"}, OpenTime: "08:00", CloseTime: "20:00", Timezone: "Asia/Jakarta"})
	if err != nil {
		t.Fatal(err)
	}
	// Banners go first (their cleanups were registered later, so they run earlier); an outlet a banner still names cannot be deleted.
	t.Cleanup(func() { _, _ = s.pool.Exec(context.Background(), `DELETE FROM outlets WHERE id = $1`, o.ID) })
	return o
}

func idsOf(items []domain.Banner) []int64 {
	out := make([]int64, 0, len(items))
	for _, b := range items {
		out = append(out, b.ID)
	}
	return out
}

func contains(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// A banner goes in last, comes back with its outlets named, and the live list judges it by the clock and the outlet.
func TestBannerLiveAndOutlets(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	o1, o2 := testOutlet(t, s, "ZZ ban a"), testOutlet(t, s, "ZZ ban b")
	now := time.Now().Truncate(time.Second)
	past, soon := now.Add(-time.Hour), now.Add(time.Hour)

	everywhere := bannerOf(t, s, "ZZ semua", nil)
	onlyO1 := bannerOf(t, s, "ZZ satu", func(i *domain.BannerInput) { i.OutletIDs = []int64{o1.ID} })
	notYet := bannerOf(t, s, "ZZ nanti", func(i *domain.BannerInput) { i.StartsAt = &soon })
	over := bannerOf(t, s, "ZZ usai", func(i *domain.BannerInput) { i.EndsAt = &past })
	off := bannerOf(t, s, "ZZ mati", func(i *domain.BannerInput) { i.Status = domain.BannerInactive })

	if !(everywhere.SortIndex < onlyO1.SortIndex && onlyO1.SortIndex < notYet.SortIndex) {
		t.Errorf("banners are not in the order they were made: %d %d %d", everywhere.SortIndex, onlyO1.SortIndex, notYet.SortIndex)
	}
	if len(onlyO1.Outlets) != 1 || onlyO1.Outlets[0].ID != o1.ID || onlyO1.Outlets[0].Name != o1.Name {
		t.Errorf("the outlet limit = %+v", onlyO1.Outlets)
	}

	live := func(outlet int64) []int64 {
		t.Helper()
		items, err := s.ListLiveBanners(ctx, outlet, now)
		if err != nil {
			t.Fatal(err)
		}
		return idsOf(items)
	}
	if got := live(o1.ID); !contains(got, everywhere.ID) || !contains(got, onlyO1.ID) {
		t.Errorf("live for outlet 1 = %v, want the banner for everyone and the one for this outlet", got)
	}
	if got := live(o2.ID); !contains(got, everywhere.ID) || contains(got, onlyO1.ID) {
		t.Errorf("live for outlet 2 = %v, want only the banner for everyone", got)
	}
	if got := live(0); !contains(got, everywhere.ID) || contains(got, onlyO1.ID) {
		t.Errorf("live with no outlet = %v, want only the banner for everyone", got)
	}
	for _, b := range []domain.Banner{notYet, over, off} {
		if contains(live(o1.ID), b.ID) {
			t.Errorf("%q is live and should not be", b.Title)
		}
	}
	// The same banner, an hour later: the one that was waiting has started.
	later, err := s.ListLiveBanners(ctx, o1.ID, now.Add(90*time.Minute))
	if err != nil || !contains(idsOf(later), notYet.ID) {
		t.Errorf("an hour and a half on: %v, %v", idsOf(later), err)
	}

	// The back office's list says the same through its own filters.
	listed := func(q domain.BannerQuery) []int64 {
		t.Helper()
		q.Now, q.Limit, q.SortBy = now, 100, "id"
		items, _, err := s.listBannersSQL(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return idsOf(items)
	}
	if got := listed(domain.BannerQuery{Live: "true", OutletID: o2.ID}); !contains(got, everywhere.ID) || contains(got, onlyO1.ID) || contains(got, notYet.ID) {
		t.Errorf("live for outlet 2 (back office) = %v", got)
	}
	if got := listed(domain.BannerQuery{Live: "false", OutletID: o1.ID}); contains(got, everywhere.ID) || !contains(got, notYet.ID) || !contains(got, over.ID) || !contains(got, off.ID) {
		t.Errorf("not live for outlet 1 (back office) = %v", got)
	}
	if got := listed(domain.BannerQuery{Q: "ZZ nanti"}); len(got) != 1 || got[0] != notYet.ID {
		t.Errorf("search = %v", got)
	}
}

// A banner needs a picture to be switched on, a target and outlets that exist, and the carousel's order is a permutation of all of them.
func TestBannerRulesAndOrder(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	draft := bannerOf(t, s, "ZZ draft", func(i *domain.BannerInput) { i.ImageURL, i.Status = "", domain.BannerInactive })
	if _, err := s.SetBannerStatus(ctx, draft.ID, domain.BannerActive); kindOf(err) != apperr.KindInvalid {
		t.Errorf("switching on a banner with no picture: %v", err)
	}
	if _, err := s.SetBannerStatus(ctx, 0, domain.BannerActive); kindOf(err) != apperr.KindNotFound {
		t.Errorf("switching on an unknown banner: %v", err)
	}
	withPicture := draft
	withPicture.ImageURL, withPicture.Status = bannerImage, domain.BannerActive
	if got, err := s.UpdateBanner(ctx, withPicture); err != nil || got.Status != domain.BannerActive || got.SortIndex != draft.SortIndex {
		t.Errorf("update kept the place? %+v, %v", got, err)
	}
	if got, err := s.SetBannerStatus(ctx, draft.ID, domain.BannerInactive); err != nil || got.Status != domain.BannerInactive {
		t.Errorf("switch off: %+v, %v", got, err)
	}

	for name, mod := range map[string]func(*domain.Banner){
		"a service that does not exist":            func(b *domain.Banner) { b.TargetType, b.TargetValue = domain.TargetService, "999999999" },
		"an outlet that does not exist":            func(b *domain.Banner) { b.TargetType, b.TargetValue = domain.TargetOutlet, "999999999" },
		"a limit to an outlet that does not exist": func(b *domain.Banner) { b.OutletIDs = []int64{999999999} },
	} {
		b := withPicture
		b.ID = 0
		mod(&b)
		if _, err := s.CreateBanner(ctx, b); kindOf(err) != apperr.KindInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}

	a, b := bannerOf(t, s, "ZZ ord a", nil), bannerOf(t, s, "ZZ ord b", nil)
	var all []int64
	rows, err := s.pool.Query(ctx, `SELECT id FROM banners ORDER BY sort_index, id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		all = append(all, id)
	}
	rows.Close()
	t.Cleanup(func() { _ = s.ReorderBanners(ctx, all) })
	reversed := make([]int64, len(all))
	for i, id := range all {
		reversed[len(all)-1-i] = id
	}
	if err := s.ReorderBanners(ctx, reversed); err != nil {
		t.Fatal(err)
	}
	items, _, err := s.listBannersSQL(ctx, domain.BannerQuery{SortBy: "sort_index", Limit: 1 << 20, Now: time.Now()})
	if err != nil || strings.Join(strIDs(idsOf(items)), ",") != strings.Join(strIDs(reversed), ",") {
		t.Errorf("after the reorder: %v, want %v (%v)", idsOf(items), reversed, err)
	}
	if err := s.ReorderBanners(ctx, []int64{a.ID, b.ID}); kindOf(err) != apperr.KindInvalid && len(all) != 2 {
		t.Errorf("an order that leaves banners out: %v", err)
	}
	if err := s.ReorderBanners(ctx, append(append([]int64{}, reversed...), reversed[0])); kindOf(err) != apperr.KindInvalid {
		t.Errorf("an order with a banner twice: %v", err)
	}
	if err := s.DeleteBanner(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBanner(ctx, a.ID); kindOf(err) != apperr.KindNotFound {
		t.Errorf("deleting twice: %v", err)
	}
}

func strIDs(ids []int64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.FormatInt(id, 10)
	}
	return out
}

// A service or an outlet a banner opens, and an outlet a banner is limited to, cannot be deleted; once the banner is gone they can.
func TestBannersBlockDeletes(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	svc, err := s.CreateService(ctx, domain.ServiceItem{Name: unique("ZZ ban svc"), Unit: domain.UnitKg, Price: 5000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, svc.ID) })
	opens, limited := testOutlet(t, s, "ZZ ban opens"), testOutlet(t, s, "ZZ ban limit")

	toSvc := bannerOf(t, s, "ZZ ke layanan", func(i *domain.BannerInput) {
		i.TargetType, i.TargetValue = domain.TargetService, strconv.FormatInt(svc.ID, 10)
	})
	toOutlet := bannerOf(t, s, "ZZ ke outlet", func(i *domain.BannerInput) {
		i.TargetType, i.TargetValue = domain.TargetOutlet, strconv.FormatInt(opens.ID, 10)
	})
	scoped := bannerOf(t, s, "ZZ terbatas", func(i *domain.BannerInput) { i.OutletIDs = []int64{limited.ID} })

	for name, del := range map[string]func() error{
		"service opened by a banner": func() error { return s.DeleteService(ctx, svc.ID) },
		"outlet opened by a banner":  func() error { return s.DeleteOutlet(ctx, opens.ID) },
		"outlet limited by a banner": func() error { return s.DeleteOutlet(ctx, limited.ID) },
	} {
		err := del()
		if kindOf(err) != apperr.KindConflict || !strings.Contains(err.Error(), "banner") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := s.DeleteOutlet(ctx, limited.ID); err == nil || !strings.Contains(err.Error(), scoped.Title) {
		t.Errorf("the refusal does not name the banner: %v", err)
	}

	for _, b := range []domain.Banner{toSvc, toOutlet, scoped} {
		if err := s.DeleteBanner(ctx, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteService(ctx, svc.ID); err != nil {
		t.Errorf("the service after its banner is gone: %v", err)
	}
	if err := s.DeleteOutlet(ctx, opens.ID); err != nil {
		t.Errorf("the outlet after its banner is gone: %v", err)
	}
	if err := s.DeleteOutlet(ctx, limited.ID); err != nil {
		t.Errorf("the limited outlet after its banner is gone: %v", err)
	}
}
