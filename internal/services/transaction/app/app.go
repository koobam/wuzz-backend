// Package app holds transaction's use cases and the authorisation rule: who may
// touch which outlet's orders. The Store port is implemented by adapters/postgres.
package app

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// Caller is who is asking, reduced to what authorisation needs.
type Caller struct {
	UserID string
	Admin  bool
	// Outlets the caller works at (from `outlet:<id>` roles).
	Outlets []int64
}

func (c Caller) MayUse(outletID int64) bool {
	if c.Admin {
		return true
	}
	for _, o := range c.Outlets {
		if o == outletID {
			return true
		}
	}
	return false
}

// IsStaff reports whether the caller may use orders at all.
func (c Caller) IsStaff() bool { return c.Admin || len(c.Outlets) > 0 }

type ListFilter struct {
	// OutletIDs limits the listing; nil means every outlet (admin only).
	OutletIDs     []int64
	Status        string
	Limit, Offset int
}

type Store interface {
	// CreateOrder inserts the order, or returns the stored one when the id
	// already exists (created=false).
	CreateOrder(ctx context.Context, o domain.Order) (order domain.Order, created bool, err error)
	GetOrder(ctx context.Context, id string) (domain.Order, error)
	ListOrders(ctx context.Context, f ListFilter) ([]domain.Order, int64, error)
	UpdateStatus(ctx context.Context, id, status string) (domain.Order, error)
}

type App struct{ store Store }

func New(store Store) *App { return &App{store: store} }

var errForbidden = apperr.Forbidden("You do not have access to this outlet")

func (a *App) Create(ctx context.Context, c Caller, id string, outletID int64, name, phone string, lines []domain.Line) (domain.Order, bool, error) {
	if !c.IsStaff() {
		return domain.Order{}, false, apperr.Forbidden("Staff access is required")
	}
	if !c.MayUse(outletID) {
		return domain.Order{}, false, errForbidden
	}
	o, err := domain.NewOrder(id, outletID, name, phone, lines, c.UserID)
	if err != nil {
		return domain.Order{}, false, err
	}
	stored, created, err := a.store.CreateOrder(ctx, o)
	if err != nil {
		return domain.Order{}, false, err
	}
	// A retried id that belongs to another outlet is not this caller's to read.
	if !created && !c.MayUse(stored.OutletID) {
		return domain.Order{}, false, apperr.Conflict("That order id is already in use")
	}
	return stored, created, nil
}

func (a *App) Get(ctx context.Context, c Caller, id string) (domain.Order, error) {
	o, err := a.store.GetOrder(ctx, id)
	if err != nil {
		return domain.Order{}, err
	}
	if !c.MayUse(o.OutletID) {
		// 404, not 403: do not confirm that another outlet's order exists.
		return domain.Order{}, apperr.NotFound("Order not found")
	}
	return o, nil
}

func (a *App) List(ctx context.Context, c Caller, outletID int64, status string, limit, offset int) ([]domain.Order, int64, error) {
	if !c.IsStaff() {
		return nil, 0, apperr.Forbidden("Staff access is required")
	}
	if status != "" && !domain.ValidStatus(status) {
		return nil, 0, apperr.Invalid("Unknown status")
	}
	f := ListFilter{Status: status, Limit: limit, Offset: offset}
	switch {
	case outletID != 0 && !c.MayUse(outletID):
		return nil, 0, errForbidden
	case outletID != 0:
		f.OutletIDs = []int64{outletID}
	case !c.Admin:
		f.OutletIDs = c.Outlets
	}
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 25
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return a.store.ListOrders(ctx, f)
}

func (a *App) SetStatus(ctx context.Context, c Caller, id, status string) (domain.Order, error) {
	if !domain.ValidStatus(status) {
		return domain.Order{}, apperr.Invalid("Unknown status")
	}
	if _, err := a.Get(ctx, c, id); err != nil { // existence + outlet access
		return domain.Order{}, err
	}
	return a.store.UpdateStatus(ctx, id, status)
}
