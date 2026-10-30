package domain

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// What a new outlet is when the caller says nothing: the same defaults as the outlets table.
const (
	DefaultOutletCategory  = "all"
	DefaultOutletOpenTime  = "08:00"
	DefaultOutletCloseTime = "22:00"
	DefaultOutletTimezone  = "Asia/Jakarta"
)

// The time zones an outlet's clock can be in, with the name people use for each: WIB (west), WITA (central), WIT (east).
var timezoneLabels = map[string]string{"Asia/Jakarta": "WIB", "Asia/Makassar": "WITA", "Asia/Jayapura": "WIT"}

// Timezones lists them from west to east.
var Timezones = []string{"Asia/Jakarta", "Asia/Makassar", "Asia/Jayapura"}

// TimezoneLabel is "WIB", "WITA" or "WIT" for an allowed zone, and "" for anything else.
func TimezoneLabel(tz string) string { return timezoneLabels[tz] }

// zoneOf is the clock of an allowed zone. None of the three has daylight saving, so a fixed offset is the same clock when the
// system has no time zone database.
func zoneOf(tz string) *time.Location {
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	switch tz {
	case "Asia/Makassar":
		return time.FixedZone("WITA", 8*60*60)
	case "Asia/Jayapura":
		return time.FixedZone("WIT", 9*60*60)
	}
	return time.FixedZone("WIB", 7*60*60)
}

// DayHours is the opening hours of one day of the week. Weekday counts from Monday = 0 to Sunday = 6. A day is closed (no times),
// open 24 hours (Open24h: the whole local day, no times of its own), or has an opening and a closing "HH:MM" on the outlet's clock,
// where a closing time before the opening time means the outlet closes after midnight, in the early hours of the next day.
// A 24 hour day is stored and returned with OpenTime and CloseTime "00:00" (the flag, not the times, says "all day").
type DayHours struct {
	Weekday   int
	Closed    bool
	Open24h   bool
	OpenTime  string
	CloseTime string
}

// The times of a day open 24 hours: as stored, and as the single daily range older clients and the customer app are given.
const (
	AllDayTime      = "00:00"
	AllDayCompatEnd = "23:59"
)

// DefaultHours is a week of the default hours: open every day from 08:00 to 22:00.
func DefaultHours() []DayHours { return SameEveryDay(DefaultOutletOpenTime, DefaultOutletCloseTime) }

// SameEveryDay is the same hours on all seven days.
func SameEveryDay(open, closeAt string) []DayHours {
	out := make([]DayHours, 7)
	for d := range out {
		out[d] = DayHours{Weekday: d, OpenTime: open, CloseTime: closeAt}
	}
	return out
}

// mondayFirst turns Go's weekday (Sunday = 0) into ours (Monday = 0).
func mondayFirst(w time.Weekday) int { return (int(w) + 6) % 7 }

// The categories and delivery types an outlet can have.
var (
	outletCategories = map[string]bool{"laundry": true, "caffe": true, "all": true}
	deliveryTypes    = map[string]bool{"pickup": true, "delivery": true}
)

// WallClock is the zone the outlets' opening hours are written in.
var wallClockZone = loadJakarta()

func loadJakarta() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*60*60) // Jakarta has no daylight saving: the fixed offset is the same clock
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// ClockMinutes reads "HH:MM" (00:00 to 23:59, 24 hour) as minutes after midnight.
func ClockMinutes(s string) (int, bool) {
	m := hhmm.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return h*60 + min, true
}

// IsOpenAt says whether an outlet that opens at open and closes at closeAt (both "HH:MM") is open at the moment `at`, on the Jakarta
// clock. It is open from the opening minute up to, not including, the closing minute; a closing time before the opening time
// means it closes after midnight. Hours that cannot be read make it closed.
func IsOpenAt(open, closeAt string, at time.Time) bool {
	o, ok1 := ClockMinutes(open)
	c, ok2 := ClockMinutes(closeAt)
	if !ok1 || !ok2 || o == c {
		return false
	}
	local := at.In(wallClockZone)
	now := local.Hour()*60 + local.Minute()
	if o < c {
		return now >= o && now < c
	}
	return now >= o || now < c
}

