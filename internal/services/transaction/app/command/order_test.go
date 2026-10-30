package command

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// capture is a store that remembers the order it was asked to create or move. UpdateStatus runs the effect it is given the way the
// real store does: with the order before and after, and a failing effect stops the change.
type capture struct {
	Orders
	got       domain.Order
	existing  domain.Order
	created   int
	statusSet int
}

func (c *capture) CreateOrder(_ context.Context, o domain.Order) (domain.Order, bool, error) {
	c.got, c.created = o, c.created+1
	return o, true, nil
}

func (c *capture) GetOrder(context.Context, string) (domain.Order, error) { return c.existing, nil }

func (c *capture) UpdateStatus(ctx context.Context, _, status, _ string, effect domain.StatusEffect) (domain.Order, error) {
	after := c.existing
	after.Status = status
	if effect != nil {
		if err := effect(ctx, c.existing, after); err != nil {
			return domain.Order{}, err
		}
	}
	c.statusSet++
	return after, nil
}

// stockSpy records what the order service asked the catalog to do, and fails when told to.
type stockSpy struct {
	actions []string
	actors  []string
	fail    error
}

func (s *stockSpy) ApplyOrderStock(_ context.Context, action string, _ domain.Order, actor string) error {
	s.actions = append(s.actions, action)
	s.actors = append(s.actors, actor)
	return s.fail
}

func TestCreatePassesOnlyAWellFormedCode(t *testing.T) {
	line := []domain.Line{{ItemID: 1, Name: "x", Price: 1, Qty: 1}}
	cashier := domain.Caller{Outlets: []int64{1}, UserID: "s:1"}
	for name, c := range map[string]struct{ sent, want string }{
		"a good code is kept":       {"TRX-25102026-K7M2", "TRX-25102026-K7M2"},
		"none sent":                 {"", ""},
		"lowercase is not accepted": {"trx-25102026-k7m2", ""},
		"wrong alphabet":            {"TRX-25102026-O0I1", ""},
		"an impossible date":        {"TRX-31022026-K7M2", ""},
		"anything else":             {"hello", ""},
	} {
		st := &capture{}
		_, err := NewCreateOrderHandler(st).Handle(context.Background(), CreateOrder{
			Caller: cashier, ID: "6f9619ff-8b86-d011-b42d-00cf4fc964ff", OutletID: 1, CustomerName: "Tes", Code: c.sent, Lines: line})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if st.got.Code != c.want {
			t.Errorf("%s: the store was asked for %q; want %q", name, st.got.Code, c.want)
		}
	}
}

func TestCreateIsRefusedBeforeTheStoreForTheWrongCaller(t *testing.T) {
	line := []domain.Line{{ItemID: 1, Name: "x", Price: 1, Qty: 1}}
	for name, caller := range map[string]domain.Caller{
		"someone who is not staff": {},
		"staff of another outlet":  {Outlets: []int64{2}, UserID: "s:2"},
	} {
		st := &capture{}
		_, err := NewCreateOrderHandler(st).Handle(context.Background(), CreateOrder{
			Caller: caller, ID: "6f9619ff-8b86-d011-b42d-00cf4fc964ff", OutletID: 1, CustomerName: "Tes", Lines: line})
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
		if st.created != 0 {
			t.Errorf("%s: reached the store %d times", name, st.created)
		}
	}
}

func TestSetOrderStatusChecksTheStatusAndTheOutletBeforeWriting(t *testing.T) {
	st := &capture{existing: domain.Order{ID: "o1", OutletID: 1}}
	h := NewSetOrderStatusHandler(st, &stockSpy{})
	cashier := domain.Caller{Outlets: []int64{1}, UserID: "s:1"}

	if _, err := h.Handle(context.Background(), SetOrderStatus{Caller: cashier, ID: "o1", Status: "shipped"}); err == nil {
		t.Error("an unknown status was accepted")
	}
	if _, err := h.Handle(context.Background(), SetOrderStatus{Caller: domain.Caller{Outlets: []int64{2}}, ID: "o1", Status: "processing"}); err == nil {
		t.Error("another outlet's order was moved")
	}
	if st.statusSet != 0 {
		t.Fatalf("a refused change reached the store %d times", st.statusSet)
	}
	if _, err := h.Handle(context.Background(), SetOrderStatus{Caller: cashier, ID: "o1", Status: "processing"}); err != nil || st.statusSet != 1 {
		t.Errorf("their own order: err %v, writes %d", err, st.statusSet)
	}
}

func TestTheStockMovesWithTheStatus(t *testing.T) {
	cashier := domain.Caller{Outlets: []int64{1}, UserID: "s:1"}
	for name, c := range map[string]struct {
		from, to string
		want     []string
	}{
		"starting work takes the materials out":      {domain.StatusCreated, domain.StatusProcessing, []string{"consume"}},
		"cancelling after that gives them back":      {domain.StatusProcessing, domain.StatusCancelled, []string{"reverse"}},
		"cancelled before any work: nothing to undo": {domain.StatusCreated, domain.StatusCancelled, nil},
		"ready touches nothing":                      {domain.StatusProcessing, domain.StatusReady, nil},
		"fulfilled touches nothing":                  {domain.StatusReady, domain.StatusFulfilled, nil},
	} {
		st := &capture{existing: domain.Order{ID: "o1", OutletID: 1, Status: c.from}}
		spy := &stockSpy{}
		if _, err := NewSetOrderStatusHandler(st, spy).Handle(context.Background(), SetOrderStatus{Caller: cashier, ID: "o1", Status: c.to}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(spy.actions) != len(c.want) || (len(c.want) == 1 && spy.actions[0] != c.want[0]) {
			t.Errorf("%s: the catalog was asked %v; want %v", name, spy.actions, c.want)
		}
		if len(c.want) == 1 && spy.actors[0] != "s:1" {
			t.Errorf("%s: the movement was booked for %q; want the person who made the change", name, spy.actors[0])
		}
	}
}

func TestACatalogThatCannotBeReachedStopsTheStatusChange(t *testing.T) {
	st := &capture{existing: domain.Order{ID: "o1", OutletID: 1, Status: domain.StatusCreated}}
	down := apperr.Internal("The stock could not be updated, so the order was not changed.")
	_, err := NewSetOrderStatusHandler(st, &stockSpy{fail: down}).Handle(context.Background(),
		SetOrderStatus{Caller: domain.Caller{Outlets: []int64{1}, UserID: "s:1"}, ID: "o1", Status: domain.StatusProcessing})
	if err == nil {
		t.Fatal("the order moved although the stock could not be taken out")
	}
	if st.statusSet != 0 {
		t.Errorf("the status was written %d times; want 0", st.statusSet)
	}
}
