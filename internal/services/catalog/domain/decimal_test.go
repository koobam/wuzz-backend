package domain

import (
	"math/big"
	"strings"
	"testing"
)

func TestParseDecimalValid(t *testing.T) {
	for in, want := range map[string]string{"0.5": "1/2", "20": "20", "12.3400": "617/50", "0": "0", "0.0001": "1/10000", "99999999999999.9999": "999999999999999999/10000"} {
		got, err := ParseDecimal("x", in, 4)
		if err != nil || got.String() != want && got.RatString() != want {
			t.Errorf("%q = %v, %v; want %s", in, got, err, want)
		}
	}
}

func TestParseDecimalInvalid(t *testing.T) {
	for _, in := range []string{"1e3", "1E3", "1,5", "-1", "+1", "", " 5", "5 ", ".5", "5.", "1.23456", "abc", "0x10", "1_000", "NaN", "Inf", strings.Repeat("9", 15), "1.2.3"} {
		if _, err := ParseDecimal("The quantity", in, 4); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
	if _, err := ParseDecimal("The loss", "12.345", 2); err == nil || !strings.Contains(err.Error(), "at most 2 decimal places") {
		t.Errorf("too many places: %v", err)
	}
}

func TestRoundHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		n, d int64
		want int64
	}{{1, 3, 0}, {2, 3, 1}, {1, 2, 1}, {-1, 2, -1}, {5, 2, 3}, {-5, 2, -3}, {3, 2, 2}, {-3, 2, -2}, {499, 1000, 0}, {-499, 1000, 0}, {0, 1, 0}, {7, 1, 7}}
	for _, c := range cases {
		if got := RoundRupiah(big.NewRat(c.n, c.d)); got != c.want {
			t.Errorf("round(%d/%d) = %d; want %d", c.n, c.d, got, c.want)
		}
	}
}

func TestFormatRat(t *testing.T) {
	cases := []struct {
		r    *big.Rat
		dp   int
		want string
	}{
		{big.NewRat(100, 3), 6, "33.333333"},
		{big.NewRat(2, 3), 6, "0.666667"},
		{big.NewRat(1, 3), 6, "0.333333"},
		{big.NewRat(1, 2), 0, "1"},
		{big.NewRat(-1, 2), 0, "-1"},
		{big.NewRat(5, 1000), 2, "0.01"},   // 0.005 -> half away
		{big.NewRat(-5, 1000), 2, "-0.01"}, // and away on the negative side
		{big.NewRat(4, 1000), 2, "0.00"},
		{big.NewRat(-4, 1000), 2, "0.00"}, // never "-0.00"
		{big.NewRat(0, 1), 6, "0.000000"},
		{big.NewRat(12345, 100), 2, "123.45"},
		{big.NewRat(7, 1), 6, "7.000000"},
	}
	for _, c := range cases {
		if got := FormatRat(c.r, c.dp); got != c.want {
			t.Errorf("FormatRat(%v, %d) = %q; want %q", c.r, c.dp, got, c.want)
		}
	}
}
