package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewOrderComputesTotalAndRejectsBadInput(t *testing.T) {
	id := uuid.NewString()
	o, err := NewOrder(id, 1, " Ani ", "", []Line{{ItemID: 1, Name: "Cuci", Unit: "kg", Price: 8000, Qty: 2.5}, {ItemID: 2, Name: "Bedcover", Unit: "pcs", Price: 30000, Qty: 1}}, "9")
	if err != nil {
		t.Fatal(err)
	}
	if o.Total != 50000 || o.CustomerName != "Ani" || o.Status != StatusCreated {
		t.Fatalf("unexpected order %+v", o)
	}

	bad := map[string]func() error{
		"not a uuid":     func() error { _, e := NewOrder("x", 1, "A", "", []Line{{Name: "a", Price: 1, Qty: 1}}, "9"); return e },
		"no outlet":      func() error { _, e := NewOrder(id, 0, "A", "", []Line{{Name: "a", Price: 1, Qty: 1}}, "9"); return e },
		"no customer":    func() error { _, e := NewOrder(id, 1, " ", "", []Line{{Name: "a", Price: 1, Qty: 1}}, "9"); return e },
		"no lines":       func() error { _, e := NewOrder(id, 1, "A", "", nil, "9"); return e },
		"zero qty":       func() error { _, e := NewOrder(id, 1, "A", "", []Line{{Name: "a", Price: 1, Qty: 0}}, "9"); return e },
		"negative price": func() error { _, e := NewOrder(id, 1, "A", "", []Line{{Name: "a", Price: -1, Qty: 1}}, "9"); return e },
		"huge qty":       func() error { _, e := NewOrder(id, 1, "A", "", []Line{{Name: "a", Price: 1, Qty: 1e6}}, "9"); return e },
	}
	for name, f := range bad {
		if f() == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestStatusLifecycle(t *testing.T) {
	ok := [][2]string{{StatusCreated, StatusProcessing}, {StatusProcessing, StatusReady}, {StatusReady, StatusFulfilled}, {StatusCreated, StatusCancelled}, {StatusProcessing, StatusCancelled}}
	for _, p := range ok {
		if !CanMoveTo(p[0], p[1]) {
			t.Errorf("%s -> %s should be allowed", p[0], p[1])
		}
	}
	no := [][2]string{{StatusCreated, StatusReady}, {StatusReady, StatusCancelled}, {StatusFulfilled, StatusProcessing}, {StatusCancelled, StatusCreated}}
	for _, p := range no {
		if CanMoveTo(p[0], p[1]) {
			t.Errorf("%s -> %s should be refused", p[0], p[1])
		}
	}
}
