package domain

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// A bill of materials (BOM) says which materials, and how much of each, an owner consumes. The owner is a service, a
// variant option or a modifier option: a laundry order line consumes its service's lines plus those of the options chosen.
const (
	OwnerService        = "service"
	OwnerVariantOption  = "variant_option"
	OwnerModifierOption = "modifier_option"

	// PerUnit lines scale with how much is sold (per kg, per pcs); PerLine lines are used once per order line.
	PerUnit = "unit"
	PerLine = "line"

	MaxBOMLines = 40
)

// BOMLine is one material in a recipe. Qty is in the material's base unit; the effective quantity is Qty x (1 + loss/100).
type BOMLine struct {
	MaterialID  int64
	SKU, Name   string
	Unit        string
	Qty         *big.Rat
	Per         string
	LossPercent *big.Rat
	// UnitCost is the material's price per base unit as an exact fraction, set when the line is loaded.
	UnitCost  *big.Rat
	SortIndex int32
}

var ratOne = big.NewRat(1, 1)
var ratHundred = big.NewRat(100, 1)

// EffectiveQty is Qty with the standard waste added: qty x (1 + loss/100), exact.
func (l BOMLine) EffectiveQty() *big.Rat {
	factor := new(big.Rat).Quo(l.LossPercent, ratHundred)
	factor.Add(factor, ratOne)
	return factor.Mul(factor, l.Qty)
}

// Used is how many base units this line consumes when qtySold units are sold: scaled for per-unit lines, once for per-line ones.
func (l BOMLine) Used(qtySold *big.Rat) *big.Rat {
	q := l.EffectiveQty()
	if l.Per == PerUnit {
		q.Mul(q, qtySold)
	}
	return q
}

// CostOf is the exact cost of Used(qtySold) base units.
func (l BOMLine) CostOf(qtySold *big.Rat) *big.Rat {
	return new(big.Rat).Mul(l.Used(qtySold), l.UnitCost)
}

// SumByPer adds up, exactly, what one unit sold costs (the per-unit lines) and what an order line costs once (the
// per-line lines). Neither is rounded.
func SumByPer(lines []BOMLine) (perUnit, perLine *big.Rat) {
	perUnit, perLine = new(big.Rat), new(big.Rat)
	for _, l := range lines {
		c := l.CostOf(ratOne)
		if l.Per == PerUnit {
			perUnit.Add(perUnit, c)
		} else {
			perLine.Add(perLine, c)
		}
	}
	return perUnit, perLine
}

// BOMLineInput is a recipe line as a form posts it.
type BOMLineInput struct {
	MaterialID  int64
	Qty         string
	Per         string
	LossPercent string // "" = 0
}

// ParseBOM checks the posted lines of a recipe and reads their numbers exactly. It does not look at the materials.
func ParseBOM(in []BOMLineInput) ([]BOMLine, error) {
	if len(in) > MaxBOMLines {
		return nil, apperr.Invalid(fmt.Sprintf("A recipe can have at most %d lines", MaxBOMLines))
	}
	seen := map[int64]bool{}
	out := make([]BOMLine, 0, len(in))
	for i, l := range in {
		if l.MaterialID <= 0 {
			return nil, apperr.Invalid(fmt.Sprintf("Line %d needs a material", i+1))
		}
		if seen[l.MaterialID] {
			return nil, apperr.Invalid("The same material is listed twice")
		}
		seen[l.MaterialID] = true
		if l.Per != PerUnit && l.Per != PerLine {
			return nil, apperr.Invalid(fmt.Sprintf("Line %d: per must be unit or line", i+1))
		}
		qty, err := ParseDecimal(fmt.Sprintf("Line %d: the quantity", i+1), l.Qty, 4)
		if err != nil {
			return nil, err
		}
		if qty.Sign() <= 0 {
			return nil, apperr.Invalid(fmt.Sprintf("Line %d: the quantity must be more than 0", i+1))
		}
		loss := new(big.Rat)
		if l.LossPercent != "" {
			if loss, err = ParseDecimal(fmt.Sprintf("Line %d: the loss percent", i+1), l.LossPercent, 2); err != nil {
				return nil, err
			}
			if loss.Cmp(ratHundred) > 0 {
				return nil, apperr.Invalid(fmt.Sprintf("Line %d: the loss percent cannot be more than 100", i+1))
			}
		}
		out = append(out, BOMLine{MaterialID: l.MaterialID, Qty: qty, Per: l.Per, LossPercent: loss, SortIndex: int32(i + 1)})
	}
	return out, nil
}

