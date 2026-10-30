package domain

import (
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		name, in, want string
		ok             bool
	}{
		{"none", "", "", true},
		{"only spaces is none", "   ", "", true},
		{"plain", "pusat@wuzz.care", "pusat@wuzz.care", true},
		{"lower-cased", "Pusat@Wuzz.CARE", "pusat@wuzz.care", true},
		{"trimmed", "  pusat@wuzz.care\t", "pusat@wuzz.care", true},
		{"plus tag", "pusat+kasir@wuzz.care", "pusat+kasir@wuzz.care", true},
		{"subdomain", "a@mail.wuzz.co.id", "a@mail.wuzz.co.id", true},
		{"no at", "pusat.wuzz.care", "", false},
		{"no domain", "pusat@", "", false},
		{"no local part", "@wuzz.care", "", false},
		{"no dot in the domain", "pusat@localhost", "", false},
		{"domain ends with a dot", "pusat@wuzz.", "", false},
		{"domain starts with a dot", "pusat@.care", "", false},
		{"a display name", "Wuzz Pusat <pusat@wuzz.care>", "", false},
		{"angle brackets alone", "<pusat@wuzz.care>", "", false},
		{"a list", "a@wuzz.care, b@wuzz.care", "", false},
		{"a semicolon list", "a@wuzz.care;b@wuzz.care", "", false},
		{"an inner space", "pu sat@wuzz.care", "", false},
		{"two ats", "a@b@wuzz.care", "", false},
		{"a comment", "pusat@wuzz.care (kantor)", "", false},
		{"too long", strings.Repeat("a", 250) + "@wuzz.care", "", false},
		{"at the limit", strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 55), "", true},
	}
	for _, c := range cases {
		got, err := NormalizeEmail(c.in)
		if (err == nil) != c.ok {
			t.Errorf("%s: NormalizeEmail(%q) error = %v, want ok=%v", c.name, c.in, err, c.ok)
			continue
		}
		if c.ok && c.want != "" && got != c.want {
			t.Errorf("%s: NormalizeEmail(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
		if c.ok && c.want == "" && c.in != "" && strings.TrimSpace(c.in) == "" && got != "" {
			t.Errorf("%s: blank input gave %q", c.name, got)
		}
	}
}

// Validate stores the address normalised and refuses a bad one.
func TestOutletValidateEmail(t *testing.T) {
	base := Outlet{Name: "Wuzz Pusat", Latitude: -6.2, Longitude: 106.8, Category: "all", DeliveryTypes: []string{"pickup"}, OpenTime: "08:00", CloseTime: "22:00"}
	o := base
	o.Email = "  Pusat@Wuzz.care "
	if err := o.Validate(); err != nil || o.Email != "pusat@wuzz.care" {
		t.Errorf("Validate() = %v, email %q; want it stored as pusat@wuzz.care", err, o.Email)
	}
	bad := base
	bad.Email = "not an email"
	if bad.Validate() == nil {
		t.Error("a malformed email should be refused")
	}
	none := base
	if err := none.Validate(); err != nil || none.Email != "" {
		t.Errorf("an outlet without an email: %v, %q", err, none.Email)
	}
}

func TestOutletRequireContact(t *testing.T) {
	ok := Outlet{Phone: "+62 21 1234-567", Email: "pusat@wuzz.care", Category: "all"}
	if err := ok.RequireContact(); err != nil {
		t.Fatalf("a complete contact was refused: %v", err)
	}
	for name, mutate := range map[string]func(*Outlet){
		"empty phone":      func(o *Outlet) { o.Phone = "" },
		"short phone":      func(o *Outlet) { o.Phone = "12345" },
		"long phone":       func(o *Outlet) { o.Phone = "123456789012345678901" },
		"letters in phone": func(o *Outlet) { o.Phone = "0812-abcd-123" },
		"empty email":      func(o *Outlet) { o.Email = " " },
		"display name":     func(o *Outlet) { o.Email = "Wuzz <a@b.co>" },
		"empty category":   func(o *Outlet) { o.Category = "" },
	} {
		o := ok
		mutate(&o)
		if err := o.RequireContact(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
