package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
	pgplat "github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// These need a transaction database with the migrations applied: set TRANSACTION_TEST_DSN. They are skipped otherwise.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TRANSACTION_TEST_DSN")
	if dsn == "" {
		t.Skip("TRANSACTION_TEST_DSN not set")
	}
	pool, err := pgplat.New(context.Background(), config.Postgres{DSN: dsn, MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool)
}

func newOrder(t *testing.T, s *Store) domain.Order {
	t.Helper()
	o, err := domain.NewOrder(uuid.NewString(), 1, "Tes pesanan", "", []domain.Line{{ItemID: 8, Name: "Kiloan", Unit: "kg", Price: 7000, Qty: 2.5, VariantOptionIDs: []int64{2}, ModifierOptionIDs: []int64{1, 3}}}, "tester")
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
	return stored
}

func TestOrderLinesKeepTheirOptionIDs(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	o := newOrder(t, s)
	got, err := s.GetOrder(ctx, o.ID)
	if err != nil || len(got.Lines) != 1 || len(got.Lines[0].VariantOptionIDs) != 1 || got.Lines[0].VariantOptionIDs[0] != 2 || len(got.Lines[0].ModifierOptionIDs) != 2 {
		t.Errorf("stored lines = %+v, %v", got.Lines, err)
	}
}

// The stock is taken out together with the status change, inside its transaction: when the effect fails the status stays as it was.
func TestAStatusChangeRunsItsEffectInsideTheTransaction(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	o := newOrder(t, s)

	var calls []string
	record := func(ctx context.Context, before, after domain.Order) error {
		calls = append(calls, before.Status+">"+after.Status)
		return nil
	}

	// A failing effect: the caller gets its error and the order has not moved.
	boom := errors.New("catalog is down")
	if _, err := s.UpdateStatus(ctx, o.ID, domain.StatusProcessing, "s:3", func(context.Context, domain.Order, domain.Order) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v; want the effect's own error", err)
	}
	got, _ := s.GetOrder(ctx, o.ID)
	if got.Status != domain.StatusCreated || got.Version != o.Version {
		t.Fatalf("after a failed effect the order is %s v%d; want it untouched (%s v%d)", got.Status, got.Version, o.Status, o.Version)
	}
	var events int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE aggregate_id = $1 AND type = 'order.status_changed'`, o.ID).Scan(&events)
	if events != 0 {
		t.Errorf("a status change that was rolled back still published %d event(s)", events)
	}

	// A working effect sees the order before and after, once, and the change commits.
	if _, err := s.UpdateStatus(ctx, o.ID, domain.StatusProcessing, "s:3", record); err != nil {
		t.Fatal(err)
	}
	// A retried tap on the same status is not a change: no effect, no new version.
	again, err := s.UpdateStatus(ctx, o.ID, domain.StatusProcessing, "s:3", record)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "created>processing" {
		t.Errorf("effect calls = %v; want exactly [created>processing]", calls)
	}
	if again.Status != domain.StatusProcessing || again.Version != o.Version+1 {
		t.Errorf("after the retry the order is %s v%d", again.Status, again.Version)
	}

	// A move that is not allowed never reaches the effect.
	if _, err := s.UpdateStatus(ctx, o.ID, domain.StatusCreated, "s:3", record); err == nil || len(calls) != 1 {
		t.Errorf("an impossible move: err %v, effect calls %v", err, calls)
	}
}
