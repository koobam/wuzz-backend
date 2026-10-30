package domain

import (
	"strings"
	"testing"
	"time"
)

func TestValidCode(t *testing.T) {
	for _, ok := range []string{"TRX-25102026-K7M2", "TRX-01012000-AAAA", "TRX-29022028-ZZ99", "TRX-31122026-2345"} {
		if !ValidCode(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for name, bad := range map[string]string{
		"empty":                "",
		"lowercase":            "trx-25102026-k7m2",
		"lowercase suffix":     "TRX-25102026-k7m2",
		"letter O":             "TRX-25102026-O7M2",
		"letter I":             "TRX-25102026-I7M2",
		"digit 0":              "TRX-25102026-K0M2",
		"digit 1":              "TRX-25102026-K1M2",
		"impossible date":      "TRX-31022026-K7M2",
		"month 13":             "TRX-25132026-K7M2",
		"day 00":               "TRX-00102026-K7M2",
		"not a leap year":      "TRX-29022027-K7M2",
		"short date":           "TRX-2510206-K7M2",
		"short suffix":         "TRX-25102026-K7M",
		"long suffix":          "TRX-25102026-K7M2A",
		"wrong prefix":         "ORD-25102026-K7M2",
		"spaces":               " TRX-25102026-K7M2",
		"trailing newline":     "TRX-25102026-K7M2\n",
		"no dashes":            "TRX25102026K7M2",
		"dash instead of date": "TRX-DDMMYYYY-K7M2",
	} {
		if ValidCode(bad) {
			t.Errorf("%s: %q should be invalid", name, bad)
		}
	}
}

func TestNewCodeShape(t *testing.T) {
	// 23:30 UTC on 24 October is already 06:30 on the 25th in Jakarta.
	created := time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		c := NewCode(created)
		if !ValidCode(c) || !strings.HasPrefix(c, "TRX-25102026-") {
			t.Fatalf("code %q: want a valid one dated 25102026 (the Jakarta day)", c)
		}
		for _, r := range c[len("TRX-25102026-"):] {
			if !strings.ContainsRune(CodeAlphabet, r) {
				t.Fatalf("%q uses %q outside the alphabet", c, r)
			}
		}
		seen[c] = true
	}
	if len(seen) < 450 { // 32^4 is about a million, so 500 draws almost never repeat
		t.Errorf("only %d distinct codes in 500 draws", len(seen))
	}
	if len(CodeAlphabet) != 32 || strings.ContainsAny(CodeAlphabet, "01OI") {
		t.Errorf("alphabet = %q", CodeAlphabet)
	}
	// And just before midnight in Jakarta is still the same day there.
	if c := NewCode(time.Date(2026, 10, 25, 16, 59, 0, 0, time.UTC)); !strings.HasPrefix(c, "TRX-25102026-") {
		t.Errorf("16:59 UTC is 23:59 in Jakarta: %s", c)
	}
	if c := NewCode(time.Date(2026, 10, 25, 17, 0, 0, 0, time.UTC)); !strings.HasPrefix(c, "TRX-26102026-") {
		t.Errorf("17:00 UTC is midnight in Jakarta: %s", c)
	}
}
