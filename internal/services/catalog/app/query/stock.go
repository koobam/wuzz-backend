package query

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Stock is what the stock reads need of the store.
type Stock interface {
	ListStock(ctx context.Context, q domain.StockQuery) ([]domain.StockRow, int64, error)
	ListMovements(ctx context.Context, q domain.MovementQuery) ([]domain.StockMovement, int64, error)
}

// Who may read which outlet's stock is decided by the transport, which knows the caller.

// ---------- ListStock ----------

type ListStock struct{ Query domain.StockQuery }

type ListStockResult struct {
	Rows  []domain.StockRow
	Total int64
}

type ListStockHandler struct{ stock Stock }

func NewListStockHandler(stock Stock) *ListStockHandler { return &ListStockHandler{stock: stock} }

func (h *ListStockHandler) Handle(ctx context.Context, q ListStock) (ListStockResult, error) {
	if err := q.Query.Validate(); err != nil {
		return ListStockResult{}, err
	}
	rows, total, err := h.stock.ListStock(ctx, q.Query)
	return ListStockResult{Rows: rows, Total: total}, err
}

// ---------- ListMovements ----------

type ListMovements struct{ Query domain.MovementQuery }

type ListMovementsResult struct {
	Movements []domain.StockMovement
	Total     int64
}

type ListMovementsHandler struct{ stock Stock }

func NewListMovementsHandler(stock Stock) *ListMovementsHandler {
	return &ListMovementsHandler{stock: stock}
}

func (h *ListMovementsHandler) Handle(ctx context.Context, q ListMovements) (ListMovementsResult, error) {
	if err := q.Query.Validate(); err != nil {
		return ListMovementsResult{}, err
	}
	list, total, err := h.stock.ListMovements(ctx, q.Query)
	return ListMovementsResult{Movements: list, Total: total}, err
}
