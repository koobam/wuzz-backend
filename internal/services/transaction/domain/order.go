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

// Modifier is an add-on picked for a line (a group's option), with what it adds per unit.
type Modifier struct {
	Group string
	Name  string
	Price int64
}

type Line struct {
	ItemID int64
	Name   string
	Unit   string
	// Price is per unit and already includes the variant and the modifiers.
	Price     int64
	Qty       float64
	Variant   string
	Modifiers []Modifier
	// The options chosen, by catalog id (for costing only).
	VariantOptionIDs  []int64
	ModifierOptionIDs []int64
}

type Order struct {
	ID string
	// Code is the order's human-readable name (TRX-DDMMYYYY-XXXX). On a new order it is only the client's suggestion, which the
	// store keeps if it is valid and free and replaces otherwise.
	Code          string
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

const (
	maxQty       = 1000
	maxModifiers = 20
	maxLabel     = 80
)

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
		if err := l.cleanChoices(); err != nil {
			return Order{}, err
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

// cleanChoices trims and checks the variant and the modifiers of a line.
func (l *Line) cleanChoices() error {
	l.Variant = strings.TrimSpace(l.Variant)
	if len([]rune(l.Variant)) > maxLabel || len(l.Modifiers) > maxModifiers {
		return apperr.Invalid("An item has too many or too long choices")
	}
	for i := range l.Modifiers {
		m := &l.Modifiers[i]
		m.Group, m.Name = strings.TrimSpace(m.Group), strings.TrimSpace(m.Name)
		if m.Name == "" || len([]rune(m.Name)) > maxLabel || len([]rune(m.Group)) > maxLabel || m.Price < 0 {
			return apperr.Invalid("Every add-on needs a name and a price of 0 or more")
		}
	}
	if err := cleanIDs(l.VariantOptionIDs); err != nil {
		return err
	}
	return cleanIDs(l.ModifierOptionIDs)
}

// cleanIDs checks the option ids of a line: at most 20, each positive, none twice.
func cleanIDs(ids []int64) error {
	if len(ids) > maxModifiers {
		return apperr.Invalid("An item has too many choices")
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return apperr.Invalid("The option ids of an item must be positive and listed once")
		}
		seen[id] = true
	}
	return nil
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

// Statuses lists every status, in lifecycle order.
var Statuses = []string{StatusCreated, StatusProcessing, StatusReady, StatusFulfilled, StatusCancelled}

func ValidStatus(s string) bool {
	switch s {
	case StatusCreated, StatusProcessing, StatusReady, StatusFulfilled, StatusCancelled:
		return true
	}
	return false
}
