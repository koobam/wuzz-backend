package catalogclient

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// catalog is a stand-in for the catalog service: it records the call and fails when told to.
type catalog struct {
	catalogv1.CatalogServiceClient
	calls []*catalogv1.ApplyOrderStockRequest
	keys  []string
	fail  bool
}

func (c *catalog) ApplyOrderStock(ctx context.Context, in *catalogv1.ApplyOrderStockRequest, _ ...grpc.CallOption) (*catalogv1.ApplyOrderStockResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	c.keys = append(c.keys, md.Get(internalKeyHeader)...)
	c.calls = append(c.calls, in)
	if c.fail {
		return nil, errors.New("catalog is down")
	}
	return &catalogv1.ApplyOrderStockResponse{}, nil
}

func TestTheCallCarriesTheOrderTheActionAndTheKey(t *testing.T) {
	c := &catalog{}
	o := domain.Order{ID: "o1", Code: "TRX-25102026-K7M2", OutletID: 4, Lines: []domain.Line{
		{ItemID: 8, Qty: 2.5, VariantOptionIDs: []int64{2}, ModifierOptionIDs: []int64{1, 3}},
		{ItemID: 9, Qty: 0.1}, // a float's shortest decimal, not 0.1000000000000000055...
	}}
	if err := New(c, "secret").ApplyOrderStock(context.Background(), domain.StockConsume, o, "s:3"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 1 {
		t.Fatalf("calls = %d", len(c.calls))
	}
	got := c.calls[0]
	if got.GetOrderCode() != "TRX-25102026-K7M2" {
		t.Errorf("the order's code did not travel with the call: %q", got.GetOrderCode())
	}
	if got.GetOrderId() != "o1" || got.GetOutletId() != 4 || got.GetAction() != "consume" || got.GetActor() != "s:3" || len(got.GetLines()) != 2 {
		t.Fatalf("request = %v", got)
	}
	l := got.GetLines()[0]
	if l.GetServiceId() != 8 || l.GetQty() != "2.5" || len(l.GetVariantOptionIds()) != 1 || len(l.GetModifierOptionIds()) != 2 || got.GetLines()[1].GetQty() != "0.1" {
		t.Errorf("lines = %v", got.GetLines())
	}
	if len(c.keys) != 1 || c.keys[0] != "secret" {
		t.Errorf("the internal key was not presented: %v", c.keys)
	}
}

func TestAFailureComesBackAsAnErrorSoTheStatusDoesNotChange(t *testing.T) {
	c := &catalog{fail: true}
	err := New(c, "secret").ApplyOrderStock(context.Background(), domain.StockReverse, domain.Order{ID: "o1", OutletID: 1}, "a:1")
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Kind != apperr.KindInternal {
		t.Fatalf("err = %v; want an application error the caller can show", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("the cause was lost, so the log would not say why: %v", err)
	}
}

func TestWithoutTheKeyNothingIsSent(t *testing.T) {
	c := &catalog{}
	if err := New(c, "").ApplyOrderStock(context.Background(), domain.StockConsume, domain.Order{ID: "o1"}, "s:1"); err == nil {
		t.Error("a call without the internal key was made")
	}
	if len(c.calls) != 0 {
		t.Errorf("the catalog was called %d times without a key", len(c.calls))
	}
}
