package domain

import "sort"

// TaxRates are the rate of each tax type in basis points (1100 = 11%). "none" is always 0.
type TaxRates map[string]int32

// Rate is the rate of a tax type; an unknown type, or "none", is 0.
func (r TaxRates) Rate(taxType string) int32 { return r[taxType] }

// Types lists the types in a stable order (none first), for the screens that offer them.
func (r TaxRates) Types() []string {
	out := make([]string, 0, len(r))
	for t := range r {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == TaxNone) != (out[j] == TaxNone) {
			return out[i] == TaxNone
		}
		return out[i] < out[j]
	})
	return out
}

// AddTax is the price after tax of a whole-rupiah price before tax: price × (10000 + bps) / 10000, rounded ONCE, half away from
// zero. Done in integers, so there is no float to be wrong by a rupiah. A price before tax cannot be negative.
func AddTax(price int64, bps int32) int64 {
	if price <= 0 || bps <= 0 {
		return price
	}
	return price + roundDiv(price*int64(bps), 10000)
}

// ApplyPricing fills what is derived from the stored price, discount and tax type, in this order: the discount comes off the
// list price (Price), then tax is added to what is left. Each step rounds once, half away from zero, in integers:
//
//	DiscountAmount     = value, or round(Price × bps / 10000)
//	PriceAfterDiscount = Price − DiscountAmount        (the tax base)
//	PriceAfterTax      = PriceAfterDiscount + round(PriceAfterDiscount × rate / 10000)
func (s *ServiceItem) ApplyPricing(rates TaxRates) {
	s.DiscountAmount = DiscountAmountOf(s.Price, s.DiscountType, s.DiscountValue)
	s.PriceAfterDiscount = s.Price - s.DiscountAmount
	s.TaxRateBps = rates.Rate(s.TaxType)
	s.PriceAfterTax = AddTax(s.PriceAfterDiscount, s.TaxRateBps)
}
