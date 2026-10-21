package app

import "testing"

func TestCallerScoping(t *testing.T) {
	cashier := Caller{Outlets: []int64{1, 3}}
	admin := Caller{Admin: true}
	customer := Caller{}

	if !cashier.MayUse(1) || !cashier.MayUse(3) || cashier.MayUse(2) {
		t.Error("a cashier may use exactly their own outlets")
	}
	if !admin.MayUse(99) {
		t.Error("an admin may use every outlet")
	}
	if customer.IsStaff() || customer.MayUse(1) {
		t.Error("a caller with no roles is not staff")
	}
	if !cashier.IsStaff() || !admin.IsStaff() {
		t.Error("cashiers and admins are staff")
	}
}
