package domain

import "testing"

func TestServiceTaxTypeValidated(t *testing.T) {
	base := ServiceItem{Name: "Cuci Kiloan", Unit: UnitKg, Price: 7000, DiscountType: DiscountNone}
	for _, tc := range []struct {
		tax     string
		wantErr bool
	}{
		{TaxNone, false},
		{TaxPPN, false},
		{"", true}, // never guessed: the create command fills "none", an update must say what it keeps
		{"pb1", true},
		{"PPN", true},
		{" ppn", true},
	} {
		s := base
		s.TaxType = tc.tax
		if err := s.Validate(); (err != nil) != tc.wantErr {
			t.Errorf("tax_type %q: err=%v, wantErr=%v", tc.tax, err, tc.wantErr)
		}
	}
}