// IsOpenWeek says whether an outlet with these weekly hours, on the clock of its time zone, is open at the moment `at`. It is open
// when today's hours say so, or when yesterday's hours run past midnight and are still going (Saturday 22:00-02:00 is open on
// Sunday at 01:00). Days that are missing, closed or unreadable are closed.
func IsOpenWeek(hours []DayHours, tz string, at time.Time) bool {
	local := at.In(zoneOf(tz))
	now := local.Hour()*60 + local.Minute()
	today := mondayFirst(local.Weekday())
	yesterday := (today + 6) % 7
	for _, h := range hours {
		if h.Closed {
			continue
		}
		if h.Open24h {
			if h.Weekday == today {
				return true
			}
			continue
		}
		o, ok1 := ClockMinutes(h.OpenTime)
		c, ok2 := ClockMinutes(h.CloseTime)
		if !ok1 || !ok2 || o == c {
			continue
		}
		switch {
		case h.Weekday == today && o < c && now >= o && now < c:
			return true
		case h.Weekday == today && o > c && now >= o:
			return true
		case h.Weekday == yesterday && o > c && now < c:
			return true
		}
	}
	return false
}

// TodayHours are the opening and closing times of the day it is now on the outlet's clock, and false when that day is closed.
// A day open 24 hours reads 00:00 to 23:59 (the closest single range an older client can show).
func (o Outlet) TodayHours(now time.Time) (open, closeAt string, ok bool) {
	today := mondayFirst(now.In(zoneOf(o.Timezone)).Weekday())
	for _, h := range o.Hours {
		if h.Weekday == today && !h.Closed {
			if h.Open24h {
				return AllDayTime, AllDayCompatEnd, true
			}
			return h.OpenTime, h.CloseTime, true
		}
	}
	return "", "", false
}

// IsOpenNow is IsOpenWeek at the given time. An outlet that has no weekly hours loaded falls back to its single daily range.
func (o Outlet) IsOpenNow(now time.Time) bool {
	if len(o.Hours) == 0 {
		return IsOpenAt(o.OpenTime, o.CloseTime, now)
	}
	return IsOpenWeek(o.Hours, o.Timezone, now)
}

// ApplyDefaults fills what a new outlet left empty with the table's defaults.
func (o *Outlet) ApplyDefaults() {
	if strings.TrimSpace(o.Category) == "" {
		o.Category = DefaultOutletCategory
	}
	if len(o.DeliveryTypes) == 0 {
		o.DeliveryTypes = []string{"pickup", "delivery"}
	}
	if len(o.Hours) == 0 {
		if strings.TrimSpace(o.OpenTime) == "" {
			o.OpenTime = DefaultOutletOpenTime
		}
		if strings.TrimSpace(o.CloseTime) == "" {
			o.CloseTime = DefaultOutletCloseTime
		}
	}
	if strings.TrimSpace(o.Timezone) == "" {
		o.Timezone = DefaultOutletTimezone
	}
}

func (o *Outlet) validateDiscovery() error {
	o.Category = strings.ToLower(strings.TrimSpace(o.Category))
	o.OpenTime = strings.TrimSpace(o.OpenTime)
	o.CloseTime = strings.TrimSpace(o.CloseTime)
	if !outletCategories[o.Category] {
		return apperr.Invalid("The category must be laundry, caffe or all")
	}
	if err := o.validateHours(); err != nil {
		return err
	}
	if len(o.DeliveryTypes) == 0 {
		return apperr.Invalid("Choose at least one of pickup and delivery")
	}
	seen := map[string]bool{}
	for i, d := range o.DeliveryTypes {
		d = strings.ToLower(strings.TrimSpace(d))
		o.DeliveryTypes[i] = d
		if !deliveryTypes[d] {
			return apperr.Invalid("Delivery types can only be pickup or delivery")
		}
		if seen[d] {
			return apperr.Invalid("A delivery type is listed twice")
		}
		seen[d] = true
	}
	return nil
}

