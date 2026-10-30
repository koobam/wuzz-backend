// Package command is transaction's write side: one handler per use case. Each handler states, in its own port, the
// few repository methods it needs; app.go wires them to the Postgres store.
package command

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// Orders is what the order writes need of the store.
type Orders interface {
	// CreateOrder inserts the order, or returns the stored one when the id already exists (created=false).
	CreateOrder(ctx context.Context, o domain.Order) (order domain.Order, created bool, err error)
	GetOrder(ctx context.Context, id string) (domain.Order, error)
	// UpdateStatus moves the order; actor is who did it. effect, when given, runs inside the same transaction once the move is
	// allowed and before it commits, so if it fails nothing changes. It is not run when the order is already in that status.
	UpdateStatus(ctx context.Context, id, status, actor string, effect domain.StatusEffect) (domain.Order, error)
}

// Stock is the catalog's side of an order: it takes the order's materials out of stock, or puts them back. The catalog applies each
// action once per order, so asking again after a failure is harmless.
type Stock interface {
	ApplyOrderStock(ctx context.Context, action string, order domain.Order, actor string) error
}

// ---------- CreateOrder ----------

type CreateOrder struct {
	Caller        domain.Caller
	ID            string
	OutletID      int64
	CustomerName  string
	CustomerPhone string
	// Code is one the client suggests, if any (the POS no longer does): it is kept when it has the right shape (a real date, the
	// right alphabet) and is not another order's; otherwise the store gives the order a fresh one.
	Code  string
	Lines []domain.Line
}

type CreateOrderResult struct {
	Order domain.Order
	// Created is false when the id was already stored (a retried sale): the stored order is returned unchanged.
	Created bool
}

type CreateOrderHandler struct{ orders Orders }

func NewCreateOrderHandler(orders Orders) *CreateOrderHandler {
	return &CreateOrderHandler{orders: orders}
}

func (h *CreateOrderHandler) Handle(ctx context.Context, cmd CreateOrder) (CreateOrderResult, error) {
	c := cmd.Caller
	if !c.IsStaff() {
		return CreateOrderResult{}, apperr.Forbidden("Staff access is required")
	}
	if !c.MayUse(cmd.OutletID) {
		return CreateOrderResult{}, domain.ErrOutletForbidden
	}
	o, err := domain.NewOrder(cmd.ID, cmd.OutletID, cmd.CustomerName, cmd.CustomerPhone, cmd.Lines, c.UserID)
	if err != nil {
		return CreateOrderResult{}, err
	}
	if domain.ValidCode(cmd.Code) {
		o.Code = cmd.Code
	}
	stored, created, err := h.orders.CreateOrder(ctx, o)
	if err != nil {
		return CreateOrderResult{}, err
	}
	// A retried id that belongs to another outlet is not this caller's to read.
	if !created && !c.MayUse(stored.OutletID) {
		return CreateOrderResult{}, apperr.Conflict("That order id is already in use")
	}
	return CreateOrderResult{Order: stored, Created: created}, nil
}

// ---------- SetOrderStatus ----------

type SetOrderStatus struct {
	Caller domain.Caller
	ID     string
	Status string
}

type SetOrderStatusHandler struct {
	orders Orders
	stock  Stock
}

func NewSetOrderStatusHandler(orders Orders, stock Stock) *SetOrderStatusHandler {
	return &SetOrderStatusHandler{orders: orders, stock: stock}
}

func (h *SetOrderStatusHandler) Handle(ctx context.Context, cmd SetOrderStatus) (domain.Order, error) {
	if !domain.ValidStatus(cmd.Status) {
		return domain.Order{}, apperr.Invalid("Unknown status")
	}
	// Existence and outlet access, before anything is written.
	o, err := h.orders.GetOrder(ctx, cmd.ID)
	if err != nil {
		return domain.Order{}, err
	}
	if err := cmd.Caller.CanRead(o); err != nil {
		return domain.Order{}, err
	}
	// The POS is online-only, so the stock moves with the status, not afterwards from a queue: if the catalog cannot be reached the
	// status stays as it was and the cashier is told, instead of an order running on materials that were never taken out.
	actor := cmd.Caller.UserID
	return h.orders.UpdateStatus(ctx, cmd.ID, cmd.Status, actor, func(ctx context.Context, before, after domain.Order) error {
		action := domain.StockActionFor(before.Status, after.Status)
		if action == "" {
			return nil
		}
		return h.stock.ApplyOrderStock(ctx, action, after, actor)
	})
}