// ---- costing a sale ----

// CostingContext is everything a costing needs, loaded once: the service with its linked groups, the recipe of the
// service and the recipes of every option of those groups.
type CostingContext struct {
	Service ServiceItem
	Base    []BOMLine
	// Variant and Modifier map an option id to its recipe.
	Variant, Modifier map[int64][]BOMLine
}

// CalcInput is a sale to cost: the options chosen and how many units.
type CalcInput struct {
	VariantOptionIDs, ModifierOptionIDs []int64
	Qty                                 string
}

// CostedLine is one material line of a costed sale.
type CostedLine struct {
	Source     string // service | variant | modifier
	SourceName string
	Line       BOMLine
	QtyUsed    *big.Rat
	Cost       *big.Rat
}

// CalcResult is the exact breakdown of a sale. Revenue and Cost are whole rupiah, each rounded once.
type CalcResult struct {
	Qty         *big.Rat
	UnitPrice   int64
	Revenue     int64
	Lines       []CostedLine
	Cost        int64
	GrossProfit int64
	// MarginPercent is gross profit over revenue x 100, rounded to 2 places half away from zero ("0.00" without revenue).
	MarginPercent string
}

// Chosen is the options a sale resolved to, with the group each belongs to.
type Chosen struct {
	Variant  []ChosenOption
	Modifier []ChosenOption
}

type ChosenOption struct {
	ID    int64
	Group string
	Name  string
	// Delta is the variant's extra price or the modifier's price, whole rupiah.
	Delta int64
}

// Resolve checks the options chosen for a service and fills in the defaults: an option must belong to a group linked to the
// service and be active; a variant group (pick exactly one) with nothing chosen takes its default; at most one variant per
// group; a modifier group's min and max are respected; nothing is listed twice.
func Resolve(s ServiceItem, variantIDs, modifierIDs []int64) (Chosen, error) {
	var out Chosen
	if err := noDuplicates(variantIDs, "variant"); err != nil {
		return out, err
	}
	if err := noDuplicates(modifierIDs, "modifier"); err != nil {
		return out, err
	}
	asked := map[int64]bool{}
	for _, id := range variantIDs {
		asked[id] = true
	}
	for _, g := range s.VariantGroups {
		var picked []ChosenOption
		var fallback *VariantOption
		for i := range g.Options {
			o := g.Options[i]
			if !o.Active {
				continue
			}
			// What a group with nothing chosen takes: its default option, else its first active one.
			if fallback == nil || o.IsDefault {
				fallback = &g.Options[i]
			}
			if asked[o.ID] {
				picked = append(picked, ChosenOption{ID: o.ID, Group: g.Name, Name: o.Name, Delta: o.PriceDelta})
				delete(asked, o.ID)
			}
		}
		switch {
		case len(picked) > 1:
			return out, apperr.Invalid(fmt.Sprintf("Pick only one option of %q", g.Name))
		case len(picked) == 0 && fallback != nil:
			picked = append(picked, ChosenOption{ID: fallback.ID, Group: g.Name, Name: fallback.Name, Delta: fallback.PriceDelta})
		}
		out.Variant = append(out.Variant, picked...)
	}
	for id := range asked {
		return out, apperr.Invalid(fmt.Sprintf("Variant option %d is not offered for this service", id))
	}

	asked = map[int64]bool{}
	for _, id := range modifierIDs {
		asked[id] = true
	}
	for _, g := range s.ModifierGroups {
		n := 0
		for _, o := range g.Options {
			if o.Active && asked[o.ID] {
				out.Modifier = append(out.Modifier, ChosenOption{ID: o.ID, Group: g.Name, Name: o.Name, Delta: o.Price})
				delete(asked, o.ID)
				n++
			}
		}
		if int32(n) < g.Min {
			return out, apperr.Invalid(fmt.Sprintf("Pick at least %d of %q", g.Min, g.Name))
		}
		if g.Max != nil && int32(n) > *g.Max {
			return out, apperr.Invalid(fmt.Sprintf("Pick at most %d of %q", *g.Max, g.Name))
		}
	}
	for id := range asked {
		return out, apperr.Invalid(fmt.Sprintf("Modifier option %d is not offered for this service", id))
	}
	return out, nil
}

