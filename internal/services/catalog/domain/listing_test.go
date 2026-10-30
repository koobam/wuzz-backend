package domain

import "testing"

func TestOutletQueryValidate(t *testing.T) {
	q := OutletQuery{Q: "  bsd ", Limit: 500, Offset: -3}
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	if q.Q != "bsd" || q.SortBy != "id" || q.SortDir != "asc" || q.Limit != 100 || q.Offset != 0 {
		t.Fatalf("defaults = %+v", q)
	}
	for _, bad := range []OutletQuery{{SortBy: "password"}, {SortDir: "sideways"}, {Active: "maybe"}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v should be refused", bad)
		}
	}
}

func TestServiceQueryValidate(t *testing.T) {
	ok := ServiceQuery{Unit: "kg", Active: "false", SortBy: "price", CategoryID: 2, TaxType: TaxPPN}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ServiceQuery{{Unit: "liter"}, {CategoryID: -1}, {SortBy: "icon"}, {Active: "x"}, {TaxType: "vat"}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v should be refused", bad)
		}
	}
}
