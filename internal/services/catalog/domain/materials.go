package domain

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Material is a bahan the laundry buys: a chemical, packaging, a consumable. Every quantity in a recipe (BOM) is in its
// base Unit. What it costs per base unit is the exact rational PurchasePrice / PurchaseQty (see UnitCost).
type Material struct {
	ID       int64
	SKU      string
	Name     string
	Category string // chemical | packaging | consumable | other
	Unit     string // ml | g | pcs | sheet
	// PurchasePrice is whole rupiah and buys PurchaseQty base units (an exact decimal of up to 4 places).
	PurchasePrice int64
	PurchaseQty   *big.Rat
	Active        bool
	Notes         string
	// UsedIn is how many recipe lines use it. Filled on reads.
	UsedIn    int32
	CreatedAt time.Time
	UpdatedAt time.Time
}

var (
	MaterialCategories = []string{"chemical", "packaging", "consumable", "other"}
	MaterialUnits      = []string{"ml", "g", "pcs", "sheet"}
	skuRE              = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

const (
	maxSKU           = 40
	maxMaterialNotes = 500
)

// UnitCost is what one base unit costs: purchase price over purchase quantity, as an exact fraction. It is not rounded.
func (m Material) UnitCost() *big.Rat {
	return new(big.Rat).Quo(new(big.Rat).SetInt64(m.PurchasePrice), m.PurchaseQty)
}

// MaterialInput is a material as a form posts it; quantities are strings until ParseMaterial reads them exactly.
type MaterialInput struct {
	SKU, Name, Category, Unit string
	PurchasePrice             int64
	PurchaseQty               string
	Active                    bool
	Notes                     string
}

func oneOf(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ParseMaterial checks and trims a material. The SKU keeps its case but is unique ignoring it.
func ParseMaterial(in MaterialInput) (Material, error) {
	m := Material{SKU: strings.TrimSpace(in.SKU), Name: strings.TrimSpace(in.Name), Category: in.Category, Unit: in.Unit,
		PurchasePrice: in.PurchasePrice, Active: in.Active, Notes: strings.TrimSpace(in.Notes)}
	switch {
	case m.SKU == "" || len(m.SKU) > maxSKU:
		return m, apperr.Invalid("The SKU must be 1 to 40 characters")
	case !skuRE.MatchString(m.SKU):
		return m, apperr.Invalid("The SKU can only use letters, digits and - _ . /")
	case m.Name == "":
		return m, apperr.Invalid("A material needs a name")
	case len([]rune(m.Name)) > maxName:
		return m, apperr.Invalid("The name can be at most 80 characters")
	case !oneOf(MaterialCategories, m.Category):
		return m, apperr.Invalid("The category must be chemical, packaging, consumable or other")
	case !oneOf(MaterialUnits, m.Unit):
		return m, apperr.Invalid("The unit must be ml, g, pcs or sheet")
	case m.PurchasePrice < 0:
		return m, apperr.Invalid("The purchase price cannot be negative")
	case len([]rune(m.Notes)) > maxMaterialNotes:
		return m, apperr.Invalid("The notes can be at most 500 characters")
	}
	qty, err := ParseDecimal("The purchase quantity", in.PurchaseQty, 4)
	if err != nil {
		return m, err
	}
	if qty.Sign() <= 0 {
		return m, apperr.Invalid("The purchase quantity must be more than 0")
	}
	m.PurchaseQty = qty
	return m, nil
}

// MaterialQuery filters and orders the list of materials.
type MaterialQuery struct {
	// Q matches, ignoring case, part of the SKU, the name or the notes.
	Q        string
	Category string
	// Active is "", "true" or "false".
	Active          string
	SortBy, SortDir string
	Limit, Offset   int
}

// MaterialSortFields are the columns a list may be ordered by; the store maps each to its SQL.
var MaterialSortFields = map[string]bool{"id": true, "sku": true, "name": true, "created_at": true, "updated_at": true}

func (q *MaterialQuery) Validate() error {
	q.Q = strings.TrimSpace(q.Q)
	if len([]rune(q.Q)) > 64 {
		return apperr.Invalid("The search can be at most 64 characters")
	}
	if q.Category != "" && !oneOf(MaterialCategories, q.Category) {
		return apperr.Invalid("category must be chemical, packaging, consumable or other")
	}
	if q.Active != "" && q.Active != "true" && q.Active != "false" {
		return apperr.Invalid("active must be true or false")
	}
	if q.SortBy == "" {
		q.SortBy = "id"
	}
	if !MaterialSortFields[q.SortBy] {
		return apperr.Invalid("sort_by must be one of id, sku, name, created_at, updated_at")
	}
	switch q.SortDir {
	case "":
		q.SortDir = "asc"
	case "asc", "desc":
	default:
		return apperr.Invalid("sort_dir must be asc or desc")
	}
	if q.Limit <= 0 {
		q.Limit = 10
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return nil
}

// UsedByMessage is what deleting something that a recipe still needs is refused with: up to five names.
func UsedByMessage(names []string, total int) string {
	msg := "Still used by: " + strings.Join(names, ", ")
	if total > len(names) {
		msg += fmt.Sprintf(" and %d more", total-len(names))
	}
	return msg
}
