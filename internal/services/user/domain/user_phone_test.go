package domain

import "testing"

func TestNormalisePhone(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		// As before, with no plus: Indonesian.
		{"081234567890", "6281234567890", true},
		{"0812-3456-7890", "6281234567890", true},
		{"6281234567890", "6281234567890", true},
		{"81234567890", "6281234567890", true},
		// With a plus: international, complete as written.
		{"+6281234567890", "6281234567890", true},
		{"+62 812 3456 7890", "6281234567890", true},
		{"+15551234567", "15551234567", true},
		{"+1 (555) 123-4567", "15551234567", true},
		{"+60123456789", "60123456789", true},
		{"+442071234567", "442071234567", true},
		// Not a number.
		{"+0812345678", "", false},
		{"+123", "", false},
		{"+1234567890123456", "", false},
		{"0812abc", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := NormalisePhone(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("NormalisePhone(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("NormalisePhone(%q) = %q; want an error", c.in, got)
		}
	}
}
