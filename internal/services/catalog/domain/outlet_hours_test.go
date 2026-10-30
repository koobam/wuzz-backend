package domain

import (
	"strings"
	"testing"
	"time"
)

func jakarta(h, m int) time.Time { return time.Date(2026, 10, 25, h, m, 0, 0, wallClockZone) }

func TestIsOpenAt(t *testing.T) {
	cases := []struct {
		name        string
		open, close string
		at          time.Time
		want        bool
	}{
		{"inside a day shift", "08:00", "22:00", jakarta(12, 0), true},
		{"opening minute is open", "08:00", "22:00", jakarta(8, 0), true},
		{"the minute before opening", "08:00", "22:00", jakarta(7, 59), false},
		{"last open minute", "08:00", "22:00", jakarta(21, 59), true},
		{"closing minute is closed", "08:00", "22:00", jakarta(22, 0), false},
		{"midnight, day shift", "08:00", "22:00", jakarta(0, 0), false},
		{"overnight, evening", "22:00", "02:00", jakarta(23, 30), true},
		{"overnight, after midnight", "22:00", "02:00", jakarta(1, 59), true},
		{"overnight, closing minute", "22:00", "02:00", jakarta(2, 0), false},
		{"overnight, afternoon", "22:00", "02:00", jakarta(15, 0), false},
		{"closes at midnight, evening", "08:00", "00:00", jakarta(23, 59), true},
		{"closes at midnight, midnight itself", "08:00", "00:00", jakarta(0, 0), false},
		{"opens at midnight", "00:00", "06:00", jakarta(0, 0), true},
		{"opens at midnight, after closing", "00:00", "06:00", jakarta(6, 0), false},
		{"hours that cannot be read", "8am", "22:00", jakarta(12, 0), false},
		{"equal hours are never open", "08:00", "08:00", jakarta(8, 0), false},
		{"the instant is read on the Jakarta clock", "08:00", "22:00", time.Date(2026, 10, 25, 5, 0, 0, 0, time.UTC), true},   // 12:00 WIB
		{"the instant in UTC after Jakarta closes", "08:00", "22:00", time.Date(2026, 10, 25, 15, 30, 0, 0, time.UTC), false}, // 22:30 WIB
	}
	for _, c := range cases {
		if got := IsOpenAt(c.open, c.close, c.at); got != c.want {
			t.Errorf("%s: IsOpenAt(%q, %q, %v) = %v, want %v", c.name, c.open, c.close, c.at, got, c.want)
		}
	}
}

