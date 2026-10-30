package domain

import (
	"strings"
	"testing"
)

func vopt(name string, delta int64, active, def bool) VariantOption {
	return VariantOption{Name: name, PriceDelta: delta, Active: active, IsDefault: def}
}

func TestVariantGroupDefaultsToFirstActive(t *testing.T) {
	g := VariantGroup{Name: " Speed ", Options: []VariantOption{vopt("Off", 0, false, false), vopt("Reguler", 0, true, false), vopt("Express", 3000, true, false)}}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if g.Name != "Speed" || g.Options[0].IsDefault || !g.Options[1].IsDefault || g.Options[2].IsDefault || g.Options[2].SortIndex != 3 {
		t.Errorf("group = %+v", g)
	}
}

func TestVariantGroupRejected(t *testing.T) {
	cases := map[string]VariantGroup{
		"no name":           {Name: " ", Options: []VariantOption{vopt("A", 0, true, false)}},
		"no options":        {Name: "G"},
		"too many options":  {Name: "G", Options: make([]VariantOption, MaxGroupOptions+1)},
		"duplicate names":   {Name: "G", Options: []VariantOption{vopt("A", 0, true, false), vopt("a", 0, true, false)}},
		"negative extra":    {Name: "G", Options: []VariantOption{vopt("A", -1, true, false)}},
		"two defaults":      {Name: "G", Options: []VariantOption{vopt("A", 0, true, true), vopt("B", 0, true, true)}},
		"inactive default":  {Name: "G", Options: []VariantOption{vopt("A", 0, false, true), vopt("B", 0, true, false)}},
		"none active":       {Name: "G", Options: []VariantOption{vopt("A", 0, false, false)}},
		"long name":         {Name: strings.Repeat("x", 81), Options: []VariantOption{vopt("A", 0, true, false)}},
		"blank option name": {Name: "G", Options: []VariantOption{vopt("  ", 0, true, false)}},
		"negative time":     {Name: "G", Options: []VariantOption{{Name: "A", Active: true, EstimatedTime: -1}}},
		"note too long":     {Name: "G", InternalNotes: strings.Repeat("x", 501), Options: []VariantOption{vopt("A", 0, true, false)}},
	}
	for name, g := range cases {
		if err := g.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func mopt(name string, price int64, active bool) ModifierOption {
	return ModifierOption{Name: name, Price: price, Active: active}
}

func TestModifierGroupRule(t *testing.T) {
	one, two, zero := int32(1), int32(2), int32(0)
	opts := []ModifierOption{mopt("A", 0, true), mopt("B", 1000, true), mopt("C", 0, false)}
	ok := map[string]struct {
		min int32
		max *int32
	}{"optional, no ceiling": {0, nil}, "pick exactly one": {1, &one}, "up to two": {0, &two}, "exactly two": {2, &two}, "required, no ceiling": {2, nil}}
	for name, r := range ok {
		g := ModifierGroup{Name: "G", Min: r.min, Max: r.max, Options: append([]ModifierOption{}, opts...)}
		if err := g.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]struct {
		min int32
		max *int32
	}{"negative min": {-1, nil}, "max zero": {0, &zero}, "max below min": {2, &one}, "min above the active options": {3, nil}}
	for name, r := range bad {
		g := ModifierGroup{Name: "G", Min: r.min, Max: r.max, Options: append([]ModifierOption{}, opts...)}
		if err := g.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if (ModifierGroup{Min: 1}).Required() != true || (ModifierGroup{}).Required() {
		t.Error("Required follows min")
	}
}

func TestModifierGroupRejected(t *testing.T) {
	cases := map[string]ModifierGroup{
		"no name":         {Name: "", Options: []ModifierOption{mopt("A", 0, true)}},
		"no options":      {Name: "G"},
		"duplicate names": {Name: "G", Options: []ModifierOption{mopt("A", 0, true), mopt(" a ", 0, true)}},
		"negative price":  {Name: "G", Options: []ModifierOption{mopt("A", -5, true)}},
	}
	for name, g := range cases {
		if err := g.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestServiceLinksValidated(t *testing.T) {
	good := ServiceItem{Name: "S", Unit: UnitKg, TaxType: TaxNone, DiscountType: DiscountNone, VariantGroupIDs: []int64{1, 2}, ModifierGroupIDs: []int64{}}
	if err := good.Validate(); err != nil {
		t.Errorf("good links: %v", err)
	}
	for name, s := range map[string]ServiceItem{
		"repeated variant group": {Name: "S", Unit: UnitKg, TaxType: TaxNone, DiscountType: DiscountNone, VariantGroupIDs: []int64{1, 1}},
		"repeated modifier":      {Name: "S", Unit: UnitKg, TaxType: TaxNone, DiscountType: DiscountNone, ModifierGroupIDs: []int64{3, 4, 3}},
		"zero id":                {Name: "S", Unit: UnitKg, TaxType: TaxNone, DiscountType: DiscountNone, ModifierGroupIDs: []int64{0}},
		"too many": {Name: "S", Unit: UnitKg, TaxType: TaxNone, DiscountType: DiscountNone, VariantGroupIDs: func() []int64 {
			ids := make([]int64, MaxLinkedGroups+1)
			for i := range ids {
				ids[i] = int64(i + 1)
			}
			return ids
		}()},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestUsedBy(t *testing.T) {
	if got := UsedBy([]ServiceRef{{1, "Cuci Pakaian Kiloan"}, {2, "Dryer"}}); got != "Still used by: Cuci Pakaian Kiloan, Dryer" {
		t.Errorf("got %q", got)
	}
}

func TestGroupQueryDefaultsAndLimits(t *testing.T) {
	q := GroupQuery{Q: "  kec  "}
	if err := q.Validate(true); err != nil {
		t.Fatal(err)
	}
	if q.Q != "kec" || q.SortBy != "id" || q.SortDir != "asc" || q.Limit != 10 || q.Offset != 0 {
		t.Errorf("defaults = %+v", q)
	}
	big := GroupQuery{Limit: 5000, Offset: -4, SortBy: "updated_at", SortDir: "desc", Requirement: "required"}
	if err := big.Validate(true); err != nil || big.Limit != 100 || big.Offset != 0 {
		t.Errorf("clamped = %+v, %v", big, err)
	}
}

func TestGroupQueryRejected(t *testing.T) {
	cases := map[string]struct {
		q         GroupQuery
		modifiers bool
	}{
		"unknown sort":           {GroupQuery{SortBy: "min; drop table"}, true},
		"unknown direction":      {GroupQuery{SortDir: "sideways"}, true},
		"unknown requirement":    {GroupQuery{Requirement: "maybe"}, true},
		"requirement on variant": {GroupQuery{Requirement: "required"}, false},
		"long search":            {GroupQuery{Q: strings.Repeat("x", 65)}, true},
	}
	for name, c := range cases {
		if err := c.q.Validate(c.modifiers); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestGroupQueryServiceAndUsage(t *testing.T) {
	ok := GroupQuery{ServiceID: 8, Usage: "used"}
	if err := ok.Validate(false); err != nil {
		t.Errorf("service and usage: %v", err)
	}
	for name, q := range map[string]GroupQuery{
		"negative service": {ServiceID: -1},
		"unknown usage":    {Usage: "sometimes"},
	} {
		if err := q.Validate(true); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
