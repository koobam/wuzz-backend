package domain

import (
	"strings"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// The paged, searchable lists of outlets and services (the back office's tables). They are asked for with a limit; without one
// the plain list is given, which the dashboard's map and the pickers read whole.

// OutletQuery filters and orders a page of outlets.
type OutletQuery struct {
	// Q matches, ignoring case, part of the name, the address or the phone.
	Q string
	// Active is "", "true" or "false".
	Active          string
	SortBy, SortDir string
	Limit, Offset   int
}

// OutletSortFields are the columns an outlet list may be ordered by.
var OutletSortFields = map[string]bool{"id": true, "name": true, "address": true, "phone": true, "active": true}

func (q *OutletQuery) Validate() error {
	if err := validateList(&q.Q, &q.SortBy, &q.SortDir, &q.Limit, &q.Offset, OutletSortFields, "id, name, address, phone, active"); err != nil {
		return err
	}
	return validateTriState(q.Active, "active")
}

// ServiceQuery filters and orders a page of services.
type ServiceQuery struct {
	// Q matches, ignoring case, part of the name, the description or the category's name.
	Q string
	// CategoryID keeps one category's services (0 = no filter); Unit one unit ("" = any); Active is "", "true" or "false".
	CategoryID int64
	Unit       string
	Active     string
	// TaxType keeps one tax type's services ("" = any).
	TaxType string
	// DiscountType keeps the services with one kind of discount ("" = any): none, value or percent.
	DiscountType    string
	SortBy, SortDir string
	Limit, Offset   int
}

// ServiceSortFields are the columns a service list may be ordered by.
var ServiceSortFields = map[string]bool{"id": true, "name": true, "unit": true, "price": true, "category_id": true, "active": true}

func (q *ServiceQuery) Validate() error {
	if err := validateList(&q.Q, &q.SortBy, &q.SortDir, &q.Limit, &q.Offset, ServiceSortFields, "id, name, unit, price, category_id, active"); err != nil {
		return err
	}
	if q.CategoryID < 0 {
		return apperr.Invalid("category_id must be a positive number")
	}
	if q.Unit != "" && !oneOf([]string{UnitKg, UnitPcs, UnitSession, UnitUnit}, q.Unit) {
		return apperr.Invalid("unit must be kg, pcs, sesi or unit")
	}
	if q.TaxType != "" && !validTaxType(q.TaxType) {
		return apperr.Invalid("tax_type must be none or ppn")
	}
	if q.DiscountType != "" && !validDiscountType(q.DiscountType) {
		return apperr.Invalid("discount_type must be none, value or percent")
	}
	return validateTriState(q.Active, "active")
}

func validateTriState(v, name string) error {
	if v != "" && v != "true" && v != "false" {
		return apperr.Invalid(name + " must be true or false")
	}
	return nil
}

// validateList is what every list query shares: the trimmed search, a known sort, and a page of 1 to 100 rows.
func validateList(q, sortBy, sortDir *string, limit, offset *int, fields map[string]bool, names string) error {
	*q = strings.TrimSpace(*q)
	if len([]rune(*q)) > 64 {
		return apperr.Invalid("The search can be at most 64 characters")
	}
	if *sortBy == "" {
		*sortBy = "id"
	}
	if !fields[*sortBy] {
		return apperr.Invalid("sort_by must be one of " + names)
	}
	switch *sortDir {
	case "":
		*sortDir = "asc"
	case "asc", "desc":
	default:
		return apperr.Invalid("sort_dir must be asc or desc")
	}
	if *limit <= 0 {
		*limit = 10
	}
	if *limit > 100 {
		*limit = 100
	}
	if *offset < 0 {
		*offset = 0
	}
	return nil
}
