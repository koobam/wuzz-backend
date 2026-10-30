package domain

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

func TestCanReadAnswersNotFoundForAnotherOutlet(t *testing.T) {
	cashier := Caller{Outlets: []int64{1}}
	if err := cashier.CanRead(Order{OutletID: 1}); err != nil {
		t.Errorf("their own outlet: %v", err)
	}
	err := cashier.CanRead(Order{OutletID: 2})
	if err == nil || err.Error() == "" {
		t.Fatal("another outlet's order was readable")
	}
	// Not 403: do not confirm that the order exists.
	if got := err.Error(); got != "Order not found" {
		t.Errorf("error = %q; want the same answer as a missing order", got)
	}
}
