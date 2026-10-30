// Package command is catalog's write side: one handler per use case. Each handler states, in its own port, the
// few repository methods it needs; app.go wires them to the Postgres store.
package command

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Outlets is what the outlet writes need of the store.
type Outlets interface {
	CreateOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error)
	UpdateOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error)
	// DeleteOutlet refuses while the outlet has stock records; it takes its opening hours with it.
	DeleteOutlet(ctx context.Context, id int64) error
}

// ---------- CreateOutlet ----------

type CreateOutlet struct{ Outlet domain.Outlet }

type CreateOutletHandler struct{ outlets Outlets }

func NewCreateOutletHandler(outlets Outlets) *CreateOutletHandler {
	return &CreateOutletHandler{outlets: outlets}
}

// Handle validates the outlet before it reaches the store. A new outlet is always active, and what it leaves out of its category,
// hours and delivery types takes the defaults.
func (h *CreateOutletHandler) Handle(ctx context.Context, cmd CreateOutlet) (domain.Outlet, error) {
	o := cmd.Outlet
	// Phone, email and category are required on a new outlet, so they are checked before the defaults fill the category in.
	if err := o.RequireContact(); err != nil {
		return domain.Outlet{}, err
	}
	o.Active = true
	o.ApplyDefaults()
	if err := o.Validate(); err != nil {
		return domain.Outlet{}, err
	}
	return h.outlets.CreateOutlet(ctx, o)
}

// ---------- UpdateOutlet ----------

type UpdateOutlet struct{ Outlet domain.Outlet }

type UpdateOutletHandler struct{ outlets Outlets }

func NewUpdateOutletHandler(outlets Outlets) *UpdateOutletHandler {
	return &UpdateOutletHandler{outlets: outlets}
}

func (h *UpdateOutletHandler) Handle(ctx context.Context, cmd UpdateOutlet) (domain.Outlet, error) {
	o := cmd.Outlet
	if err := o.Validate(); err != nil {
		return domain.Outlet{}, err
	}
	return h.outlets.UpdateOutlet(ctx, o)
}

// ---------- DeleteOutlet ----------

type DeleteOutlet struct{ ID int64 }

type DeleteOutletHandler struct{ outlets Outlets }

func NewDeleteOutletHandler(outlets Outlets) *DeleteOutletHandler {
	return &DeleteOutletHandler{outlets: outlets}
}

func (h *DeleteOutletHandler) Handle(ctx context.Context, cmd DeleteOutlet) error {
	return h.outlets.DeleteOutlet(ctx, cmd.ID)
}
