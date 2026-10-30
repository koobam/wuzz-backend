package domain

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// The stock ledger and its valuation.
//
// Stock is kept per (outlet, material) as a quantity (exact, at most 4 decimal places) and a value (whole rupiah). The
// valuation is a MOVING WEIGHTED AVERAGE: what a unit is worth is value / qty, an exact fraction that is never stored or
// rounded. Rupiah appear only as the rounded value_delta of a movement, and each movement rounds once, half away from zero:
//
//   - receipt: value_delta is the total cost paid, as entered.
//   - consumption, waste, transfer out, a negative adjustment (anything that takes qty out): the value that leaves is
//     qty x value / qty_on_hand, rounded. When the movement takes the balance to exactly 0 it takes the ENTIRE remaining value,
//     so value never drifts and a zero balance has zero value. Taking more than is on hand (only automatic consumption may)
//     takes the on-hand value, and values the part that is not there at the material's standard unit cost.
//   - a positive adjustment (the count found more than the book): valued at the current average or, with nothing on hand,
//     at the standard unit cost.
//   - transfer in: takes the value the matching transfer out moved. A reversal of a consumption restores exactly what that
//     consumption took.
//
// While qty <= 0 the average is undefined. A movement that has to value units while the balance is empty or negative uses the
// material's STANDARD unit cost, purchase_price / purchase_qty as an exact fraction rounded half away from zero, and the balance
// value then goes negative accordingly. The single exception to "a zero balance has zero value": a receipt that lands exactly on
// 0 from a negative balance keeps the cost it was entered with, so the difference between the standard cost used and the price
// actually paid stays visible in the ledger instead of being written off silently.

// Movement kinds.
const (
	KindReceipt     = "receipt"
	KindConsumption = "consumption"
	KindReversal    = "consumption_reversal"
	KindWaste       = "waste"
	KindAdjustment  = "adjustment"
	KindTransferIn  = "transfer_in"
	KindTransferOut = "transfer_out"

	ReasonCountCorrection = "count_correction"

	RefOrder    = "order"
	RefTransfer = "transfer"
)

var (
	MovementKinds = []string{KindReceipt, KindConsumption, KindReversal, KindWaste, KindAdjustment, KindTransferIn, KindTransferOut}
	WasteReasons  = []string{"expired", "damaged", "spilled", "lost", "other"}
)

// StockState is a balance: the quantity and what it is worth.
type StockState struct {
	Qty   *big.Rat
	Value int64
}

// Plan is a movement worked out against a balance: the signed quantity and the signed value that change it.
type Plan struct {
	QtyDelta   *big.Rat
	ValueDelta int64
}

// After is the balance once the plan is applied.
func (s StockState) After(p Plan) StockState {
	return StockState{Qty: new(big.Rat).Add(s.Qty, p.QtyDelta), Value: s.Value + p.ValueDelta}
}

// OnHand is the quantity that can be taken out: 0 when the balance is empty or negative.
func (s StockState) OnHand() *big.Rat {
	if s.Qty.Sign() <= 0 {
		return new(big.Rat)
	}
	return s.Qty
}

// share is qty x value / onHand, the exact value of qty units at the average.
func share(qty *big.Rat, value int64, onHand *big.Rat) *big.Rat {
	r := new(big.Rat).Mul(qty, new(big.Rat).SetInt64(value))
	return r.Quo(r, onHand)
}

func neg(v int64) int64 { return -v }

// PlanReceipt books goods received: qty in, the total cost paid as the value.
func PlanReceipt(_ StockState, qty *big.Rat, totalCost int64) Plan {
	return Plan{QtyDelta: new(big.Rat).Set(qty), ValueDelta: totalCost}
}

// PlanOut works out taking qty out of the balance. std is the material's standard unit cost, used only for the part that is not
// on hand. It does not refuse to go negative; PlanOutManual does.
func PlanOut(b StockState, qty, std *big.Rat) Plan {
	p := Plan{QtyDelta: new(big.Rat).Neg(qty)}
	switch {
	case b.Qty.Sign() <= 0:
		// Nothing on hand to average over: every unit is valued at the standard cost.
		p.ValueDelta = neg(RoundRupiah(new(big.Rat).Mul(qty, std)))
	case qty.Cmp(b.Qty) == 0:
		p.ValueDelta = neg(b.Value) // the whole remaining value, so a zero balance is worth nothing
	case qty.Cmp(b.Qty) < 0:
		p.ValueDelta = neg(RoundRupiah(share(qty, b.Value, b.Qty)))
	default:
		// More than is on hand: all of the value on hand, plus the missing part at the standard cost.
		missing := new(big.Rat).Sub(qty, b.Qty)
		p.ValueDelta = neg(b.Value + RoundRupiah(new(big.Rat).Mul(missing, std)))
	}
	return p
}