func noDuplicates(ids []int64, kind string) error {
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 {
			return apperr.Invalid(fmt.Sprintf("A %s option id is invalid", kind))
		}
		if seen[id] {
			return apperr.Invalid(fmt.Sprintf("The same %s option is listed twice", kind))
		}
		seen[id] = true
	}
	return nil
}

// Calc costs a sale exactly. The unit price is the service's price after its discount (and before tax: tax is the state's,
// not revenue) plus the variants' extras plus the modifiers' prices (integer rupiah). Every material line contributes an exact rational; they are added and rounded to rupiah once.
func Calc(c CostingContext, in CalcInput) (CalcResult, error) {
	var out CalcResult
	qty, err := ParseDecimal("The quantity", in.Qty, 4)
	if err != nil {
		return out, err
	}
	if qty.Sign() <= 0 {
		return out, apperr.Invalid("The quantity must be more than 0")
	}
	chosen, err := Resolve(c.Service, in.VariantOptionIDs, in.ModifierOptionIDs)
	if err != nil {
		return out, err
	}
	out.Qty = qty
	out.UnitPrice = c.Service.Price - DiscountAmountOf(c.Service.Price, c.Service.DiscountType, c.Service.DiscountValue)
	for _, o := range chosen.Variant {
		out.UnitPrice += o.Delta
	}
	for _, o := range chosen.Modifier {
		out.UnitPrice += o.Delta
	}
	out.Revenue = RoundRupiah(new(big.Rat).Mul(new(big.Rat).SetInt64(out.UnitPrice), qty))

	total := new(big.Rat)
	add := func(source, name string, lines []BOMLine) {
		for _, l := range lines {
			used := l.Used(qty)
			cost := new(big.Rat).Mul(used, l.UnitCost)
			total.Add(total, cost)
			out.Lines = append(out.Lines, CostedLine{Source: source, SourceName: name, Line: l, QtyUsed: used, Cost: cost})
		}
	}
	add("service", c.Service.Name, c.Base)
	for _, o := range chosen.Variant {
		add("variant", o.Group+" / "+o.Name, c.Variant[o.ID])
	}
	for _, o := range chosen.Modifier {
		add("modifier", o.Group+" / "+o.Name, c.Modifier[o.ID])
	}
	out.Cost = RoundRupiah(total)
	out.GrossProfit = out.Revenue - out.Cost
	out.MarginPercent = "0.00"
	if out.Revenue != 0 {
		m := new(big.Rat).SetFrac64(out.GrossProfit, out.Revenue)
		m.Mul(m, ratHundred)
		out.MarginPercent = FormatRat(m, 2)
	}
	return out, nil
}

// MaterialTotals is what a set of lines uses in total, per material, across a whole order: the exact quantities added
// before anything is rounded. The stock ledger rounds each material's total once.
func MaterialTotals(lines []CostedLine) map[int64]*big.Rat {
	out := map[int64]*big.Rat{}
	for _, l := range lines {
		if out[l.Line.MaterialID] == nil {
			out[l.Line.MaterialID] = new(big.Rat)
		}
		out[l.Line.MaterialID].Add(out[l.Line.MaterialID], l.QtyUsed)
	}
	return out
}

// SortedMaterialIDs lists the keys of totals in ascending order, so movements are written in a stable order.
func SortedMaterialIDs(totals map[int64]*big.Rat) []int64 {
	ids := make([]int64, 0, len(totals))
	for id := range totals {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// OwnerName is "<group> / <option>" or a service name; a tiny helper for messages.
func OwnerName(parts ...string) string { return strings.Join(parts, " / ") }
