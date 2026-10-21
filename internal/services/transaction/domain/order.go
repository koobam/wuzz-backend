// Package domain holds transaction's business rules. No Postgres, gRPC or JSON.
package domain

import (
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Order statuses, mirroring the orders_status_valid constraint.
const (
	StatusCreated    = "created"
	StatusProcessing = "processing"
	StatusReady      = "ready"
	StatusFulfilled  = "fulfilled"
	StatusCancelled  = "cancelled"
)

// transitions is the whole lifecycle: an order moves forward one step at a time,
// or is cancelled before it is ready. Fulfilled and cancelled are final.
var transitions = map[string][]string{
	StatusCreated:    {StatusProcessing, StatusCancelled},
	StatusProcessing: {StatusReady, StatusCancelled},
	StatusReady:      {StatusFulfilled},
}

type Line struct {
	ItemID int64
	Name   string
	Unit   string
	Price  int64
	Qty    float64
}

type Order struct {
	ID            string
	OutletID      int64
	CustomerName  string
	CustomerPhone string
	Lines         []Line
	Total         int64
	Status        string
	CreatedBy     string
	Version       int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const maxQty = 1000

// NewOrder validates an order a staff member rang up and computes its total.
// The total is always recomputed here, never trusted from the client.
func NewOrder(id string, outletID int64, customerName, customerPhone string, lines []Line, createdBy string) (Order, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Order{}, apperr.Invalid("The order id must be a UUID")
	}
	if outletID <= 0 {
		return Order{}, apperr.Invalid("An outlet is required")
	}
	customerName = strings.TrimSpace(customerName)
	if customerName == "" {
		return Order{}, apperr.Invalid("A customer name is required")
	}
	if len(lines) == 0 {
		return Order{}, apperr.Invalid("An order needs at least one item")
	}

	var total float64
	for i := range lines {
		l := &lines[i]
		l.Name = strings.TrimSpace(l.Name)
		if l.Name == "" || l.Price < 0 || l.Qty <= 0 || l.Qty > maxQty || math.IsNaN(l.Qty) {
			return Order{}, apperr.Invalid("Every item needs a name, a price and a quantity between 0 and 1000")
		}
		total += float64(l.Price) * l.Qty
	}

	return Order{
		ID:            id,
		OutletID:      outletID,
		CustomerName:  customerName,
		CustomerPhone: strings.TrimSpace(customerPhone),
		Lines:         lines,
		Total:         int64(math.Round(total)),
		Status:        StatusCreated,
		CreatedBy:     createdBy,
		Version:       1,
	}, nil
}

// CanMoveTo reports whether the lifecycle allows from -> to.
func CanMoveTo(from, to string) bool {
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

func ValidStatus(s string) bool {
	switch s {
	case StatusCreated, StatusProcessing, StatusReady, StatusFulfilled, StatusCancelled:
		return true
	}
	return false
}
