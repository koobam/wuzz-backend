package domain

import "testing"

func TestValidatePin(t *testing.T) {
	cases := []struct {
		pin string
		ok  bool
	}{
		{"135790", true},
		{"482916", true},
		{"123450", true},
		{"000000", false},
		{"777777", false},
		{"123456", false},
		{"654321", false},
		{"345678", false},
		{"12345", false},
		{"1234567", false},
		{"", false},
		{"12a456", false},
		{"12 456", false},
	}
	for _, c := range cases {
		err := ValidatePin(c.pin)
		if (err == nil) != c.ok {
			t.Errorf("ValidatePin(%q) = %v, want ok=%v", c.pin, err, c.ok)
		}
	}
}
