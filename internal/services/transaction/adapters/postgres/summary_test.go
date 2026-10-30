package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koobam/wuzz-backend/internal/services/transaction/app"
	"github.com/koobam/wuzz-backend/internal/services/transaction/app/query"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// seedOrder stores an order for a test customer and walks it to a status. It is removed afterwards.
func seedOrder(t *testing.T, s *Store, outlet int64, customer, status string, prices ...int64) domain.Order {
	t.Helper()
	lines := make([]domain.Line, 0, len(prices))
	for i, p := range prices {
		lines = append(lines, domain.Line{ItemID: 8, Name: fmt.Sprintf("Layanan %d", i+1), Unit: "kg", Price: p, Qty: 1})
	}
	o, err := domain.NewOrder(uuid.NewString(), outlet, customer, "", lines, "tester")
	if err != nil {
		t.Fatal(err)
	}
	stored, _, err := s.CreateOrder(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM outbox WHERE aggregate_id = $1`, stored.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, stored.ID)
	})
	path := map[string][]string{
		"created":    nil,
		"processing": {"processing"},
		"ready":      {"processing", "ready"},
		"fulfilled":  {"processing", "ready", "fulfilled"},
		"cancelled":  {"cancelled"},
	}[status]
	for _, st := range path {
		if _, err := s.UpdateStatus(context.Background(), stored.ID, st, "tester", nil); err != nil {
			t.Fatal(err)
		}
	}
	return stored
}

// summarize and list ask the read side the way the transport does.
func summarize(a *app.App, ctx context.Context, c domain.Caller, outlet int64, status, q, from, to string) (domain.Summary, error) {
	return a.Queries.SummarizeOrders.Handle(ctx, query.SummarizeOrders{Caller: c, OutletID: outlet, Status: status, Query: q, From: from, To: to})
}

func list(a *app.App, ctx context.Context, c domain.Caller, outlet int64, status, q, sortBy, sortDir, from, to string, limit, offset int) ([]domain.Order, int64, error) {
	res, err := a.Queries.ListOrders.Handle(ctx, query.ListOrders{Caller: c, OutletID: outlet, Status: status, Query: q,
		SortBy: sortBy, SortDir: sortDir, From: from, To: to, Limit: limit, Offset: offset})
	return res.Orders, res.Total, err
}

func tagOf(t *testing.T) string {
	return strings.ReplaceAll(fmt.Sprintf("Sm%d", time.Now().UnixNano()), " ", "")
}

// check asks both the summary and the list the same question and requires them to agree on how many orders there are.
func check(t *testing.T, a *app.App, label string, outlet int64, status, q, from, to string) domain.Summary {
	t.Helper()
	ctx, admin := context.Background(), domain.Caller{Admin: true, UserID: "a:1"}
	sum, err := summarize(a, ctx, admin, outlet, status, q, from, to)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	_, total, err := list(a, ctx, admin, outlet, status, q, "", "", from, to, 1, 0)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if sum.Orders != total {
		t.Errorf("%s: the summary counts %d orders but the list has %d", label, sum.Orders, total)
	}
	if sum.Fulfilled+sum.Cancelled+sum.Open != sum.Orders {
		t.Errorf("%s: %d fulfilled + %d cancelled + %d open != %d orders", label, sum.Fulfilled, sum.Cancelled, sum.Open, sum.Orders)
	}
	return sum
}

func TestSummaryByHand(t *testing.T) {
	s := testStore(t)
	a := app.New(s, nil)
	tag := tagOf(t)
	// A fulfilled order of Rp 2 (1 line), an order still created of Rp 3 (2 lines), and a cancelled one of Rp 100 (1 line). The cancelled
	// one is counted like the others: nothing is left out of a summary.
	seedOrder(t, s, 1, tag+" A", "fulfilled", 2)
	seedOrder(t, s, 1, tag+" B", "created", 1, 2)
	seedOrder(t, s, 2, tag+" X", "cancelled", 100)

	sum := check(t, a, "everything", 0, "", tag, "", "")
	if sum.Orders != 3 || sum.Fulfilled != 1 || sum.Cancelled != 1 || sum.Open != 1 || sum.Counted != 3 || sum.Value != 105 || sum.Lines != 4 {
		t.Fatalf("counts = %+v", sum.SummaryCounts)
	}
	// 105 / 3 = 35; 4 lines / 3 = 1.33; 1 of 3 is 33.3%.
	if sum.AvgValue != 35 || sum.AvgLines != "1.33" || sum.CancelledPercent != "33.3" || sum.FulfilledPercent != "33.3" {
		t.Errorf("derived = %d / %s / %s / %s; want 35 / 1.33 / 33.3 / 33.3", sum.AvgValue, sum.AvgLines, sum.CancelledPercent, sum.FulfilledPercent)
	}

	// The filters, each checked against the list.
	if got := check(t, a, "fulfilled only", 0, "fulfilled", tag, "", ""); got.Orders != 1 || got.Counted != 1 || got.Value != 2 || got.AvgValue != 2 || got.AvgLines != "1.00" || got.FulfilledPercent != "100.0" {
		t.Errorf("fulfilled only = %+v", got)
	}
	if got := check(t, a, "cancelled only", 0, "cancelled", tag, "", ""); got.Orders != 1 || got.Counted != 1 || got.Value != 100 || got.AvgValue != 100 || got.Lines != 1 || got.AvgLines != "1.00" || got.CancelledPercent != "100.0" {
		t.Errorf("cancelled only = %+v", got)
	}
	// Outlet 1 holds the 2 and the 3: 5 over 2 orders is 2.5, which rounds up to 3; 3 lines over 2 is 1.50.
	if got := check(t, a, "outlet 1", 1, "", tag, "", ""); got.Orders != 2 || got.Cancelled != 0 || got.Value != 5 || got.AvgValue != 3 || got.AvgLines != "1.50" {
		t.Errorf("outlet 1 = %+v", got)
	}
	if got := check(t, a, "outlet 2", 2, "", tag, "", ""); got.Orders != 1 || got.Counted != 1 || got.Value != 100 {
		t.Errorf("outlet 2 = %+v", got)
	}
	if got := check(t, a, "search by name", 0, "", tag+" B", "", ""); got.Orders != 1 || got.Open != 1 || got.Value != 3 || got.AvgValue != 3 {
		t.Errorf("search = %+v", got)
	}
	if got := check(t, a, "nothing matches", 0, "", tag+" nobody", "", ""); got.Orders != 0 || got.AvgValue != 0 || got.AvgLines != "0.00" || got.CancelledPercent != "0.0" || got.FulfilledPercent != "0.0" {
		t.Errorf("empty = %+v", got)
	}

	// Dates are Jakarta days: today holds all three; a day in the past or the future none.
	var today string
	_ = s.pool.QueryRow(context.Background(), `SELECT (now() AT TIME ZONE 'Asia/Jakarta')::date::text`).Scan(&today)
	for label, r := range map[string][2]string{"today": {today, today}, "open ended": {"2000-01-01", ""}, "up to today": {"", today}} {
		if got := check(t, a, label, 0, "", tag, r[0], r[1]); got.Orders != 3 {
			t.Errorf("%s: %d orders; want 3", label, got.Orders)
		}
	}
	for label, r := range map[string][2]string{"the past": {"2000-01-01", "2000-12-31"}, "the future": {"2999-01-01", ""}, "before today": {"", "2000-01-01"}} {
		if got := check(t, a, label, 0, "", tag, r[0], r[1]); got.Orders != 0 {
			t.Errorf("%s: %d orders; want 0", label, got.Orders)
		}
	}
	// The edge of the day is the Jakarta one: an order made just after 17:00 UTC is already tomorrow there.
	var edge string
	_ = s.pool.QueryRow(context.Background(), `SELECT ((now() AT TIME ZONE 'Asia/Jakarta')::date + 1)::text`).Scan(&edge)
	if got := check(t, a, "tomorrow", 0, "", tag, edge, edge); got.Orders != 0 {
		t.Errorf("tomorrow: %d orders", got.Orders)
	}
}

func TestSummaryRoundsAtTheSecondPlaceAndTheTenth(t *testing.T) {
	s := testStore(t)
	a := app.New(s, nil)
	tag := tagOf(t)
	// Three sales: a processing order of Rp 3 with 2 lines, and two fulfilled ones, Rp 3 with 2 lines and Rp 4 with 1 line.
	seedOrder(t, s, 1, tag+" P", "processing", 1, 2)
	seedOrder(t, s, 1, tag+" D1", "fulfilled", 1, 2)
	seedOrder(t, s, 1, tag+" D2", "fulfilled", 4)
	sum := check(t, a, "thirds", 0, "", tag, "", "")
	// 10 / 3 = 3.33 -> 3; 5 lines / 3 = 1.666.. -> 1.67; 2 of 3 fulfilled = 66.666.. -> 66.7; no cancelled -> 0.0.
	if sum.Counted != 3 || sum.Value != 10 || sum.Lines != 5 || sum.AvgValue != 3 || sum.AvgLines != "1.67" || sum.FulfilledPercent != "66.7" || sum.CancelledPercent != "0.0" || sum.Open != 1 {
		t.Errorf("summary = %+v / %d / %s / %s / %s", sum.SummaryCounts, sum.AvgValue, sum.AvgLines, sum.FulfilledPercent, sum.CancelledPercent)
	}
}

func TestSummaryRespectsWhoMaySeeWhat(t *testing.T) {
	s := testStore(t)
	a := app.New(s, nil)
	tag := tagOf(t)
	seedOrder(t, s, 1, tag+" one", "created", 5)
	seedOrder(t, s, 2, tag+" two", "created", 7)
	cashier := domain.Caller{Outlets: []int64{1}, UserID: "s:1"}
	got, err := summarize(a, context.Background(), cashier, 0, "", tag, "", "")
	if err != nil || got.Orders != 1 || got.Value != 5 {
		t.Errorf("a cashier's summary = %+v, %v; want just outlet 1", got.SummaryCounts, err)
	}
	if _, err := summarize(a, context.Background(), cashier, 2, "", tag, "", ""); err == nil {
		t.Error("another outlet is refused")
	}
	if _, err := summarize(a, context.Background(), domain.Caller{}, 0, "", "", "", ""); err == nil {
		t.Error("someone who is not staff is refused")
	}
}
