package command

import (
	"context"
	"math/big"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Stock is what the stock writes need of the store: the stock ledger. `created` is false when the idempotency key had
// already been used and the original movement is returned.
type Stock interface {
	ReceiveStock(ctx context.Context, r domain.StockReceipt, by string) (mv domain.StockMovement, created bool, err error)
	WasteStock(ctx context.Context, w domain.StockWaste, by string) (mv domain.StockMovement, created bool, err error)
	// AdjustStock books counted minus book; it returns nil when they are equal.
	AdjustStock(ctx context.Context, a domain.StockAdjust, by string) (mv *domain.StockMovement, created bool, err error)
	TransferStock(ctx context.Context, t domain.StockTransfer, by string) (out, in domain.StockMovement, created bool, err error)
	SetMinStock(ctx context.Context, outlet, material int64, min *big.Rat) (domain.StockRow, error)
	// ConsumeOrder and ReverseOrder return how many movements they wrote; both are idempotent per order.
	ConsumeOrder(ctx context.Context, o domain.StockOrder, by string) (int, error)
	ReverseOrder(ctx context.Context, o domain.StockOrder, by string) (int, error)
}

// Every stock handler reads the request exactly (domain.*Input.Parse) before the store sees it. Who may touch which
// outlet is decided by the transport, which knows the caller.

// StockMovementResult is the movement a receipt or a waste booked.
type StockMovementResult struct {
	Movement domain.StockMovement
	Created  bool
}

// ---------- ReceiveStock ----------

type ReceiveStock struct {
	Input domain.StockReceiptInput
	By    string
}

type ReceiveStockHandler struct{ stock Stock }

func NewReceiveStockHandler(stock Stock) *ReceiveStockHandler {
	return &ReceiveStockHandler{stock: stock}
}

func (h *ReceiveStockHandler) Handle(ctx context.Context, cmd ReceiveStock) (StockMovementResult, error) {
	r, err := cmd.Input.Parse()
	if err != nil {
		return StockMovementResult{}, err
	}
	mv, created, err := h.stock.ReceiveStock(ctx, r, cmd.By)
	return StockMovementResult{Movement: mv, Created: created}, err
}

// ---------- WasteStock ----------

type WasteStock struct {
	Input domain.StockWasteInput
	By    string
}

type WasteStockHandler struct{ stock Stock }

func NewWasteStockHandler(stock Stock) *WasteStockHandler { return &WasteStockHandler{stock: stock} }

func (h *WasteStockHandler) Handle(ctx context.Context, cmd WasteStock) (StockMovementResult, error) {
	w, err := cmd.Input.Parse()
	if err != nil {
		return StockMovementResult{}, err
	}
	mv, created, err := h.stock.WasteStock(ctx, w, cmd.By)
	return StockMovementResult{Movement: mv, Created: created}, err
}

// ---------- CountStock ----------

type CountStock struct {
	Input domain.StockAdjustInput
	By    string
}

// CountStockResult carries no movement when the count matched the book.
type CountStockResult struct {
	Movement *domain.StockMovement
	Created  bool
}

type CountStockHandler struct{ stock Stock }

func NewCountStockHandler(stock Stock) *CountStockHandler { return &CountStockHandler{stock: stock} }

func (h *CountStockHandler) Handle(ctx context.Context, cmd CountStock) (CountStockResult, error) {
	c, err := cmd.Input.Parse()
	if err != nil {
		return CountStockResult{}, err
	}
	mv, created, err := h.stock.AdjustStock(ctx, c, cmd.By)
	return CountStockResult{Movement: mv, Created: created}, err
}

// ---------- TransferStock ----------

type TransferStock struct {
	Input domain.StockTransferInput
	By    string
}

type TransferStockResult struct {
	Out, In domain.StockMovement
	Created bool
}

type TransferStockHandler struct{ stock Stock }

func NewTransferStockHandler(stock Stock) *TransferStockHandler {
	return &TransferStockHandler{stock: stock}
}

func (h *TransferStockHandler) Handle(ctx context.Context, cmd TransferStock) (TransferStockResult, error) {
	t, err := cmd.Input.Parse()
	if err != nil {
		return TransferStockResult{}, err
	}
	out, in, created, err := h.stock.TransferStock(ctx, t, cmd.By)
	return TransferStockResult{Out: out, In: in, Created: created}, err
}

// ---------- SetMinStock ----------

type SetMinStock struct {
	OutletID, MaterialID int64
	MinQty               string
}

type SetMinStockHandler struct{ stock Stock }

func NewSetMinStockHandler(stock Stock) *SetMinStockHandler { return &SetMinStockHandler{stock: stock} }

func (h *SetMinStockHandler) Handle(ctx context.Context, cmd SetMinStock) (domain.StockRow, error) {
	if cmd.OutletID <= 0 || cmd.MaterialID <= 0 {
		return domain.StockRow{}, apperr.Invalid("An outlet and a material are required")
	}
	q, err := domain.ParseMinQty(cmd.MinQty)
	if err != nil {
		return domain.StockRow{}, err
	}
	return h.stock.SetMinStock(ctx, cmd.OutletID, cmd.MaterialID, q)
}

// ---------- ConsumeOrder / ReverseOrder ----------

// ConsumeOrder takes an order's materials out of stock; ReverseOrder puts them back. Both are called by the order
// service, never by a person, and both are idempotent per order. They answer with how many movements they wrote.
type ConsumeOrder struct {
	Order domain.StockOrder
	By    string
}

type ConsumeOrderHandler struct{ stock Stock }

func NewConsumeOrderHandler(stock Stock) *ConsumeOrderHandler {
	return &ConsumeOrderHandler{stock: stock}
}

func (h *ConsumeOrderHandler) Handle(ctx context.Context, cmd ConsumeOrder) (int, error) {
	return h.stock.ConsumeOrder(ctx, cmd.Order, cmd.By)
}

type ReverseOrder struct {
	Order domain.StockOrder
	By    string
}

type ReverseOrderHandler struct{ stock Stock }

func NewReverseOrderHandler(stock Stock) *ReverseOrderHandler {
	return &ReverseOrderHandler{stock: stock}
}

func (h *ReverseOrderHandler) Handle(ctx context.Context, cmd ReverseOrder) (int, error) {
	return h.stock.ReverseOrder(ctx, cmd.Order, cmd.By)
}
