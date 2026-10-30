package domain

import "testing"

func TestStockActionFor(t *testing.T) {
	for name, c := range map[string]struct{ from, to, want string }{
		"starting work takes the materials out":      {StatusCreated, StatusProcessing, StockConsume},
		"cancelling after that gives them back":      {StatusProcessing, StatusCancelled, StockReverse},
		"cancelled before any work: nothing to give": {StatusCreated, StatusCancelled, ""},
		"ready leaves the stock alone":               {StatusProcessing, StatusReady, ""},
		"fulfilled leaves the stock alone":           {StatusReady, StatusFulfilled, ""},
		"staying put is not a change":                {StatusProcessing, StatusProcessing, StockConsume},
	} {
		if got := StockActionFor(c.from, c.to); got != c.want {
			t.Errorf("%s: %s -> %s = %q; want %q", name, c.from, c.to, got, c.want)
		}
	}
}