// PlanOutManual is PlanOut for movements a person books (waste, transfer out): they cannot take more than is on hand.
func PlanOutManual(b StockState, qty, std *big.Rat, unit string) (Plan, error) {
	if qty.Cmp(b.OnHand()) > 0 {
		return Plan{}, apperr.Invalid(fmt.Sprintf("Only %s %s in stock", FormatExact(b.OnHand(), 4), unit))
	}
	return PlanOut(b, qty, std), nil
}

// PlanAdjustUp books a count that found qty more than the book: valued at the current average, or at the standard cost with nothing
// on hand. A balance that comes back to exactly 0 has its (negative) carried value cleared.
func PlanAdjustUp(b StockState, qty, std *big.Rat) Plan {
	p := Plan{QtyDelta: new(big.Rat).Set(qty)}
	switch {
	case new(big.Rat).Add(b.Qty, qty).Sign() == 0:
		p.ValueDelta = neg(b.Value)
	case b.Qty.Sign() > 0:
		p.ValueDelta = RoundRupiah(share(qty, b.Value, b.Qty))
	default:
		p.ValueDelta = RoundRupiah(new(big.Rat).Mul(qty, std))
	}
	return p
}

// StockRow is a balance as listed.
type StockRow struct {
	OutletID, MaterialID int64
	SKU, Name, Unit      string
	Qty                  *big.Rat
	Value                int64
	MinQty               *big.Rat
	// StdUnitCost is the material's purchase_price / purchase_qty, exact.
	StdUnitCost *big.Rat
}

// AvgCost is value / qty, exact. With nothing on hand the average is undefined and the standard unit cost is shown instead.
func (r StockRow) AvgCost() *big.Rat {
	if r.Qty.Sign() <= 0 {
		return new(big.Rat).Set(r.StdUnitCost)
	}
	return new(big.Rat).Quo(new(big.Rat).SetInt64(r.Value), r.Qty)
}

// Low is true when a reorder level is set and the stock has reached it.
func (r StockRow) Low() bool { return r.MinQty.Sign() > 0 && r.Qty.Cmp(r.MinQty) <= 0 }

// StockMovement is a row of the ledger.
type StockMovement struct {
	ID                   int64
	OutletID, MaterialID int64
	SKU, Name, Unit      string
	Kind, Reason         string
	QtyDelta             *big.Rat
	ValueDelta           int64
	BalanceQty           *big.Rat
	BalanceValue         int64
	RefType, RefID       string
	// RefCode is the TRX code of the order a movement came from ("" for any other, and for movements older than the column).
	RefCode         string
	Supplier, DocNo string
	Note, CreatedBy string
	CreatedAt       time.Time
	IdempotencyKey  string
}

// ---- requests ----

const (
	maxSupplier = 80
	maxDocNo    = 40
	maxNote     = 500
	maxKey      = 100
)

// StockReceiptInput is goods received, as posted.
type StockReceiptInput struct {
	OutletID, MaterialID int64
	Qty                  string
	TotalCost            int64
	Supplier, DocNo      string
	Note, Key            string
}

type StockReceipt struct {
	OutletID, MaterialID int64
	Qty                  *big.Rat
	TotalCost            int64
	Supplier, DocNo      string
	Note, Key            string
}

func trimmed(label, v string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if len([]rune(v)) > max {
		return "", apperr.Invalid(fmt.Sprintf("%s can be at most %d characters", label, max))
	}
	return v, nil
}

func stockQty(in string) (*big.Rat, error) {
	q, err := ParseDecimal("The quantity", in, 4)
	if err != nil {
		return nil, err
	}
	if q.Sign() <= 0 {
		return nil, apperr.Invalid("The quantity must be more than 0")
	}
	return q, nil
}

func ids(outlet, material int64) error {
	if outlet <= 0 {
		return apperr.Invalid("An outlet is required")
	}
	if material <= 0 {
		return apperr.Invalid("A material is required")
	}
	return nil
}

func (in StockReceiptInput) Parse() (StockReceipt, error) {
	var r StockReceipt
	if err := ids(in.OutletID, in.MaterialID); err != nil {
		return r, err
	}
	qty, err := stockQty(in.Qty)
	if err != nil {
		return r, err
	}
	if in.TotalCost < 0 {
		return r, apperr.Invalid("The total cost cannot be negative")
	}
	r = StockReceipt{OutletID: in.OutletID, MaterialID: in.MaterialID, Qty: qty, TotalCost: in.TotalCost}
	if r.Supplier, err = trimmed("The supplier", in.Supplier, maxSupplier); err != nil {
		return r, err
	}
	if r.DocNo, err = trimmed("The document number", in.DocNo, maxDocNo); err != nil {
		return r, err
	}
	if r.Note, err = trimmed("The note", in.Note, maxNote); err != nil {
		return r, err
	}
	r.Key, err = trimmed("The idempotency key", in.Key, maxKey)
	return r, err
}

