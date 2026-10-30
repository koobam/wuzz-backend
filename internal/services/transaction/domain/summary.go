package domain

import "fmt"

// SummaryCounts is what the store counts for a filter; everything else of a Summary is worked out from it, exactly.
type SummaryCounts struct {
	Orders, Fulfilled, Cancelled, Open, Counted int64
	// Value is the sum of total, and Lines the number of order lines, over every matching order. Counted equals Orders: nothing is left out.
	Value, Lines int64
}

// Summary is the totals the Orders page shows over a filter; it follows the filter exactly, and cancelled orders are not special. Money is whole rupiah. Every division is done on integers and rounded
// once, half away from zero; there is no float anywhere.
type Summary struct {
	SummaryCounts
	AvgValue         int64
	AvgLines         string // 2 decimal places
	CancelledPercent string // 1 decimal place
	FulfilledPercent string
}

// roundDiv is n / d rounded half away from zero (for n >= 0, d > 0): (2n + d) / 2d.
func roundDiv(n, d int64) int64 { return (2*n + d) / (2 * d) }

// decimal prints n / d x scale as a decimal with places digits after the point, rounded once half away from zero; "0.00" for d = 0.
func decimal(n, d int64, factor int64, places int) string {
	var v int64
	if d != 0 {
		v = roundDiv(n*factor, d)
	}
	unit := int64(1)
	for i := 0; i < places; i++ {
		unit *= 10
	}
	return fmt.Sprintf("%d.%0*d", v/unit, places, v%unit)
}

// NewSummary works out the averages and percentages of a set of counts.
func NewSummary(c SummaryCounts) Summary {
	c.Counted = c.Orders // kept as its own field for compatibility: every matching order is counted
	s := Summary{SummaryCounts: c}
	if c.Orders > 0 {
		s.AvgValue = roundDiv(c.Value, c.Orders)
	}
	// lines / orders to 2 places is lines x 100 / orders; a percentage to 1 place is count x 1000 / orders.
	s.AvgLines = decimal(c.Lines, c.Orders, 100, 2)
	s.CancelledPercent = decimal(c.Cancelled, c.Orders, 1000, 1)
	s.FulfilledPercent = decimal(c.Fulfilled, c.Orders, 1000, 1)
	return s
}
