package domain

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Exact arithmetic for quantities, percentages and costs.
//
// Money is whole rupiah (int64). Quantities and percentages are decimals that arrive as JSON strings and are handled
// as exact rationals (math/big.Rat), never as floats: 0.1 + 0.2 is 3/10 here. A material's unit cost is the rational
// purchase_price / purchase_qty (Rp 100.000 for 3.000 ml is 100/3 per ml) and is never rounded, stored or cached as a
// decimal; it is derived each time it is needed. A costing adds up the exact rationals of every line and rounds once, at
// the end, to whole rupiah, half away from zero. The decimal strings shown beside a line (unit cost, line cost) are
// rounded half away from zero to 6 places for display only; no total or margin is ever computed from them.

var decimalRE = regexp.MustCompile(`^[0-9]{1,14}(\.[0-9]+)?$`)

// ParseDecimal reads a plain decimal string ("0.5", "20", "12.3400"): digits and at most one point, no sign, no exponent,
// no comma, no spaces. More than maxDP decimal places is refused rather than rounded. The integer part is limited to 14
// digits, which is what the numeric(18,4) columns hold.
func ParseDecimal(label, s string, maxDP int) (*big.Rat, error) {
	if s == "" {
		return nil, apperr.Invalid(fmt.Sprintf("%s is required", label))
	}
	if !decimalRE.MatchString(s) {
		return nil, apperr.Invalid(fmt.Sprintf("%s must be a plain decimal such as \"0.5\" or \"20\" (digits and one point; no sign, comma or exponent)", label))
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 > maxDP {
		return nil, apperr.Invalid(fmt.Sprintf("%s can have at most %d decimal places", label, maxDP))
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, apperr.Invalid(fmt.Sprintf("%s is not a number", label))
	}
	return r, nil
}

var (
	bigTwo = big.NewInt(2)
	bigTen = big.NewInt(10)
)

// scaledRound is round(|r| x 10^dp) with halves going up, as an integer: half away from zero on the absolute value.
func scaledRound(abs *big.Rat, dp int) *big.Int {
	scale := new(big.Int).Exp(bigTen, big.NewInt(int64(dp)), nil)
	n := new(big.Int).Mul(abs.Num(), scale)
	d := abs.Denom()
	// floor((2n + d) / 2d)
	n.Mul(n, bigTwo)
	n.Add(n, d)
	return n.Quo(n, new(big.Int).Mul(d, bigTwo))
}

// RoundRat rounds to a whole number, half away from zero (2.5 -> 3, -2.5 -> -3).
func RoundRat(r *big.Rat) *big.Int {
	q := scaledRound(new(big.Rat).Abs(r), 0)
	if r.Sign() < 0 {
		q.Neg(q)
	}
	return q
}

// RoundRupiah is RoundRat as an int64 of whole rupiah.
func RoundRupiah(r *big.Rat) int64 { return RoundRat(r).Int64() }

// FormatRat prints r rounded half away from zero to dp decimal places, always with exactly dp places ("0.333333").
// It is for display; do not parse it back to compute with.
func FormatRat(r *big.Rat, dp int) string {
	q := scaledRound(new(big.Rat).Abs(r), dp)
	digits := q.String()
	if dp > 0 {
		for len(digits) <= dp {
			digits = "0" + digits
		}
		digits = digits[:len(digits)-dp] + "." + digits[len(digits)-dp:]
	}
	if r.Sign() < 0 && q.Sign() != 0 {
		digits = "-" + digits
	}
	return digits
}

// FormatExact prints a value that is known to have at most maxDP decimal places as the shortest plain decimal ("0.5", "20",
// "12.34"): nothing is rounded for display, trailing zeros are just not written.
func FormatExact(r *big.Rat, maxDP int) string {
	s := FormatRat(r, maxDP)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return s
}
