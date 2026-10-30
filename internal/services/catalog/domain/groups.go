package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Variants and modifiers are reusable groups of options, linked to the services that offer them. A variant group is a
// choice of exactly one option (the speed, a size) and each option adds its price_delta to the service's price. A modifier
// group is a set of optional add-ons with a rule for how many may be picked, and each option adds its price.
const (
	MaxGroupOptions = 30
	MaxLinkedGroups = 20
	maxName         = 80
	maxNotes        = 500
)

// ServiceRef is a service a group is used by.
type ServiceRef struct {
	ID   int64
	Name string
}

type VariantOption struct {
	ID   int64 // 0 = a new option
	Name string
	// PriceDelta is added per unit to the service's price.
	PriceDelta        int64
	IsDefault         bool
	Active            bool
	SortIndex         int32
	EstimatedTime     int32
	EstimatedTimeUnit string
}

type VariantGroup struct {
	ID            int64
	Name          string
	InternalNotes string
	SortIndex     int32
	Options       []VariantOption
	// Services is the reverse lookup, filled when a group is read on its own.
	Services  []ServiceRef
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ModifierOption struct {
	ID          int64 // 0 = a new option
	Name        string
	Description string
	Price       int64
	Active      bool
	SortIndex   int32
}

type ModifierGroup struct {
	ID            int64
	Name          string
	InternalNotes string
	// Min 0 is an optional group, Min >= 1 a required one, Min == Max "pick exactly N". Max nil is no ceiling.
	Min       int32
	Max       *int32
	Options   []ModifierOption
	Services  []ServiceRef
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Required reports whether the customer has to pick something.
func (g ModifierGroup) Required() bool { return g.Min > 0 }

func cleanName(kind, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", apperr.Invalid(fmt.Sprintf("A %s needs a name", kind))
	}
	if len([]rune(name)) > maxName {
		return "", apperr.Invalid(fmt.Sprintf("A %s name can be at most %d characters", kind, maxName))
	}
	return name, nil
}

// Validate trims and checks a variant group before it is written, and settles which option is the default: the one the
// form marked, or the first active option when none was.
func (g *VariantGroup) Validate() error {
	var err error
	if g.Name, err = cleanName("variant group", g.Name); err != nil {
		return err
	}
	g.InternalNotes = strings.TrimSpace(g.InternalNotes)
	if len([]rune(g.InternalNotes)) > maxNotes {
		return apperr.Invalid("The note can be at most 500 characters")
	}
	if len(g.Options) == 0 {
		return apperr.Invalid("A variant group needs at least one option")
	}
	if len(g.Options) > MaxGroupOptions {
		return apperr.Invalid(fmt.Sprintf("A group can have at most %d options", MaxGroupOptions))
	}
	seen := map[string]bool{}
	defaults, firstActive := 0, -1
	for i := range g.Options {
		o := &g.Options[i]
		if o.Name, err = cleanName("variant option", o.Name); err != nil {
			return err
		}
		key := strings.ToLower(o.Name)
		if seen[key] {
			return apperr.Invalid(fmt.Sprintf("Two options are called %q", o.Name))
		}
		seen[key] = true
		if o.PriceDelta < 0 {
			return apperr.Invalid(fmt.Sprintf("The extra price of %q cannot be negative", o.Name))
		}
		if o.EstimatedTime < 0 {
			return apperr.Invalid(fmt.Sprintf("The time of %q cannot be negative", o.Name))
		}
		o.EstimatedTimeUnit = strings.TrimSpace(o.EstimatedTimeUnit)
		if o.IsDefault {
			defaults++
			if !o.Active {
				return apperr.Invalid(fmt.Sprintf("The default option %q has to be active", o.Name))
			}
		}
		if o.Active && firstActive < 0 {
			firstActive = i
		}
		o.SortIndex = int32(i + 1)
	}
	if defaults > 1 {
		return apperr.Invalid("Only one option can be the default")
	}
	if firstActive < 0 {
		return apperr.Invalid("A variant group needs at least one active option")
	}
	if defaults == 0 {
		g.Options[firstActive].IsDefault = true
	}
	return nil
}

// Validate trims and checks a modifier group before it is written.
func (g *ModifierGroup) Validate() error {
	var err error
	if g.Name, err = cleanName("modifier group", g.Name); err != nil {
		return err
	}
	g.InternalNotes = strings.TrimSpace(g.InternalNotes)
	if len([]rune(g.InternalNotes)) > maxNotes {
		return apperr.Invalid("The note can be at most 500 characters")
	}
	if len(g.Options) == 0 {
		return apperr.Invalid("A modifier group needs at least one option")
	}
	if len(g.Options) > MaxGroupOptions {
		return apperr.Invalid(fmt.Sprintf("A group can have at most %d options", MaxGroupOptions))
	}
	seen := map[string]bool{}
	active := int32(0)
	for i := range g.Options {
		o := &g.Options[i]
		if o.Name, err = cleanName("modifier option", o.Name); err != nil {
			return err
		}
		key := strings.ToLower(o.Name)
		if seen[key] {
			return apperr.Invalid(fmt.Sprintf("Two options are called %q", o.Name))
		}
		seen[key] = true
		if o.Price < 0 {
			return apperr.Invalid(fmt.Sprintf("The price of %q cannot be negative", o.Name))
		}
		o.Description = strings.TrimSpace(o.Description)
		if o.Active {
			active++
		}
		o.SortIndex = int32(i + 1)
	}
	switch {
	case g.Min < 0:
		return apperr.Invalid("The minimum cannot be negative")
	case g.Max != nil && *g.Max < 1:
		return apperr.Invalid("The maximum has to be at least 1, or left empty for no limit")
	case g.Max != nil && *g.Max < g.Min:
		return apperr.Invalid("The maximum cannot be below the minimum")
	case g.Min > active:
		return apperr.Invalid(fmt.Sprintf("The minimum is %d but only %d options are active", g.Min, active))
	}
	return nil
}

// validateLinks checks the ordered group ids a service is saved with: no repeats, and not an absurd number.
func validateLinks(ids []int64, kind string) error {
	if len(ids) > MaxLinkedGroups {
		return apperr.Invalid(fmt.Sprintf("A service can have at most %d %s groups", MaxLinkedGroups, kind))
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 {
			return apperr.Invalid(fmt.Sprintf("A %s group id is invalid", kind))
		}
		if seen[id] {
			return apperr.Invalid(fmt.Sprintf("The same %s group is listed twice", kind))
		}
		seen[id] = true
	}
	return nil
}

// UsedBy is the message a group that is still in use is refused deletion with.
func UsedBy(services []ServiceRef) string {
	names := make([]string, 0, len(services))
	for _, s := range services {
		names = append(names, s.Name)
	}
	return "Still used by: " + strings.Join(names, ", ")
}

// GroupQuery filters and orders the list of variant or modifier groups.
type GroupQuery struct {
	// Q matches, ignoring case, the group's name, its note or the name of any option.
	Q string
	// SortBy is one of GroupSortFields; empty is "id". SortDir is "asc" (the default) or "desc".
	SortBy, SortDir string
	// Requirement is "required", "optional" or empty for all. Modifier groups only.
	Requirement string
	// Limit is the page size (default 10, at most 100) and Offset where the page starts.
	Limit, Offset int
	// ServiceID keeps only the groups linked to that service (0 = no filter). Usage is "used" (linked to at least one service),
	// "unused" (linked to none) or empty for all.
	ServiceID int64
	Usage     string
}

// GroupSortFields are the columns a list may be ordered by; the store maps each to its SQL.
var GroupSortFields = map[string]bool{"id": true, "name": true, "created_at": true, "updated_at": true}

// Validate trims Q and fills the defaults. modifiers says whether Requirement applies.
func (q *GroupQuery) Validate(modifiers bool) error {
	q.Q = strings.TrimSpace(q.Q)
	if len([]rune(q.Q)) > 64 {
		return apperr.Invalid("The search can be at most 64 characters")
	}
	if q.SortBy == "" {
		q.SortBy = "id"
	}
	if !GroupSortFields[q.SortBy] {
		return apperr.Invalid("sort_by must be one of id, name, created_at, updated_at")
	}
	switch q.SortDir {
	case "":
		q.SortDir = "asc"
	case "asc", "desc":
	default:
		return apperr.Invalid("sort_dir must be asc or desc")
	}
	switch q.Requirement {
	case "", "required", "optional":
	default:
		return apperr.Invalid("requirement must be required or optional")
	}
	if q.ServiceID < 0 {
		return apperr.Invalid("service_id must be a positive number")
	}
	switch q.Usage {
	case "", "used", "unused":
	default:
		return apperr.Invalid("usage must be used or unused")
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
	if !modifiers && q.Requirement != "" {
		return apperr.Invalid("requirement only applies to modifier groups")
	}
	return nil
}
