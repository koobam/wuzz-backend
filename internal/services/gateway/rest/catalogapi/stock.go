package catalogapi

import (
	"github.com/gin-gonic/gin"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/orders"
)

// The stock ledger. Quantities are exact decimal strings (a JSON number is refused), money is whole rupiah. The same handlers serve
// the back office (any outlet) and the POS (the outlet the account is pinned to, whatever the request says).

type stockRowDTO struct {
	OutletID   int64  `json:"outlet_id"`
	MaterialID int64  `json:"material_id"`
	SKU        string `json:"sku"`
	Name       string `json:"name"`
	Unit       string `json:"unit"`
	Qty        string `json:"qty"`
	Value      int64  `json:"value"`
	// AvgCost is value / qty rounded half away from zero to 6 places, for display; the standard unit cost while nothing is on hand.
	AvgCost string `json:"avg_cost"`
	MinQty  string `json:"min_qty"`
	Low     bool   `json:"low"`
}

func stockRowDTOOf(r *catalogv1.StockRow) stockRowDTO {
	return stockRowDTO{OutletID: r.GetOutletId(), MaterialID: r.GetMaterialId(), SKU: r.GetSku(), Name: r.GetName(), Unit: r.GetUnit(), Qty: r.GetQty(),
		Value: r.GetValue(), AvgCost: r.GetAvgCost(), MinQty: r.GetMinQty(), Low: r.GetLow()}
}

type movementDTO struct {
	ID           int64  `json:"id"`
	OutletID     int64  `json:"outlet_id"`
	MaterialID   int64  `json:"material_id"`
	SKU          string `json:"sku"`
	Name         string `json:"name"`
	Unit         string `json:"unit"`
	Kind         string `json:"kind"`
	Reason       string `json:"reason"`
	QtyDelta     string `json:"qty_delta"`
	ValueDelta   int64  `json:"value_delta"`
	BalanceQty   string `json:"balance_qty"`
	BalanceValue int64  `json:"balance_value"`
	RefType      string `json:"ref_type"`
	RefID        string `json:"ref_id"`
	// RefCode is the TRX code of the order the movement came from; "" for other movements and for those from before codes existed.
	RefCode   string `json:"ref_code"`
	Supplier  string `json:"supplier"`
	DocNo     string `json:"doc_no"`
	Note      string `json:"note"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

func movementDTOOf(m *catalogv1.StockMovement) *movementDTO {
	if m == nil {
		return nil
	}
	return &movementDTO{ID: m.GetId(), OutletID: m.GetOutletId(), MaterialID: m.GetMaterialId(), SKU: m.GetSku(), Name: m.GetName(), Unit: m.GetUnit(),
		Kind: m.GetKind(), Reason: m.GetReason(), QtyDelta: m.GetQtyDelta(), ValueDelta: m.GetValueDelta(), BalanceQty: m.GetBalanceQty(),
		BalanceValue: m.GetBalanceValue(), RefType: m.GetRefType(), RefID: m.GetRefId(), RefCode: m.GetRefCode(), Supplier: m.GetSupplier(), DocNo: m.GetDocNo(), Note: m.GetNote(),
		CreatedBy: m.GetCreatedBy(), CreatedAt: utc(m.GetCreatedAt())}
}

// scope is the outlet a request is about: the one a POS account is pinned to, else what the request asked for.
type scope func(c *gin.Context, asked int64) int64

func anyOutlet(_ *gin.Context, asked int64) int64 { return asked }
func ownOutlet(c *gin.Context, asked int64) int64 {
	if p := orders.Pinned(c); p != 0 {
		return p
	}
	return asked
}

func intParam(c *gin.Context, key string) int64 { return int64(httpx.IntQuery(c, key, 0)) }

// stored answers 201 for a new movement and 200 for a retry that got the original back.
func stored(c *gin.Context, created bool, data any) {
	if created {
		httpx.Created(c, data)
		return
	}
	httpx.OK(c, data)
}

func (h *Handler) listStock(sc scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		res, err := h.Catalog.ListStock(c.Request.Context(), &catalogv1.ListStockRequest{
			OutletId: sc(c, intParam(c, "outlet_id")), Q: c.Query("q"), Low: c.Query("low") == "true", SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"),
			Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
		if err != nil {
			httpx.Fail(c, httpx.Upstream(err))
			return
		}
		items := make([]stockRowDTO, 0, len(res.GetRows()))
		for _, r := range res.GetRows() {
			items = append(items, stockRowDTOOf(r))
		}
		httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
	}
}

func (h *Handler) listMovements(sc scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		res, err := h.Catalog.ListMovements(c.Request.Context(), &catalogv1.ListMovementsRequest{
			OutletId: sc(c, intParam(c, "outlet_id")), MaterialId: intParam(c, "material_id"), Kind: c.Query("kind"), Q: c.Query("q"),
			From: c.Query("from"), To: c.Query("to"), SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"),
			Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
		if err != nil {
			httpx.Fail(c, httpx.Upstream(err))
			return
		}
		items := make([]*movementDTO, 0, len(res.GetMovements()))
		for _, m := range res.GetMovements() {
			items = append(items, movementDTOOf(m))
		}
		httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
	}
}

type receiptBody struct {
	OutletID       int64  `json:"outlet_id"`
	MaterialID     int64  `json:"material_id"`
	Qty            Dec    `json:"qty"`
	TotalCost      int64  `json:"total_cost"`
	Supplier       string `json:"supplier"`
	DocNo          string `json:"doc_no"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *Handler) receipt(sc scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		b, ok := bindJSON[receiptBody](c)
		if !ok {
			return
		}
		res, err := h.Catalog.ReceiveStock(c.Request.Context(), &catalogv1.ReceiveStockRequest{OutletId: sc(c, b.OutletID), MaterialId: b.MaterialID,
			Qty: string(b.Qty), TotalCost: b.TotalCost, Supplier: b.Supplier, DocNo: b.DocNo, Note: b.Note, IdempotencyKey: b.IdempotencyKey})
		if err != nil {
			httpx.Fail(c, httpx.Upstream(err))
			return
		}
		stored(c, res.GetCreated(), movementDTOOf(res.GetMovement()))
	}
}

