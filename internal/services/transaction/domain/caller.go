package domain

import "github.com/koobam/wuzz-backend/internal/pkg/apperr"

// Caller is who is asking, reduced to what authorisation needs: who may touch which outlet's orders.
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

// ErrOutletForbidden is what a caller gets for naming an outlet that is not theirs.
var ErrOutletForbidden = apperr.Forbidden("You do not have access to this outlet")

// CanRead says whether the caller may see the order. It answers 404, not 403: another outlet's order is not confirmed to exist.
func (c Caller) CanRead(o Order) error {
	if !c.MayUse(o.OutletID) {
		return apperr.NotFound("Order not found")
	}
	return nil
}