// StockWasteInput is stock written off.
type StockWasteInput struct {
	OutletID, MaterialID int64
	Qty, Reason          string
	Note, Key            string
}

type StockWaste struct {
	OutletID, MaterialID int64
	Qty                  *big.Rat
	Reason, Note, Key    string
}

func (in StockWasteInput) Parse() (StockWaste, error) {
	var w StockWaste
	if err := ids(in.OutletID, in.MaterialID); err != nil {
		return w, err
	}
	qty, err := stockQty(in.Qty)
	if err != nil {
		return w, err
	}
	if !oneOf(WasteReasons, in.Reason) {
		return w, apperr.Invalid("The reason must be expired, damaged, spilled, lost or other")
	}
	w = StockWaste{OutletID: in.OutletID, MaterialID: in.MaterialID, Qty: qty, Reason: in.Reason}
	if w.Note, err = trimmed("The note", in.Note, maxNote); err != nil {
		return w, err
	}
	w.Key, err = trimmed("The idempotency key", in.Key, maxKey)
	return w, err
}

// StockAdjustInput is a stock count: what was counted on the shelf.
type StockAdjustInput struct {
	OutletID, MaterialID int64
	CountedQty           string
	Note, Key            string
}

type StockAdjust struct {
	OutletID, MaterialID int64
	CountedQty           *big.Rat
	Note, Key            string
}

func (in StockAdjustInput) Parse() (StockAdjust, error) {
	var a StockAdjust
	if err := ids(in.OutletID, in.MaterialID); err != nil {
		return a, err
	}
	counted, err := ParseDecimal("The counted quantity", in.CountedQty, 4)
	if err != nil {
		return a, err
	}
	a = StockAdjust{OutletID: in.OutletID, MaterialID: in.MaterialID, CountedQty: counted}
	if a.Note, err = trimmed("The note", in.Note, maxNote); err != nil {
		return a, err
	}
	if a.Note == "" {
		return a, apperr.Invalid("A stock count needs a note")
	}
	a.Key, err = trimmed("The idempotency key", in.Key, maxKey)
	return a, err
}

// StockTransferInput moves stock from one outlet to another.
type StockTransferInput struct {
	FromOutletID, ToOutletID, MaterialID int64
	Qty                                  string
	Note, Key                            string
}

type StockTransfer struct {
	FromOutletID, ToOutletID, MaterialID int64
	Qty                                  *big.Rat
	Note, Key                            string
}

func (in StockTransferInput) Parse() (StockTransfer, error) {
	var t StockTransfer
	if err := ids(in.FromOutletID, in.MaterialID); err != nil {
		return t, err
	}
	if in.ToOutletID <= 0 {
		return t, apperr.Invalid("A destination outlet is required")
	}
	if in.FromOutletID == in.ToOutletID {
		return t, apperr.Invalid("A transfer needs two different outlets")
	}
	qty, err := stockQty(in.Qty)
	if err != nil {
		return t, err
	}
	t = StockTransfer{FromOutletID: in.FromOutletID, ToOutletID: in.ToOutletID, MaterialID: in.MaterialID, Qty: qty}
	if t.Note, err = trimmed("The note", in.Note, maxNote); err != nil {
		return t, err
	}
	t.Key, err = trimmed("The idempotency key", in.Key, maxKey)
	return t, err
}

// ParseMinQty reads a reorder level: an exact decimal of at most 4 places, 0 or more.
func ParseMinQty(in string) (*big.Rat, error) {
	return ParseDecimal("The minimum quantity", in, 4)
}

// ---- queries ----

// StockQuery filters and orders the stock list.
type StockQuery struct {
	// OutletID 0 lists every outlet's balances.
	OutletID int64
	Q        string
	Low      bool
	// SortBy is sku, name, qty or value (default sku); SortDir asc (default) or desc.
	SortBy, SortDir string
	Limit, Offset   int
}

func (q *StockQuery) Validate() error {
	q.Q = strings.TrimSpace(q.Q)
	if len([]rune(q.Q)) > 64 {
		return apperr.Invalid("The search can be at most 64 characters")
	}
	if q.OutletID < 0 {
		return apperr.Invalid("outlet_id must be a positive number")
	}
	switch q.SortBy {
	case "":
		q.SortBy = "sku"
	case "sku", "name", "qty", "value":
	default:
		return apperr.Invalid("sort_by must be one of sku, name, qty, value")
	}
	if err := pageDefaults(&q.SortDir, &q.Limit, &q.Offset); err != nil {
		return err
	}
	return nil
}

