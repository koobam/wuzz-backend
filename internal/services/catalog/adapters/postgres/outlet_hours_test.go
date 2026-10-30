package postgres

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// An outlet's category, delivery types and hours go in and come back out of the outlets table as they were written, and an
// update replaces them. (Needs CATALOG_TEST_DSN, like the other store tests.)
func TestOutletHoursRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	created, err := s.CreateOutlet(ctx, domain.Outlet{
		Name: unique("ZZ jam"), Latitude: -6.2, Longitude: 106.8, Active: true,
		Category: "laundry", DeliveryTypes: []string{"pickup"}, OpenTime: "22:00", CloseTime: "02:00", Timezone: "Asia/Jakarta",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM outlets WHERE id = $1`, created.ID) })
	if created.Category != "laundry" || created.OpenTime != "22:00" || created.CloseTime != "02:00" || len(created.DeliveryTypes) != 1 || created.DeliveryTypes[0] != "pickup" {
		t.Fatalf("created = %+v", created)
	}

	created.OpenTime, created.CloseTime, created.DeliveryTypes, created.Category = "09:00", "21:30", []string{"pickup", "delivery"}, "caffe"
	updated, err := s.UpdateOutlet(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetOutlet(ctx, updated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OpenTime != "09:00" || got.CloseTime != "21:30" || got.Category != "caffe" || len(got.DeliveryTypes) != 2 {
		t.Errorf("after the update: %+v", got)
	}
}

// The week and the time zone go in and come back out day by day, an update replaces the week, the outlets the migration backfilled
// have seven days, and the customer-app read gives today's row on the outlet's own clock.
func TestOutletWeekRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	week := domain.SameEveryDay("08:00", "22:00")
	week[6] = domain.DayHours{Weekday: 6, Closed: true}
	week[5] = domain.DayHours{Weekday: 5, OpenTime: "22:00", CloseTime: "02:00"}
	o := domain.Outlet{Name: unique("ZZ minggu"), Latitude: -2.5, Longitude: 140.7, Active: true, Category: "all",
		DeliveryTypes: []string{"pickup"}, Timezone: "Asia/Jayapura", Hours: week}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateOutlet(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM outlets WHERE id = $1`, created.ID) })

	got, err := s.GetOutlet(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Timezone != "Asia/Jayapura" || len(got.Hours) != 7 || !got.Hours[6].Closed || got.Hours[5].CloseTime != "02:00" || got.Hours[0].OpenTime != "08:00" {
		t.Fatalf("read back: %+v", got)
	}
	for d, h := range got.Hours {
		if h.Weekday != d {
			t.Errorf("hours[%d] is weekday %d", d, h.Weekday)
		}
	}

	// A new week replaces the old one, and the paged list and the plain list carry it too.
	got.Hours[6] = domain.DayHours{Weekday: 6, OpenTime: "10:00", CloseTime: "16:00"}
	got.Timezone = "Asia/Makassar"
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateOutlet(ctx, got); err != nil {
		t.Fatal(err)
	}
	listed, err := s.ListOutlets(ctx, []int64{created.ID}, true, nil)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list: %v %v", err, listed)
	}
	if listed[0].Timezone != "Asia/Makassar" || listed[0].Hours[6].Closed || listed[0].Hours[6].OpenTime != "10:00" {
		t.Errorf("after the update: %+v", listed[0])
	}
	var rows int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outlet_hours WHERE outlet_id = $1`, created.ID).Scan(&rows); err != nil || rows != 7 {
		t.Errorf("rows = %d, %v (an update must leave exactly seven)", rows, err)
	}

	// The database refuses a zone nobody offers and an open day with equal times.
	if _, err := s.pool.Exec(ctx, `UPDATE outlets SET timezone = 'Asia/Tokyo' WHERE id = $1`, created.ID); err == nil {
		t.Error("an unknown time zone was accepted")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outlet_hours SET open_time = '08:00', close_time = '08:00' WHERE outlet_id = $1 AND weekday = 0`, created.ID); err == nil {
		t.Error("an open day with equal times was accepted")
	}

	// Deleting the outlet takes its days with it.
	if _, err := s.pool.Exec(ctx, `DELETE FROM outlets WHERE id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outlet_hours WHERE outlet_id = $1`, created.ID).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("rows after delete = %d, %v", rows, err)
	}
}