type wasteBody struct {
	OutletID       int64  `json:"outlet_id"`
	MaterialID     int64  `json:"material_id"`
	Qty            Dec    `json:"qty"`
	Reason         string `json:"reason"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *Handler) waste(sc scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		b, ok := bindJSON[wasteBody](c)
		if !ok {
			return
		}
		res, err := h.Catalog.WasteStock(c.Request.Context(), &catalogv1.WasteStockRequest{OutletId: sc(c, b.OutletID), MaterialId: b.MaterialID,
			Qty: string(b.Qty), Reason: b.Reason, Note: b.Note, IdempotencyKey: b.IdempotencyKey})
		if err != nil {
			httpx.Fail(c, httpx.Upstream(err))
			return
		}
		stored(c, res.GetCreated(), movementDTOOf(res.GetMovement()))
	}
}

// ---- back office only ----

func (h *Handler) BOListStock() gin.HandlerFunc     { return h.listStock(anyOutlet) }
func (h *Handler) BOListMovements() gin.HandlerFunc { return h.listMovements(anyOutlet) }
func (h *Handler) BOReceipt() gin.HandlerFunc       { return h.receipt(anyOutlet) }
func (h *Handler) BOWaste() gin.HandlerFunc         { return h.waste(anyOutlet) }

// POS mirrors: pinned to the caller's own outlet.
func (h *Handler) POSListStock() gin.HandlerFunc     { return h.listStock(ownOutlet) }
func (h *Handler) POSListMovements() gin.HandlerFunc { return h.listMovements(ownOutlet) }
func (h *Handler) POSReceipt() gin.HandlerFunc       { return h.receipt(ownOutlet) }
func (h *Handler) POSWaste() gin.HandlerFunc         { return h.waste(ownOutlet) }

// HeadOfficeOnly answers the stock actions the POS may not do (counts, transfers, reorder levels) with a 403.
func HeadOfficeOnly(c *gin.Context) {
	httpx.Fail(c, apperr.Forbidden("Only head office can do that"))
}

type adjustBody struct {
	OutletID       int64  `json:"outlet_id"`
	MaterialID     int64  `json:"material_id"`
	CountedQty     Dec    `json:"counted_qty"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

// BOAdjust books a stock count: the movement for counted minus the book. When they are equal nothing is booked and the answer is
// 200 with data null.
func (h *Handler) BOAdjust(c *gin.Context) {
	b, ok := bindJSON[adjustBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.AdjustStock(c.Request.Context(), &catalogv1.AdjustStockRequest{OutletId: b.OutletID, MaterialId: b.MaterialID,
		CountedQty: string(b.CountedQty), Note: b.Note, IdempotencyKey: b.IdempotencyKey})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	if res.GetMovement() == nil {
		httpx.OK(c, nil)
		return
	}
	stored(c, res.GetCreated(), movementDTOOf(res.GetMovement()))
}

type transferBody struct {
	FromOutletID   int64  `json:"from_outlet_id"`
	ToOutletID     int64  `json:"to_outlet_id"`
	MaterialID     int64  `json:"material_id"`
	Qty            Dec    `json:"qty"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *Handler) BOTransfer(c *gin.Context) {
	b, ok := bindJSON[transferBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.TransferStock(c.Request.Context(), &catalogv1.TransferStockRequest{FromOutletId: b.FromOutletID, ToOutletId: b.ToOutletID,
		MaterialId: b.MaterialID, Qty: string(b.Qty), Note: b.Note, IdempotencyKey: b.IdempotencyKey})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	stored(c, res.GetCreated(), gin.H{"out": movementDTOOf(res.GetOut()), "in": movementDTOOf(res.GetIn())})
}

type minBody struct {
	OutletID   int64 `json:"outlet_id"`
	MaterialID int64 `json:"material_id"`
	MinQty     Dec   `json:"min_qty"`
}

func (h *Handler) BOSetMin(c *gin.Context) {
	b, ok := bindJSON[minBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.SetMinStock(c.Request.Context(), &catalogv1.SetMinStockRequest{OutletId: b.OutletID, MaterialId: b.MaterialID, MinQty: string(b.MinQty)})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, stockRowDTOOf(res.GetRow()))
}