func pageDefaults(dir *string, limit, offset *int) error {
	switch *dir {
	case "":
		*dir = "asc"
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

// MovementQuery filters and orders the ledger.
type MovementQuery struct {
	OutletID, MaterialID int64
	Kind                 string
	// Q matches, ignoring case, part of the note, the document number or the supplier.
	Q string
	// From and To are days (YYYY-MM-DD, Asia/Jakarta), both included; empty = open.
	From, To string
	// SortBy is created_at (default) or id; SortDir desc (default) or asc.
	SortBy, SortDir string
	Limit, Offset   int
}

func validDay(label, v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return apperr.Invalid(fmt.Sprintf("%s must be a day written YYYY-MM-DD", label))
	}
	return nil
}

func (q *MovementQuery) Validate() error {
	q.Q = strings.TrimSpace(q.Q)
	if len([]rune(q.Q)) > 64 {
		return apperr.Invalid("The search can be at most 64 characters")
	}
	if q.OutletID < 0 || q.MaterialID < 0 {
		return apperr.Invalid("outlet_id and material_id must be positive numbers")
	}
	if q.Kind != "" && !oneOf(MovementKinds, q.Kind) {
		return apperr.Invalid("kind must be one of " + strings.Join(MovementKinds, ", "))
	}
	if err := validDay("from", q.From); err != nil {
		return err
	}
	if err := validDay("to", q.To); err != nil {
		return err
	}
	if q.From != "" && q.To != "" && q.To < q.From {
		return apperr.Invalid("to cannot be before from")
	}
	switch q.SortBy {
	case "":
		q.SortBy = "created_at"
	case "created_at", "id":
	default:
		return apperr.Invalid("sort_by must be created_at or id")
	}
	if q.SortDir == "" {
		q.SortDir = "desc"
	}
	return pageDefaults(&q.SortDir, &q.Limit, &q.Offset)
}

// ---- consumption from sales ----

// OrderLineUse is a sold line as stock sees it: which service, how many, and the options chosen (ids recorded on the order).
type OrderLineUse struct {
	ServiceID         int64
	Qty               string // the shortest exact decimal of the line's quantity
	VariantOptionIDs  []int64
	ModifierOptionIDs []int64
}

// StockOrder is an order whose materials are to be taken out of (or put back into) an outlet's stock.
type StockOrder struct {
	OrderID string
	// Code is the order's TRX code, recorded on each of its movements.
	Code     string
	OutletID int64
	Lines    []OrderLineUse
}

// OrderUse is how much of each material an order consumes: the exact quantities of every line added together, then rounded ONCE
// per material to 4 places, half away from zero. A material that rounds to nothing is left out.
//
// Only the options recorded on the line count, and nothing is validated against today's min/max rules, because the order was
// priced and sold under the rules of its day. An order with no option ids consumes its service's own recipe only.
func OrderUse(contexts map[int64]CostingContext, o StockOrder) (map[int64]*big.Rat, error) {
	totals := map[int64]*big.Rat{}
	for _, l := range o.Lines {
		c, ok := contexts[l.ServiceID]
		if !ok {
			continue // not a service of the catalog (an old POS-only item): nothing to consume
		}
		// The quantity of an order line is stored as a float64 by the order service; it arrives as that number's shortest exact decimal
		// ("0.5", "2.5"), which is read as such and rounded to 4 places, half away from zero.
		qty, ok := new(big.Rat).SetString(l.Qty)
		if !ok || qty.Sign() <= 0 {
			return nil, apperr.Invalid("An order quantity is not a positive number")
		}
		qty = new(big.Rat).SetFrac(scaledRound(qty, 4), pow10(4))
		add := func(lines []BOMLine) {
			for _, b := range lines {
				if totals[b.MaterialID] == nil {
					totals[b.MaterialID] = new(big.Rat)
				}
				totals[b.MaterialID].Add(totals[b.MaterialID], b.Used(qty))
			}
		}
		add(c.Base)
		for _, id := range l.VariantOptionIDs {
			add(c.Variant[id])
		}
		for _, id := range l.ModifierOptionIDs {
			add(c.Modifier[id])
		}
	}
	out := map[int64]*big.Rat{}
	for id, t := range totals {
		if r := new(big.Rat).SetFrac(scaledRound(t, 4), pow10(4)); r.Sign() > 0 {
			out[id] = r
		}
	}
	return out, nil
}

func pow10(n int) *big.Int { return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil) }