func TestOutletDiscoveryValidation(t *testing.T) {
	base := func() Outlet {
		return Outlet{Name: "Wuzz", Category: "all", DeliveryTypes: []string{"pickup", "delivery"}, OpenTime: "08:00", CloseTime: "22:00"}
	}
	cases := []struct {
		name   string
		change func(*Outlet)
		ok     bool
	}{
		{"as is", func(*Outlet) {}, true},
		{"overnight", func(o *Outlet) { o.OpenTime, o.CloseTime = "22:00", "02:00" }, true},
		{"closes at 00:00", func(o *Outlet) { o.CloseTime = "00:00" }, true},
		{"padded times", func(o *Outlet) { o.OpenTime = " 08:30 " }, true},
		{"equal times", func(o *Outlet) { o.CloseTime = "08:00" }, false},
		{"both midnight", func(o *Outlet) { o.OpenTime, o.CloseTime = "00:00", "00:00" }, false},
		{"24:00 is not a time", func(o *Outlet) { o.CloseTime = "24:00" }, false},
		{"minutes out of range", func(o *Outlet) { o.OpenTime = "08:60" }, false},
		{"one digit hour", func(o *Outlet) { o.OpenTime = "8:00" }, false},
		{"seconds", func(o *Outlet) { o.CloseTime = "22:00:00" }, false},
		{"words", func(o *Outlet) { o.OpenTime = "morning" }, false},
		{"empty opening", func(o *Outlet) { o.OpenTime = "" }, false},
		{"category caffe", func(o *Outlet) { o.Category = "Caffe" }, true},
		{"category unknown", func(o *Outlet) { o.Category = "spa" }, false},
		{"pickup only", func(o *Outlet) { o.DeliveryTypes = []string{"pickup"} }, true},
		{"no delivery type", func(o *Outlet) { o.DeliveryTypes = nil }, false},
		{"unknown delivery type", func(o *Outlet) { o.DeliveryTypes = []string{"drone"} }, false},
		{"duplicate delivery type", func(o *Outlet) { o.DeliveryTypes = []string{"pickup", "pickup"} }, false},
	}
	for _, c := range cases {
		o := base()
		c.change(&o)
		if err := o.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestOutletApplyDefaults(t *testing.T) {
	var o Outlet
	o.ApplyDefaults()
	if o.Category != "all" || o.OpenTime != "08:00" || o.CloseTime != "22:00" || len(o.DeliveryTypes) != 2 {
		t.Errorf("defaults = %+v", o)
	}
	kept := Outlet{Category: "laundry", OpenTime: "07:00", CloseTime: "21:00", DeliveryTypes: []string{"pickup"}}
	kept.ApplyDefaults()
	if kept.Category != "laundry" || kept.OpenTime != "07:00" || kept.CloseTime != "21:00" || len(kept.DeliveryTypes) != 1 {
		t.Errorf("defaults overwrote %+v", kept)
	}
}

// at builds an instant from a wall-clock reading in a named zone. 2026-10-26 is a Monday, so day 0 of the week.
func at(tz string, day, h, m int) time.Time {
	loc := zoneOf(tz)
	return time.Date(2026, 10, 26+day, h, m, 0, 0, loc)
}

func hoursWith(change func(week []DayHours)) []DayHours {
	week := SameEveryDay("08:00", "22:00")
	change(week)
	return week
}

func TestIsOpenWeek(t *testing.T) {
	const wib, wita, wit = "Asia/Jakarta", "Asia/Makassar", "Asia/Jayapura"
	sundayClosed := hoursWith(func(w []DayHours) { w[6] = DayHours{Weekday: 6, Closed: true} })
	saturdayLate := hoursWith(func(w []DayHours) { w[5] = DayHours{Weekday: 5, OpenTime: "22:00", CloseTime: "02:00"} })
	differ := hoursWith(func(w []DayHours) { w[2] = DayHours{Weekday: 2, OpenTime: "10:00", CloseTime: "18:00"} }) // Wednesday
	all24 := SameEveryDay24()
	cases := []struct {
		name  string
		hours []DayHours
		tz    string
		at    time.Time
		want  bool
	}{
		{"an ordinary Monday", SameEveryDay("08:00", "22:00"), wib, at(wib, 0, 12, 0), true},
		{"Sunday is closed all day", sundayClosed, wib, at(wib, 6, 12, 0), false},
		{"the day before a closed Sunday is open", sundayClosed, wib, at(wib, 5, 12, 0), true},
		{"the day after a closed Sunday is open", sundayClosed, wib, at(wib, 7, 12, 0), true}, // Monday again
		{"Saturday 23:00 on a late Saturday", saturdayLate, wib, at(wib, 5, 23, 0), true},
		{"Sunday 01:00 is still Saturday's late hours", saturdayLate, wib, at(wib, 6, 1, 0), true},
		{"Sunday 02:00 is past them", saturdayLate, wib, at(wib, 6, 2, 0), false},
		{"Sunday 10:00 is Sunday's own", saturdayLate, wib, at(wib, 6, 10, 0), true},
		{"Monday 01:00 is not Sunday's overnight (Sunday is a day shift)", saturdayLate, wib, at(wib, 7, 1, 0), false},
		{"late Saturday does not make Friday night open", saturdayLate, wib, at(wib, 4, 23, 0), false},
		{"overnight spill into a CLOSED day still counts", hoursWith(func(w []DayHours) {
			w[5] = DayHours{Weekday: 5, OpenTime: "22:00", CloseTime: "02:00"}
			w[6] = DayHours{Weekday: 6, Closed: true}
		}), wib, at(wib, 6, 1, 0), true},
		{"Wednesday has its own hours: before", differ, wib, at(wib, 2, 9, 30), false},
		{"Wednesday has its own hours: inside", differ, wib, at(wib, 2, 10, 0), true},
		{"Wednesday has its own hours: after", differ, wib, at(wib, 2, 18, 0), false},
		{"Tuesday keeps the usual hours", differ, wib, at(wib, 1, 9, 30), true},
		// The same instant, three outlets: 21:30 in Jakarta is 22:30 in Makassar and 23:30 in Jayapura.
		{"21:30 WIB is open in WIB", SameEveryDay("08:00", "22:00"), wib, at(wib, 0, 21, 30), true},
		{"the same instant is 22:30 in WITA, closed", SameEveryDay("08:00", "22:00"), wita, at(wib, 0, 21, 30), false},
		{"and 23:30 in WIT, closed", SameEveryDay("08:00", "22:00"), wit, at(wib, 0, 21, 30), false},
		{"07:30 WIB is 08:30 in WITA, open there", SameEveryDay("08:00", "22:00"), wita, at(wib, 0, 7, 30), true},
		{"07:30 WIB is 09:30 in WIT, open there", SameEveryDay("08:00", "22:00"), wit, at(wib, 0, 7, 30), true},
		{"07:30 WIB is closed in WIB", SameEveryDay("08:00", "22:00"), wib, at(wib, 0, 7, 30), false},
		{"the weekday is the outlet's: Sunday 23:30 WIB is already Monday 00:30 in WIT", sundayClosed, wit, at(wib, 6, 22, 30), false},            // Monday 00:30 WIT, but opens 08:00
		{"a UTC instant is read on the outlet's clock", SameEveryDay("08:00", "22:00"), wit, time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), true}, // 09:00 WIT
		{"no hours at all is closed", nil, wib, at(wib, 0, 12, 0), false},
		// Days open 24 hours.
		{"a 24 hour Monday is open at 00:00", all24, wib, at(wib, 0, 0, 0), true},
		{"a 24 hour Monday is open at 12:00", all24, wib, at(wib, 0, 12, 0), true},
		{"a 24 hour Monday is open at 23:59", all24, wib, at(wib, 0, 23, 59), true},
		{"a 24 hour week is open on Sunday at 03:00 too", all24, wib, at(wib, 6, 3, 0), true},
		{"24 hours then a closed Sunday: open until midnight", hoursWith(func(w []DayHours) {
			w[5] = DayHours{Weekday: 5, Open24h: true, OpenTime: "00:00", CloseTime: "00:00"}
			w[6] = DayHours{Weekday: 6, Closed: true}
		}), wib, at(wib, 5, 23, 59), true},
		{"24 hours then a closed Sunday: closed from 00:00", hoursWith(func(w []DayHours) {
			w[5] = DayHours{Weekday: 5, Open24h: true, OpenTime: "00:00", CloseTime: "00:00"}
			w[6] = DayHours{Weekday: 6, Closed: true}
		}), wib, at(wib, 6, 0, 0), false},
		{"an overnight day into a 24 hour day: open all of the 24 hour day", hoursWith(func(w []DayHours) {
			w[4] = DayHours{Weekday: 4, OpenTime: "22:00", CloseTime: "02:00"}
			w[5] = DayHours{Weekday: 5, Open24h: true, OpenTime: "00:00", CloseTime: "00:00"}
		}), wib, at(wib, 5, 1, 0), true},
		{"a closed day before a 24 hour day opens it at 00:00", hoursWith(func(w []DayHours) {
			w[3] = DayHours{Weekday: 3, Closed: true}
			w[4] = DayHours{Weekday: 4, Open24h: true, OpenTime: "00:00", CloseTime: "00:00"}
		}), wib, at(wib, 4, 0, 0), true},
		{"a closed day before a 24 hour day is closed itself", hoursWith(func(w []DayHours) {
			w[3] = DayHours{Weekday: 3, Closed: true}
			w[4] = DayHours{Weekday: 4, Open24h: true, OpenTime: "00:00", CloseTime: "00:00"}
		}), wib, at(wib, 3, 12, 0), false},
		{"a 24 hour day follows the outlet's clock: 23:30 WIB Monday is Tuesday 01:30 in WIT, a closed Tuesday", hoursWith(func(w []DayHours) {
			w[0] = DayHours{Weekday: 0, Open24h: true, OpenTime: "00:00", CloseTime: "00:00"}
			w[1] = DayHours{Weekday: 1, Closed: true}
		}), wit, at(wib, 0, 23, 30), false},
		{"the same instant is Monday 22:30 in WITA: open there", hoursWith(func(w []DayHours) {
			w[0] = DayHours{Weekday: 0, Open24h: true, OpenTime: "00:00", CloseTime: "00:00"}
			w[1] = DayHours{Weekday: 1, Closed: true}
		}), wita, at(wib, 0, 21, 30), true},
	}
	for _, c := range cases {
		if got := IsOpenWeek(c.hours, c.tz, c.at); got != c.want {
			t.Errorf("%s: IsOpenWeek = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTodayHours(t *testing.T) {
	o := Outlet{Timezone: "Asia/Jayapura", Hours: hoursWith(func(w []DayHours) { w[0] = DayHours{Weekday: 0, Closed: true} })}
	// Sunday 23:30 in Jakarta is Monday 01:30 in Jayapura: that Monday is closed.
	if _, _, ok := o.TodayHours(at("Asia/Jakarta", 6, 23, 30)); ok {
		t.Errorf("Monday in Jayapura is closed")
	}
	if open, closeAt, ok := o.TodayHours(at("Asia/Jakarta", 1, 12, 0)); !ok || open != "08:00" || closeAt != "22:00" {
		t.Errorf("Tuesday = %q-%q ok=%v", open, closeAt, ok)
	}
}

func TestOutletWeekValidation(t *testing.T) {
	base := func() Outlet {
		return Outlet{Name: "Wuzz", Category: "all", DeliveryTypes: []string{"pickup"}, Timezone: "Asia/Makassar", Hours: SameEveryDay("08:00", "22:00")}
	}
	cases := []struct {
		name   string
		change func(*Outlet)
		ok     bool
	}{
		{"a full week", func(*Outlet) {}, true},
		{"WIT", func(o *Outlet) { o.Timezone = "Asia/Jayapura" }, true},
		{"no time zone means WIB", func(o *Outlet) { o.Timezone = "" }, true},
		{"a zone nobody offers", func(o *Outlet) { o.Timezone = "Asia/Tokyo" }, false},
		{"a label is not a zone", func(o *Outlet) { o.Timezone = "WIB" }, false},
		{"six days", func(o *Outlet) { o.Hours = o.Hours[:6] }, false},
		{"eight days", func(o *Outlet) {
			o.Hours = append(o.Hours, DayHours{Weekday: 0, OpenTime: "08:00", CloseTime: "22:00"})
		}, false},
		{"a day twice", func(o *Outlet) { o.Hours[3].Weekday = 2 }, false},
		{"weekday 7", func(o *Outlet) { o.Hours[6].Weekday = 7 }, false},
		{"weekday -1", func(o *Outlet) { o.Hours[0].Weekday = -1 }, false},
		{"a closed day ignores its times", func(o *Outlet) { o.Hours[6] = DayHours{Weekday: 6, Closed: true, OpenTime: "junk", CloseTime: "junk"} }, true},
		{"an open day with equal times", func(o *Outlet) { o.Hours[2].CloseTime = "08:00" }, false},
		{"an open day with no times", func(o *Outlet) { o.Hours[2] = DayHours{Weekday: 2} }, false},
		{"an overnight day", func(o *Outlet) { o.Hours[5] = DayHours{Weekday: 5, OpenTime: "22:00", CloseTime: "02:00"} }, true},
		{"the week out of order is put in order", func(o *Outlet) { o.Hours[0], o.Hours[6] = o.Hours[6], o.Hours[0] }, true},
	}
	for _, c := range cases {
		o := base()
		o.Hours = append([]DayHours(nil), o.Hours...)
		c.change(&o)
		if err := o.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", c.name, err, c.ok)
		}
	}
	// A valid week comes out Monday to Sunday, with the legacy single range set to the first open day, and a closed day cleared.
	o := base()
	o.Hours[0] = DayHours{Weekday: 0, Closed: true, OpenTime: "x", CloseTime: "y"}
	o.Hours[1] = DayHours{Weekday: 1, OpenTime: "09:00", CloseTime: "20:00"}
	o.Hours[0], o.Hours[6] = o.Hours[6], o.Hours[0]
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	for d, h := range o.Hours {
		if h.Weekday != d {
			t.Errorf("hours[%d].Weekday = %d", d, h.Weekday)
		}
	}
	if !o.Hours[0].Closed || o.Hours[0].OpenTime != "" || o.OpenTime != "09:00" || o.CloseTime != "20:00" {
		t.Errorf("monday=%+v legacy=%q-%q", o.Hours[0], o.OpenTime, o.CloseTime)
	}
}

func TestTimezoneLabelsAndDefaults(t *testing.T) {
	for tz, want := range map[string]string{"Asia/Jakarta": "WIB", "Asia/Makassar": "WITA", "Asia/Jayapura": "WIT", "Asia/Tokyo": ""} {
		if got := TimezoneLabel(tz); got != want {
			t.Errorf("TimezoneLabel(%q) = %q, want %q", tz, got, want)
		}
	}
	var o Outlet
	o.ApplyDefaults()
	if o.Timezone != "Asia/Jakarta" || o.OpenTime != "08:00" {
		t.Errorf("defaults = %+v", o)
	}
	if err := (&Outlet{Name: "N", Category: "all", DeliveryTypes: []string{"pickup"}, OpenTime: "07:00", CloseTime: "21:00"}).Validate(); err != nil {
		t.Errorf("the single-range form must still validate: %v", err)
	}
}

// SameEveryDay24 is a week that is open 24 hours on every day.
func SameEveryDay24() []DayHours {
	out := make([]DayHours, 7)
	for d := range out {
		out[d] = DayHours{Weekday: d, Open24h: true, OpenTime: AllDayTime, CloseTime: AllDayTime}
	}
	return out
}

func TestOutletDay24Validation(t *testing.T) {
	base := func() Outlet {
		return Outlet{Name: "Wuzz", Category: "all", DeliveryTypes: []string{"pickup"}, Timezone: "Asia/Jakarta", Hours: SameEveryDay("08:00", "22:00")}
	}
	cases := []struct {
		name   string
		change func(*Outlet)
		ok     bool
	}{
		{"a 24 hour day", func(o *Outlet) { o.Hours[2] = DayHours{Weekday: 2, Open24h: true} }, true},
		{"a 24 hour day ignores the times it came with", func(o *Outlet) {
			o.Hours[2] = DayHours{Weekday: 2, Open24h: true, OpenTime: "junk", CloseTime: "99:99"}
		}, true},
		{"a 24 hour day with equal times is fine (they are ignored)", func(o *Outlet) {
			o.Hours[2] = DayHours{Weekday: 2, Open24h: true, OpenTime: "08:00", CloseTime: "08:00"}
		}, true},
		{"closed and 24 hours together", func(o *Outlet) { o.Hours[2] = DayHours{Weekday: 2, Closed: true, Open24h: true} }, false},
		{"a whole week of 24 hours", func(o *Outlet) { o.Hours = SameEveryDay24() }, true},
		{"a normal day with equal times is still refused", func(o *Outlet) { o.Hours[2].CloseTime = "08:00" }, false},
	}
	for _, c := range cases {
		o := base()
		o.Hours = append([]DayHours(nil), o.Hours...)
		c.change(&o)
		if err := o.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", c.name, err, c.ok)
		}
	}
	// What a 24 hour day becomes: the times are the all-day marker, the legacy single range (taken from the first open day) reads
	// 00:00-23:59, and the error names the day.
	o := base()
	o.Hours[0] = DayHours{Weekday: 0, Closed: true}
	o.Hours[1] = DayHours{Weekday: 1, Open24h: true, OpenTime: "junk", CloseTime: "junk"}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	if h := o.Hours[1]; !h.Open24h || h.Closed || h.OpenTime != "00:00" || h.CloseTime != "00:00" {
		t.Errorf("tuesday = %+v", h)
	}
	if o.OpenTime != "00:00" || o.CloseTime != "23:59" {
		t.Errorf("legacy range = %q-%q, want 00:00-23:59", o.OpenTime, o.CloseTime)
	}
	bad := base()
	bad.Hours[4] = DayHours{Weekday: 4, Closed: true, Open24h: true}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "Friday") {
		t.Errorf("error = %v, want one naming Friday", err)
	}
}

func TestTodayHours24(t *testing.T) {
	o := Outlet{Timezone: "Asia/Jakarta", Hours: hoursWith(func(w []DayHours) { w[1] = DayHours{Weekday: 1, Open24h: true, OpenTime: "00:00", CloseTime: "00:00"} })}
	if open, closeAt, ok := o.TodayHours(at("Asia/Jakarta", 1, 12, 0)); !ok || open != "00:00" || closeAt != "23:59" {
		t.Errorf("a 24 hour Tuesday reads %q-%q ok=%v, want 00:00-23:59", open, closeAt, ok)
	}
	if !o.IsOpenNow(at("Asia/Jakarta", 1, 3, 0)) {
		t.Error("a 24 hour Tuesday is open at 03:00")
	}
}
