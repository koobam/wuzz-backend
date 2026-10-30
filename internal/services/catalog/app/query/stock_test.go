package query

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type stockReadFake struct {
	Stock
	listed int
}

func (f *stockReadFake) ListStock(context.Context, domain.StockQuery) ([]domain.StockRow, int64, error) {
	f.listed++
	return nil, 0, nil
}

func TestListStockReadsTheQueryBeforeTheStoreSeesIt(t *testing.T) {
	f := &stockReadFake{}
	h := NewListStockHandler(f)

	if _, err := h.Handle(context.Background(), ListStock{Query: domain.StockQuery{SortBy: "colour"}}); err == nil {
		t.Error("a sort by colour was accepted")
	}
	if f.listed != 0 {
		t.Errorf("an invalid query reached the store: %d reads", f.listed)
	}
	if _, err := h.Handle(context.Background(), ListStock{Query: domain.StockQuery{}}); err != nil || f.listed != 1 {
		t.Errorf("a default query: err %v, reads %d", err, f.listed)
	}
}