// validateHours checks the time zone and the week. The week is either the single daily range (OpenTime and CloseTime: the same on
// all seven days, the way outlets were before hours differed by day) or exactly seven days, one per weekday. Either way the
// outlet ends with seven days in Monday-to-Sunday order, and OpenTime and CloseTime are the first open day's hours.
func (o *Outlet) validateHours() error {
	o.Timezone = strings.TrimSpace(o.Timezone)
	if o.Timezone == "" {
		o.Timezone = DefaultOutletTimezone
	}
	if timezoneLabels[o.Timezone] == "" {
		return apperr.Invalid("The time zone must be Asia/Jakarta (WIB), Asia/Makassar (WITA) or Asia/Jayapura (WIT)")
	}
	if len(o.Hours) == 0 {
		o.OpenTime = strings.TrimSpace(o.OpenTime)
		o.CloseTime = strings.TrimSpace(o.CloseTime)
		if err := checkRange(o.OpenTime, o.CloseTime, "The opening", "The closing"); err != nil {
			return err
		}
		o.Hours = SameEveryDay(o.OpenTime, o.CloseTime)
		return nil
	}
	if len(o.Hours) != 7 {
		return apperr.Invalid("The opening hours need all seven days, Monday to Sunday")
	}
	week := make([]DayHours, 7)
	seen := map[int]bool{}
	for _, h := range o.Hours {
		if h.Weekday < 0 || h.Weekday > 6 {
			return apperr.Invalid("A weekday must be from 0 (Monday) to 6 (Sunday)")
		}
		if seen[h.Weekday] {
			return apperr.Invalid("A weekday is listed twice in the opening hours")
		}
		seen[h.Weekday] = true
		h.OpenTime, h.CloseTime = strings.TrimSpace(h.OpenTime), strings.TrimSpace(h.CloseTime)
		switch {
		case h.Closed && h.Open24h:
			return apperr.Invalid(dayName(h.Weekday) + ": a day cannot be closed and open 24 hours at the same time")
		case h.Closed:
			h.OpenTime, h.CloseTime = "", ""
		case h.Open24h:
			// Whatever times came with it are ignored: the day has none of its own.
			h.OpenTime, h.CloseTime = AllDayTime, AllDayTime
		default:
			if err := checkRange(h.OpenTime, h.CloseTime, "The opening", "The closing"); err != nil {
				return apperr.Invalid(dayName(h.Weekday) + ": " + strings.ToLower(err.Error()[:1]) + err.Error()[1:])
			}
		}
		week[h.Weekday] = h
	}
	o.Hours = week
	o.OpenTime, o.CloseTime = DefaultOutletOpenTime, DefaultOutletCloseTime
	for _, h := range week {
		if h.Closed {
			continue
		}
		o.OpenTime, o.CloseTime = h.OpenTime, h.CloseTime
		if h.Open24h {
			o.OpenTime, o.CloseTime = AllDayTime, AllDayCompatEnd
		}
		break
	}
	return nil
}

func checkRange(open, closeAt, openWord, closeWord string) error {
	o, ok := ClockMinutes(open)
	if !ok {
		return apperr.Invalid(openWord + " time must be HH:MM on a 24 hour clock, from 00:00 to 23:59")
	}
	c, ok := ClockMinutes(closeAt)
	if !ok {
		return apperr.Invalid(closeWord + " time must be HH:MM on a 24 hour clock, from 00:00 to 23:59")
	}
	if o == c {
		return apperr.Invalid("The opening and closing times cannot be the same")
	}
	return nil
}

var dayNames = [7]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func dayName(weekday int) string { return dayNames[weekday] }

// SortedHours returns the week in Monday-to-Sunday order (a copy).
func SortedHours(h []DayHours) []DayHours {
	out := append([]DayHours(nil), h...)
	sort.Slice(out, func(i, j int) bool { return out[i].Weekday < out[j].Weekday })
	return out
}
