package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/transaction/app"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

func orderWith(t *testing.T, id, customer, code string) domain.Order {
	t.Helper()
	o, err := domain.NewOrder(id, 1, customer, "", []domain.Line{{ItemID: 8, Name: "Kiloan", Unit: "kg", Price: 7000, Qty: 1}}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	o.Code = code
	return o
}

func store(t *testing.T, s *Store, o domain.Order) (domain.Order, bool) {
	t.Helper()
	stored, created, err := s.CreateOrder(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM outbox WHERE aggregate_id = $1`, stored.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, stored.ID)
	})
	return stored, created
}

func jakartaToday(t *testing.T, s *Store) string {
	t.Helper()
	var d string
	if err := s.pool.QueryRow(context.Background(), `SELECT to_char(now() AT TIME ZONE 'Asia/Jakarta', 'DDMMYYYY')`).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEveryOrderHasACode(t *testing.T) {
	s := testStore(t)
	o, _ := store(t, s, orderWith(t, uuid.NewString(), "Tes kode", ""))
	if !domain.ValidCode(o.Code) {
		t.Fatalf("no client code: got %q", o.Code)
	}
	// The day in the code is the Jakarta day the order was created.
	want := o.CreatedAt.In(time.FixedZone("WIB", 7*3600)).Format("02012006")
	if !strings.HasPrefix(o.Code, "TRX-"+want+"-") {
		t.Errorf("code %s does not carry the creation day %s", o.Code, want)
	}
	got, err := s.GetOrder(context.Background(), o.ID)
	if err != nil || got.Code != o.Code {
		t.Errorf("read back %q, %v; want %q", got.Code, err, o.Code)
	}
}

func TestClientCodeIsKeptWhenFreeAndReplacedWhenNot(t *testing.T) {
	s := testStore(t)
	day := jakartaToday(t, s)
	mine := "TRX-" + day + "-" + strings.ToUpper(strings.ReplaceAll(uuid.NewString()[:4], "0", "2"))
	// Make sure the suggestion is inside the alphabet: derive it, then check it.
	mine = "TRX-" + day + "-Q" + string("ABCDEFGHJKLMNPQRSTUVWXYZ"[time.Now().Nanosecond()%24]) + "7" + string("23456789"[time.Now().Nanosecond()%8])
	if !domain.ValidCode(mine) {
		t.Fatalf("test code %q is not valid", mine)
	}
	first, created := store(t, s, orderWith(t, uuid.NewString(), "Tes kode klien", mine))
	if !created || first.Code != mine {
		t.Fatalf("a free well-formed client code is kept: %q (created %v)", first.Code, created)
	}
	// Another order suggesting the same code gets a fresh one.
	second, _ := store(t, s, orderWith(t, uuid.NewString(), "Tes kode kembar", mine))
	if second.Code == mine || !domain.ValidCode(second.Code) {
		t.Errorf("a taken code was reused or the replacement is invalid: %q", second.Code)
	}
	// A malformed suggestion is ignored.
	third, _ := store(t, s, orderWith(t, uuid.NewString(), "Tes kode rusak", "TRX-31022026-K7M2"))
	if third.Code == "TRX-31022026-K7M2" || !domain.ValidCode(third.Code) {
		t.Errorf("an impossible date was accepted: %q", third.Code)
	}
	// A retry of the same order id returns the stored order and its stored code, whatever code the retry carries.
	again, created := store(t, s, orderWith(t, first.ID, "Tes kode klien", "TRX-"+day+"-ZZZZ"))
	if created || again.Code != mine {
		t.Errorf("retry: created %v, code %q; want the stored %q", created, again.Code, mine)
	}
	// Even a retry that suggests a code another order holds is just a retry.
	again, created = store(t, s, orderWith(t, first.ID, "Tes kode klien", second.Code))
	if created || again.Code != mine {
		t.Errorf("retry with a taken code: created %v, code %q; want the stored %q", created, again.Code, mine)
	}
}

func TestCollisionIsRetriedWithAFreshCode(t *testing.T) {
	s := testStore(t)
	taken, _ := store(t, s, orderWith(t, uuid.NewString(), "Tes tabrakan", ""))
	// A generator that proposes the taken code twice before a free one: the order must still be created, with the free one.
	free := "TRX-" + jakartaToday(t, s) + "-" + "W" + string("ABCDEFGHJKLMNPQRSTUVWXYZ"[time.Now().Nanosecond()%24]) + "9" + string("23456789"[time.Now().Nanosecond()%8])
	calls := 0
	s.newCode = func(time.Time) string {
		calls++
		if calls <= 2 {
			return taken.Code
		}
		return free
	}
	got, created := store(t, s, orderWith(t, uuid.NewString(), "Tes tabrakan 2", ""))
	if !created || got.Code != free || calls != 3 {
		t.Errorf("code %q after %d calls (created %v); want %q after 3", got.Code, calls, created, free)
	}

	// A generator that never finds a free code: after 10 tries the answer is a clear error, and nothing is stored.
	calls = 0
	s.newCode = func(time.Time) string { calls++; return taken.Code }
	id := uuid.NewString()
	_, _, err := s.CreateOrder(context.Background(), orderWith(t, id, "Tes tabrakan 3", ""))
	ae, ok := err.(*apperr.Error)
	if !ok || ae.Kind != apperr.KindInternal || !strings.Contains(ae.Message, "unique transaction code") || calls != maxCodeAttempts {
		t.Errorf("err = %v after %d tries; want an internal error naming the code after %d", err, calls, maxCodeAttempts)
	}
	if _, err := s.GetOrder(context.Background(), id); err == nil {
		t.Error("an order that could not get a code was stored")
	}
}

func TestSearchAndSortByCode(t *testing.T) {
	s := testStore(t)
	a := app.New(s, nil)
	o, _ := store(t, s, orderWith(t, uuid.NewString(), "Tes cari kode", ""))
	admin := domain.Caller{Admin: true, UserID: "a:1"}
	list := func(q string) int64 {
		t.Helper()
		_, total, err := list(a, context.Background(), admin, 0, "", q, "code", "asc", "", "", 5, 0)
		if err != nil {
			t.Fatal(err)
		}
		return total
	}
	suffix := o.Code[len(o.Code)-4:]
	for label, q := range map[string]string{"the whole code": o.Code, "lowercase": strings.ToLower(o.Code), "the four characters": strings.ToLower(suffix), "the date part": o.Code[4:12]} {
		if list(q) < 1 {
			t.Errorf("searching for %s (%q) found nothing", label, q)
		}
		sum, err := summarize(a, context.Background(), admin, 0, "", q, "", "")
		if err != nil || sum.Orders != list(q) {
			t.Errorf("%s: the summary counts %d, the list %d (%v)", label, sum.Orders, list(q), err)
		}
	}
	if list(o.Code) != 1 {
		t.Errorf("the full code finds exactly its own order, got %d", list(o.Code))
	}
	// What matched before still matches: the customer's name, the start of the id.
	if list("cari kode") < 1 || list(o.ID[:8]) < 1 || list("TRX-"+o.ID[:8]) < 1 {
		t.Error("the old matches (name, uuid prefix, TRX- + uuid prefix) must keep working")
	}
	if list(o.Code+"-nope") != 0 {
		t.Error("a longer string should not match")
	}
}
