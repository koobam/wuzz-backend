package command

import (
	"context"
	"math/big"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type stockFake struct {
	Stock
	received int
	minSet   int
}

func (f *stockFake) ReceiveStock(context.Context, domain.StockReceipt, string) (domain.StockMovement, bool, error) {
	f.received++
	return domain.StockMovement{}, true, nil
}

func (f *stockFake) SetMinStock(context.Context, int64, int64, *big.Rat) (domain.StockRow, error) {
	f.minSet++
	return domain.StockRow{}, nil
}

func TestStockWritesAreReadExactlyBeforeTheStoreSeesThem(t *testing.T) {
	f := &stockFake{}

	if _, err := NewReceiveStockHandler(f).Handle(context.Background(), ReceiveStock{By: "a:1"}); err == nil {
		t.Error("an empty receipt was accepted")
	}
	if f.received != 0 {
		t.Errorf("an unreadable receipt reached the store: %d writes", f.received)
	}

	h := NewSetMinStockHandler(f)
	for name, cmd := range map[string]SetMinStock{
		"no outlet":   {OutletID: 0, MaterialID: 1, MinQty: "5"},
		"no material": {OutletID: 1, MaterialID: 0, MinQty: "5"},
	} {
		if _, err := h.Handle(context.Background(), cmd); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
	if f.minSet != 0 {
		t.Errorf("an incomplete minimum reached the store: %d writes", f.minSet)
	}
}