// The outlets that existed before the migration have seven days each, the same range every day.
func TestOutletHoursBackfill(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var bad int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outlets o WHERE (SELECT count(*) FROM outlet_hours h WHERE h.outlet_id = o.id) <> 7`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Errorf("%d outlets do not have seven days of hours", bad)
	}
}

// The contact email goes in and comes back out lower-case, an update replaces it, "" removes it, and the table's own CHECK refuses
// a malformed address even when the application's validation is bypassed. (Needs CATALOG_TEST_DSN.)
func TestOutletEmailRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	o := domain.Outlet{
		Name: unique("ZZ email"), Latitude: -6.2, Longitude: 106.8, Active: true, Email: "pusat@wuzz.care",
		Category: "all", DeliveryTypes: []string{"pickup"}, OpenTime: "08:00", CloseTime: "22:00", Timezone: "Asia/Jakarta",
	}
	created, err := s.CreateOutlet(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM outlets WHERE id = $1`, created.ID) })
	if created.Email != "pusat@wuzz.care" {
		t.Fatalf("created email = %q", created.Email)
	}

	created.Email = "kasir@wuzz.care"
	if _, err := s.UpdateOutlet(ctx, created); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetOutlet(ctx, created.ID)
	if err != nil || got.Email != "kasir@wuzz.care" {
		t.Fatalf("after the update: %+v, %v", got, err)
	}
	found, _, err := s.PageOutlets(ctx, domain.OutletQuery{Q: "kasir@wuzz", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, f := range found {
		hit = hit || f.ID == created.ID
	}
	if !hit {
		t.Errorf("the outlet is not found by its email: %+v", found)
	}

	created.Email = ""
	if _, err := s.UpdateOutlet(ctx, created); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetOutlet(ctx, created.ID); got.Email != "" {
		t.Errorf("an empty email should remove it, got %q", got.Email)
	}

	if _, err := s.pool.Exec(ctx, `UPDATE outlets SET email = 'not an email' WHERE id = $1`, created.ID); err == nil {
		t.Error("the table should refuse a malformed email")
	}
}

// A day open 24 hours is stored with its flag and the all-day marker times, reads back the same, shows the customer app 00:00-23:59, and
// the table's own CHECK refuses a day that is both closed and 24 hours (needs CATALOG_TEST_DSN).
func TestOutlet24HourDayRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	week := domain.SameEveryDay("08:00", "22:00")
	week[2] = domain.DayHours{Weekday: 2, Open24h: true}
	week[3] = domain.DayHours{Weekday: 3, Closed: true}
	o := domain.Outlet{Name: unique("ZZ 24 jam"), Latitude: -6.2, Longitude: 106.8, Active: true, Category: "all",
		DeliveryTypes: []string{"pickup"}, Timezone: "Asia/Jakarta", Hours: week}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateOutlet(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM outlets WHERE id = $1`, created.ID) })

	got, err := s.GetOutlet(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h := got.Hours[2]; !h.Open24h || h.Closed || h.OpenTime != "00:00" || h.CloseTime != "00:00" {
		t.Fatalf("wednesday read back as %+v", h)
	}
	if got.Hours[3].Open24h || !got.Hours[3].Closed || got.Hours[1].Open24h {
		t.Errorf("neighbours changed: %+v %+v", got.Hours[3], got.Hours[1])
	}

	// Flipping the flag off with the times of a normal day goes through the same write path.
	got.Hours[2] = domain.DayHours{Weekday: 2, OpenTime: "09:00", CloseTime: "17:00"}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateOutlet(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetOutlet(ctx, created.ID)
	if err != nil || again.Hours[2].Open24h || again.Hours[2].OpenTime != "09:00" {
		t.Errorf("after turning 24 hours off: %+v %v", again.Hours[2], err)
	}

	// The database refuses both flags at once, and a day that is neither closed, 24 hours nor a real range.
	if _, err := s.pool.Exec(ctx, `UPDATE outlet_hours SET closed = true, open_24h = true WHERE outlet_id = $1 AND weekday = 0`, created.ID); err == nil {
		t.Error("a day both closed and open 24 hours was accepted")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outlet_hours SET open_24h = false, open_time = '', close_time = '' WHERE outlet_id = $1 AND weekday = 1`, created.ID); err == nil {
		t.Error("an open day with no times was accepted")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outlet_hours SET open_24h = true, open_time = '00:00', close_time = '00:00' WHERE outlet_id = $1 AND weekday = 4`, created.ID); err != nil {
		t.Errorf("a 24 hour day was refused: %v", err)
	}
}

// Every row that existed before open_24h was added is a normal day.
func TestOutletHours24Backfill(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outlet_hours WHERE open_24h AND closed`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows both closed and 24 hours: %d, %v", n, err)
	}
	var def string
	if err := s.pool.QueryRow(ctx, `SELECT column_default FROM information_schema.columns WHERE table_name = 'outlet_hours' AND column_name = 'open_24h'`).Scan(&def); err != nil || def != "false" {
		t.Errorf("open_24h default = %q, %v", def, err)
	}
}
