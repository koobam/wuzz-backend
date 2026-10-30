package catalogapi

import (
	"reflect"
	"testing"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
)

func strp(s string) *string       { return &s }
func listp(s ...string) *[]string { return &s }

func week() []*catalogv1.OutletDay {
	days := make([]*catalogv1.OutletDay, 7)
	for d := range days {
		days[d] = &catalogv1.OutletDay{Weekday: int32(d), OpenTime: "07:00", CloseTime: "21:00"}
	}
	days[0] = &catalogv1.OutletDay{Weekday: 0, Closed: true} // Monday is closed
	days[5] = &catalogv1.OutletDay{Weekday: 5, OpenTime: "22:00", CloseTime: "02:00"}
	return days
}

func TestMergeOutletUpdate(t *testing.T) {
	cur := &catalogv1.Outlet{Id: 3, Email: "pusat@wuzz.care", Active: false, Images: []string{"https://x/a.jpg"}, Category: "laundry",
		DeliveryTypes: []string{"pickup"}, OpenTime: "07:00", CloseTime: "21:00", Timezone: "Asia/Makassar", Hours: week()}
	yes := true
	cases := []struct {
		name string
		body outletBody
		want func(*catalogv1.UpdateOutletRequest) bool
	}{
		{"a body with only a name keeps everything stored", outletBody{Name: "N"}, func(r *catalogv1.UpdateOutletRequest) bool {
			return !r.Active && r.Email == "pusat@wuzz.care" && reflect.DeepEqual(r.Images, cur.Images) && r.Category == "laundry" && r.Timezone == "Asia/Makassar" &&
				len(r.Hours) == 7 && r.Hours[0].Closed && r.Hours[5].OpenTime == "22:00" && reflect.DeepEqual(r.DeliveryTypes, []string{"pickup"})
		}},
		{"a range without hours is one range for the whole week", outletBody{Name: "N", OpenTime: strp("09:00"), CloseTime: strp("02:00")}, func(r *catalogv1.UpdateOutletRequest) bool {
			return r.Hours == nil && r.OpenTime == "09:00" && r.CloseTime == "02:00" && r.Category == "laundry"
		}},
		{"only the opening time: the closing time is the first open day's", outletBody{Name: "N", OpenTime: strp("10:00")}, func(r *catalogv1.UpdateOutletRequest) bool {
			return r.Hours == nil && r.OpenTime == "10:00" && r.CloseTime == "21:00"
		}},
		{"hours in the body replace the stored week, a range beside them is ignored", outletBody{Name: "N", OpenTime: strp("09:00"), Hours: &[]outletDayBody{{Weekday: 6, Closed: true}}}, func(r *catalogv1.UpdateOutletRequest) bool {
			return len(r.Hours) == 1 && r.Hours[0].Weekday == 6 && r.Hours[0].Closed && r.OpenTime == ""
		}},
		{"a time zone in the body", outletBody{Name: "N", Timezone: strp("Asia/Jayapura")}, func(r *catalogv1.UpdateOutletRequest) bool {
			return r.Timezone == "Asia/Jayapura" && len(r.Hours) == 7
		}},
		{"delivery types and category", outletBody{Name: "N", Category: strp("caffe"), DeliveryTypes: listp("pickup", "delivery")}, func(r *catalogv1.UpdateOutletRequest) bool {
			return r.Category == "caffe" && reflect.DeepEqual(r.DeliveryTypes, []string{"pickup", "delivery"})
		}},
		{"an email in the body", outletBody{Name: "N", Email: strp("kasir@wuzz.care")}, func(r *catalogv1.UpdateOutletRequest) bool { return r.Email == "kasir@wuzz.care" }},
		{"an empty email removes it", outletBody{Name: "N", Email: strp("")}, func(r *catalogv1.UpdateOutletRequest) bool { return r.Email == "" }},
		{"active in the body wins", outletBody{Name: "N", Active: &yes}, func(r *catalogv1.UpdateOutletRequest) bool { return r.Active }},
		{"an empty list of pictures removes them", outletBody{Name: "N", Images: listp()}, func(r *catalogv1.UpdateOutletRequest) bool { return len(r.Images) == 0 }},
	}
	for _, c := range cases {
		if got := mergeOutletUpdate(3, cur, c.body); !c.want(got) {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
}

func TestOutletDTOHoursAndLabel(t *testing.T) {
	dto := outletDTO(&catalogv1.Outlet{Id: 1, Timezone: "Asia/Jayapura", Hours: week(), OpenTime: "", CloseTime: ""})
	if dto.TimezoneLabel != "WIT" || len(dto.Hours) != 7 || !dto.Hours[0].Closed || dto.Hours[5].CloseTime != "02:00" {
		t.Errorf("dto = %+v", dto)
	}
	view := outletViewDTO(&catalogv1.OutletView{Id: 1}) // a day the outlet is closed: no hours rather than made-up ones
	if len(view.Hours) != 0 {
		t.Errorf("a closed day must send no hours, got %v", view.Hours)
	}
}

func TestOutletDay24HourBodyAndDTO(t *testing.T) {
	days := []outletDayBody{{Weekday: 0, Open24h: true}, {Weekday: 1, Closed: true}}
	proto := daysToProto(&days)
	if len(proto) != 2 || !proto[0].GetOpen_24H() || proto[0].GetClosed() || proto[1].GetOpen_24H() || !proto[1].GetClosed() {
		t.Fatalf("daysToProto = %+v", proto)
	}
	dto := outletDays([]*catalogv1.OutletDay{{Weekday: 0, Open_24H: true, OpenTime: "00:00", CloseTime: "00:00"}, {Weekday: 1, OpenTime: "08:00", CloseTime: "22:00"}})
	if !dto[0].Open24h || dto[0].OpenTime != "00:00" || dto[1].Open24h {
		t.Errorf("outletDays = %+v", dto)
	}
	// A client that sends one range, with the stored week starting with a 24 hour day, gets 00:00-23:59 as "the hours" to start from.
	open, closeAt := firstOpenRange([]*catalogv1.OutletDay{{Weekday: 0, Closed: true}, {Weekday: 1, Open_24H: true}, {Weekday: 2, OpenTime: "08:00", CloseTime: "22:00"}})
	if open != "00:00" || closeAt != "23:59" {
		t.Errorf("firstOpenRange = %q-%q", open, closeAt)
	}
	// Hours sent in the body replace the stored week and carry the flag through the merge.
	cur := &catalogv1.Outlet{Id: 3, Timezone: "Asia/Jakarta", Hours: week()}
	got := mergeOutletUpdate(3, cur, outletBody{Name: "N", Hours: &days})
	if len(got.Hours) != 2 || !got.Hours[0].GetOpen_24H() {
		t.Errorf("merged hours = %+v", got.Hours)
	}
	// The stored week, untouched, keeps its 24 hour days when only the name changes.
	stored := &catalogv1.Outlet{Id: 3, Timezone: "Asia/Jakarta", Hours: []*catalogv1.OutletDay{{Weekday: 0, Open_24H: true, OpenTime: "00:00", CloseTime: "00:00"}}}
	if got := mergeOutletUpdate(3, stored, outletBody{Name: "N"}); len(got.Hours) != 1 || !got.Hours[0].GetOpen_24H() {
		t.Errorf("a name-only update dropped the 24 hour flag: %+v", got.Hours)
	}
}

func TestOnlyActiveChangedAllowsTheSwitchOnASeededOutletWithNoEmail(t *testing.T) {
	cur := &catalogv1.Outlet{Id: 1, Name: "Wuzz Pusat", Phone: "0211234567", Email: "", Active: true, Category: "all", DeliveryTypes: []string{"pickup"}, Timezone: "Asia/Jakarta", Hours: week()}
	// The switch re-sends the stored values with only `active` flipped.
	off := false
	body := outletBody{Name: "Wuzz Pusat", Phone: "0211234567", Active: &off}
	req := mergeOutletUpdate(1, cur, body)
	if !onlyActiveChanged(cur, req) {
		t.Fatal("a request that only flips active was treated as a full edit")
	}
	// Any other change is a full edit, and then the contact must be complete.
	body.Address = "Jl. Baru"
	req = mergeOutletUpdate(1, cur, body)
	if onlyActiveChanged(cur, req) {
		t.Fatal("a changed address was treated as the on/off switch")
	}
	if err := requireOutletContact(req); err == nil {
		t.Error("a full edit with no email was accepted")
	}
	body.Email = strp("pusat@wuzz.care")
	if err := requireOutletContact(mergeOutletUpdate(1, cur, body)); err != nil {
		t.Errorf("a full edit with phone, email and category was refused: %v", err)
	}
	body.Phone = ""
	if err := requireOutletContact(mergeOutletUpdate(1, cur, body)); err == nil {
		t.Error("a full edit that blanks the phone was accepted")
	}
}
