package domain

import (
	"strings"
	"testing"
)

// The tax is added once, in integers, rounded half away from zero: nothing here may be off by a rupiah.
func TestAddTax(t *testing.T) {
	for _, tc := range []struct {
		name  string
		price int64
		bps   int32
		want  int64
	}{
		{"no tax", 10000, 0, 10000},
		{"ppn 11% of 10.000", 10000, 1100, 11100},
		{"free stays free", 0, 1100, 0},
		{"exact half rounds up: 50 × 11% = 5.5", 50, 1100, 56},
		{"just under a half: 45 × 11% = 4.95", 45, 1100, 50},
		{"just over a half: 4.545 × 11% = 499.95", 4545, 1100, 5045},
		{"below a half: 4 × 11% = 0.44", 4, 1100, 4},
		{"one rupiah × 11% = 0.11", 1, 1100, 1},
		{"large price", 1_000_000_000, 1100, 1_110_000_000},
		{"a full 100% rate", 7500, 10000, 15000},
		{"odd rate: 12.345 × 2.5% = 308.625", 12345, 250, 12654},
	} {
		if got := AddTax(tc.price, tc.bps); got != tc.want {
			t.Errorf("%s: AddTax(%d, %d) = %d, want %d", tc.name, tc.price, tc.bps, got, tc.want)
		}
	}
}

func TestApplyTaxFillsDerivedFields(t *testing.T) {
	rates := TaxRates{TaxNone: 0, TaxPPN: 1100}
	s := ServiceItem{Price: 10000, TaxType: TaxPPN}
	s.ApplyPricing(rates)
	if s.TaxRateBps != 1100 || s.PriceAfterTax != 11100 || s.Price != 10000 {
		t.Errorf("ppn: %+v", s)
	}
	n := ServiceItem{Price: 10000, TaxType: TaxNone}
	n.ApplyPricing(rates)
	if n.TaxRateBps != 0 || n.PriceAfterTax != 10000 {
		t.Errorf("none: %+v", n)
	}
}

func TestTaxRatesTypesOrder(t *testing.T) {
	got := TaxRates{TaxPPN: 1100, TaxNone: 0}.Types()
	if len(got) != 2 || got[0] != TaxNone || got[1] != TaxPPN {
		t.Errorf("types = %v", got)
	}
}

func TestServiceInternalNotes(t *testing.T) {
	ok := ServiceItem{Name: "S", Unit: UnitKg, TaxType: TaxNone, DiscountType: DiscountNone, InternalNotes: "  for hotels  "}
	if err := ok.Validate(); err != nil || ok.InternalNotes != "for hotels" {
		t.Errorf("trimmed note: err=%v note=%q", err, ok.InternalNotes)
	}
	long := ServiceItem{Name: "S", Unit: UnitKg, TaxType: TaxNone, DiscountType: DiscountNone, InternalNotes: strings.Repeat("x", 501)}
	if err := long.Validate(); err == nil {
		t.Error("a 501-character note should be refused")
	}
	max := ServiceItem{Name: "S", Unit: UnitKg, TaxType: TaxNone, DiscountType: DiscountNone, InternalNotes: strings.Repeat("x", 500)}
	if err := max.Validate(); err != nil {
		t.Errorf("500 characters is the limit: %v", err)
	}
}
