package domain

import "github.com/koobam/wuzz-backend/internal/pkg/apperr"

// How a discount is read: not at all, as whole rupiah off the price, or as a percentage of it.
const (
	DiscountNone    = "none"
	DiscountValue   = "value"
	DiscountPercent = "percent"
)

// MaxDiscountBps is 100%, the most a percentage discount can be (in basis points).
const MaxDiscountBps = 10000

func validDiscountType(t string) bool {
	return t == DiscountNone || t == DiscountValue || t == DiscountPercent
}

// ValidateDiscount checks a discount against the list price it comes off. "none" carries no value (a value that is left over
// from another type is refused rather than ignored, so a form cannot save a number it does not mean); a value discount is
// whole rupiah, never more than the price; a percentage is in basis points, 0 to 10000.
func ValidateDiscount(discountType string, value, price int64) error {
	switch {
	case !validDiscountType(discountType):
		return apperr.Invalid("discount_type must be none, value or percent")
	case value < 0:
		return apperr.Invalid("discount_value cannot be negative")
	case discountType == DiscountNone && value != 0:
		return apperr.Invalid("discount_value must be 0 when discount_type is none")
	case discountType == DiscountPercent && value > MaxDiscountBps:
		return apperr.Invalid("a percentage discount is at most 100% (discount_value 10000)")
	case discountType == DiscountValue && value > price:
		return apperr.Invalid("the discount cannot be more than the price")
	}
	return nil
}

// roundDiv is n / d (d > 0) rounded ONCE, half away from zero, in integers.
func roundDiv(n, d int64) int64 {
	if n < 0 {
		return -roundDiv(-n, d)
	}
	q, r := n/d, n%d
	if r*2 >= d {
		q++
	}
	return q
}

// DiscountAmountOf is how much a discount takes off a list price, in whole rupiah: the value itself, or the percentage of the
// price rounded once, half away from zero. It never takes off more than the price.
func DiscountAmountOf(price int64, discountType string, value int64) int64 {
	var amount int64
	switch discountType {
	case DiscountValue:
		amount = value
	case DiscountPercent:
		amount = roundDiv(price*value, MaxDiscountBps)
	}
	if amount > price {
		amount = price
	}
	if amount < 0 {
		amount = 0
	}
	return amount
}
