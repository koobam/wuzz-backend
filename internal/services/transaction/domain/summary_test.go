package domain

import "testing"

func TestSummaryArithmeticIsExactAndRoundsOnce(t *testing.T) {
	cases := []struct {
		name string
		in   SummaryCounts
		want Summary
	}{
		{"nothing", SummaryCounts{}, Summary{AvgValue: 0, AvgLines: "0.00", CancelledPercent: "0.0", FulfilledPercent: "0.0"}},
		// 2 orders worth 5 is 2.5 each, which rounds up to 3; 3 lines over 2 is 1.50; one of two is 50.0%.
		{"2.5 rounds up", SummaryCounts{Orders: 2, Fulfilled: 1, Cancelled: 1, Value: 5, Lines: 3}, Summary{AvgValue: 3, AvgLines: "1.50", CancelledPercent: "50.0", FulfilledPercent: "50.0"}},
		// A cancelled order is counted like any other: 3 orders of 2, 100 (cancelled) and 3 are worth 105, 35 each, 4 lines is 1.33 each.
		{"cancelled counts", SummaryCounts{Orders: 3, Fulfilled: 1, Cancelled: 1, Open: 1, Value: 105, Lines: 4}, Summary{AvgValue: 35, AvgLines: "1.33", CancelledPercent: "33.3", FulfilledPercent: "33.3"}},
		// 10 over 3 is 3.33 -> 3; 5 lines over 3 is 1.666.. -> 1.67; 2 of 3 is 66.666.. -> 66.7.
		{"thirds", SummaryCounts{Orders: 3, Fulfilled: 2, Value: 10, Lines: 5}, Summary{AvgValue: 3, AvgLines: "1.67", CancelledPercent: "0.0", FulfilledPercent: "66.7"}},
		// 7 over 8 is 0.875 -> 1; 1 of 8 is 12.5%, exactly representable.
		{"eighths", SummaryCounts{Orders: 8, Cancelled: 1, Value: 7, Lines: 7}, Summary{AvgValue: 1, AvgLines: "0.88", CancelledPercent: "12.5", FulfilledPercent: "0.0"}},
		// 1 of 16 is 6.25 -> half away from zero at one place -> 6.3; 100000 over 16 is 6250; 20 lines over 16 is 1.25.
		{"sixteenth", SummaryCounts{Orders: 16, Cancelled: 1, Value: 100000, Lines: 20}, Summary{AvgValue: 6250, AvgLines: "1.25", CancelledPercent: "6.3", FulfilledPercent: "0.0"}},
		// Only cancelled orders: their value is the value.
		{"all cancelled", SummaryCounts{Orders: 4, Cancelled: 4, Value: 40000, Lines: 6}, Summary{AvgValue: 10000, AvgLines: "1.50", CancelledPercent: "100.0", FulfilledPercent: "0.0"}},
		// 201 lines over 200 orders is 1.005 -> 1.01 (half away), where a float would say 1.00.
		{"1.005", SummaryCounts{Orders: 200, Fulfilled: 200, Value: 200, Lines: 201}, Summary{AvgValue: 1, AvgLines: "1.01", CancelledPercent: "0.0", FulfilledPercent: "100.0"}},
	}
	for _, c := range cases {
		got := NewSummary(c.in)
		if got.Counted != c.in.Orders {
			t.Errorf("%s: counted %d; want it to equal orders (%d)", c.name, got.Counted, c.in.Orders)
		}
		if got.AvgValue != c.want.AvgValue || got.AvgLines != c.want.AvgLines || got.CancelledPercent != c.want.CancelledPercent || got.FulfilledPercent != c.want.FulfilledPercent {
			t.Errorf("%s: got %d / %s / %s / %s; want %d / %s / %s / %s", c.name, got.AvgValue, got.AvgLines, got.CancelledPercent, got.FulfilledPercent,
				c.want.AvgValue, c.want.AvgLines, c.want.CancelledPercent, c.want.FulfilledPercent)
		}
	}
}
