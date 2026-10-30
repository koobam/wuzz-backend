package catalogapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

// Materials, recipes (BOM) and costing. Quantities and percentages are exact decimals and travel as JSON strings: a JSON
// number is refused, so a value like 0.1 never passes through a float on its way in. Money is whole rupiah.

// Dec is a decimal that has to arrive as a JSON string ("0.5"), never as a number.
type Dec string

type decError struct{}

func (decError) Error() string {
	return "quantities and percentages must be sent as strings, such as \"0.5\", not as numbers"
}

func (d *Dec) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*d = ""
		return nil
	}
	if len(b) == 0 || b[0] != '"' {
		return decError{}
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*d = Dec(s)
	return nil
}

// bindJSON reads a body and says what is wrong with it in words, instead of a blanket "invalid".
func bindJSON[T any](c *gin.Context) (T, bool) {
	var body T
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		var de decError
		var ute *json.UnmarshalTypeError
		switch {
		case errors.As(err, &de):
			httpx.Fail(c, apperr.Invalid(de.Error()))
		case errors.As(err, &ute):
			httpx.Fail(c, apperr.Invalid(fmt.Sprintf("%q must be of type %s", ute.Field, ute.Type.String())))
		default:
			httpx.Fail(c, apperr.Invalid("The request body is invalid").WithCause(err))
		}
		var zero T
		return zero, false
	}
	return body, true
}

// ---- materials ----

// MaterialDTO is a material. unit_cost is purchase_price / purchase_qty rounded half away from zero to 6 places, for display;
// the exact fraction is what costing uses.
type MaterialDTO struct {
	ID            int64  `json:"id"`
	SKU           string `json:"sku"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Unit          string `json:"unit"`
	PurchasePrice int64  `json:"purchase_price"`
	PurchaseQty   string `json:"purchase_qty"`
	UnitCost      string `json:"unit_cost"`
	Active        bool   `json:"active"`
	Notes         string `json:"notes"`
	UsedIn        int32  `json:"used_in"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func materialDTO(m *catalogv1.Material) MaterialDTO {
	return MaterialDTO{ID: m.GetId(), SKU: m.GetSku(), Name: m.GetName(), Category: m.GetCategory(), Unit: m.GetUnit(),
		PurchasePrice: m.GetPurchasePrice(), PurchaseQty: m.GetPurchaseQty(), UnitCost: m.GetUnitCost(), Active: m.GetActive(),
		Notes: m.GetNotes(), UsedIn: m.GetUsedIn(), CreatedAt: utc(m.GetCreatedAt()), UpdatedAt: utc(m.GetUpdatedAt())}
}

type materialBody struct {
	SKU           string `json:"sku"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Unit          string `json:"unit"`
	PurchasePrice int64  `json:"purchase_price"`
	PurchaseQty   Dec    `json:"purchase_qty"`
	Active        *bool  `json:"active"` // left out = active
	Notes         string `json:"notes"`
}

func (b materialBody) proto() *catalogv1.Material {
	return &catalogv1.Material{Sku: b.SKU, Name: b.Name, Category: b.Category, Unit: b.Unit, PurchasePrice: b.PurchasePrice,
		PurchaseQty: string(b.PurchaseQty), Active: b.Active == nil || *b.Active, Notes: b.Notes}
}

func (h *Handler) BOListMaterials(c *gin.Context) {
	res, err := h.Catalog.ListMaterials(c.Request.Context(), &catalogv1.ListMaterialsRequest{
		Q: c.Query("q"), Category: c.Query("category"), Active: c.Query("active"), SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"),
		Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	items := make([]MaterialDTO, 0, len(res.GetMaterials()))
	for _, m := range res.GetMaterials() {
		items = append(items, materialDTO(m))
	}
	httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
}

func (h *Handler) BOGetMaterial(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	res, err := h.Catalog.GetMaterial(c.Request.Context(), &catalogv1.GetMaterialRequest{Id: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, materialDTO(res.GetMaterial()))
}

func (h *Handler) BOCreateMaterial(c *gin.Context) {
	b, ok := bindJSON[materialBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.CreateMaterial(c.Request.Context(), &catalogv1.SaveMaterialRequest{Material: b.proto()})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, materialDTO(res.GetMaterial()))
}

func (h *Handler) BOUpdateMaterial(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	b, ok := bindJSON[materialBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.UpdateMaterial(c.Request.Context(), &catalogv1.SaveMaterialRequest{Id: id, Material: b.proto()})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, materialDTO(res.GetMaterial()))
}

func (h *Handler) BODeleteMaterial(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	if _, err := h.Catalog.DeleteMaterial(c.Request.Context(), &catalogv1.GetMaterialRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"id": id})
}

// ---- recipes ----

type bomLineDTO struct {
	MaterialID  int64  `json:"material_id"`
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	Qty         string `json:"qty"`
	Per         string `json:"per"`
	LossPercent string `json:"loss_percent"`
	UnitCost    string `json:"unit_cost"`
	Cost        string `json:"cost"`
}

// BomDTO is a recipe. cost is the exact cost of the per-unit lines for one unit sold and line_cost that of the per-line lines
// (used once per order line), each rounded to 6 places for display only.
type BomDTO struct {
	OwnerType string       `json:"owner_type"`
	OwnerID   int64        `json:"owner_id"`
	Lines     []bomLineDTO `json:"lines"`
	Cost      string       `json:"cost"`
	LineCost  string       `json:"line_cost"`
}

func bomLineDTOs(in []*catalogv1.BomLine) []bomLineDTO {
	out := make([]bomLineDTO, 0, len(in))
	for _, l := range in {
		out = append(out, bomLineDTO{MaterialID: l.GetMaterialId(), SKU: l.GetSku(), Name: l.GetName(), Unit: l.GetUnit(), Qty: l.GetQty(),
			Per: l.GetPer(), LossPercent: l.GetLossPercent(), UnitCost: l.GetUnitCost(), Cost: l.GetCost()})
	}
	return out
}

func bomDTO(b *catalogv1.Bom) BomDTO {
	return BomDTO{OwnerType: b.GetOwnerType(), OwnerID: b.GetOwnerId(), Lines: bomLineDTOs(b.GetLines()), Cost: b.GetCost(), LineCost: b.GetLineCost()}
}

func ownerRef(c *gin.Context) (*catalogv1.BomRef, bool) {
	id, err := strconv.ParseInt(c.Param("owner_id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, apperr.Invalid("The owner id is invalid"))
		return nil, false
	}
	switch t := c.Param("owner_type"); t {
	case "service", "variant_option", "modifier_option":
		return &catalogv1.BomRef{OwnerType: t, OwnerId: id}, true
	}
	httpx.Fail(c, apperr.Invalid("The owner type must be service, variant_option or modifier_option"))
	return nil, false
}

func (h *Handler) BOGetBom(c *gin.Context) {
	ref, ok := ownerRef(c)
	if !ok {
		return
	}
	res, err := h.Catalog.GetBom(c.Request.Context(), ref)
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, bomDTO(res))
}

type bomBody struct {
	Lines []struct {
		MaterialID  int64  `json:"material_id"`
		Qty         Dec    `json:"qty"`
		Per         string `json:"per"`
		LossPercent Dec    `json:"loss_percent"`
	} `json:"lines"`
}

func (h *Handler) BOSetBom(c *gin.Context) {
	ref, ok := ownerRef(c)
	if !ok {
		return
	}
	b, ok := bindJSON[bomBody](c)
	if !ok {
		return
	}
	req := &catalogv1.SetBomRequest{Owner: ref}
	for _, l := range b.Lines {
		req.Lines = append(req.Lines, &catalogv1.BomLine{MaterialId: l.MaterialID, Qty: string(l.Qty), Per: l.Per, LossPercent: string(l.LossPercent)})
	}
	res, err := h.Catalog.SetBom(c.Request.Context(), req)
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, bomDTO(res))
}

// ---- costing ----

type costedOptionDTO struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	PriceDelta *int64 `json:"price_delta,omitempty"`
	Price      *int64 `json:"price,omitempty"`
	PerUnit    string `json:"per_unit"`
	PerLine    string `json:"per_line"`
}

type costedGroupDTO struct {
	ID      int64             `json:"id"`
	Name    string            `json:"name"`
	Options []costedOptionDTO `json:"options"`
}

func costedGroups(in []*catalogv1.CostedGroup, variant bool) []costedGroupDTO {
	out := make([]costedGroupDTO, 0, len(in))
	for _, g := range in {
		cg := costedGroupDTO{ID: g.GetId(), Name: g.GetName(), Options: make([]costedOptionDTO, 0, len(g.GetOptions()))}
		for _, o := range g.GetOptions() {
			price := o.GetPrice()
			d := costedOptionDTO{ID: o.GetId(), Name: o.GetName(), PerUnit: o.GetPerUnit(), PerLine: o.GetPerLine()}
			if variant {
				d.PriceDelta = &price
			} else {
				d.Price = &price
			}
			cg.Options = append(cg.Options, d)
		}
		out = append(out, cg)
	}
	return out
}

// BOGetServiceCosting is the cost table of a service: what its own recipe and each option's recipe cost, per unit sold and per line.
func (h *Handler) BOGetServiceCosting(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, apperr.Invalid("The service id is invalid"))
		return
	}
	res, err := h.Catalog.GetServiceCosting(c.Request.Context(), &catalogv1.GetServiceCostingRequest{ServiceId: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{
		"service":         gin.H{"id": res.GetServiceId(), "name": res.GetServiceName(), "unit": res.GetUnit(), "price": res.GetPrice()},
		"base":            gin.H{"per_unit": res.GetBasePerUnit(), "per_line": res.GetBasePerLine(), "lines": bomLineDTOs(res.GetBaseLines())},
		"variant_groups":  costedGroups(res.GetVariantGroups(), true),
		"modifier_groups": costedGroups(res.GetModifierGroups(), false),
	})
}

type calcBody struct {
	ServiceID         int64   `json:"service_id"`
	VariantOptionIDs  []int64 `json:"variant_option_ids"`
	ModifierOptionIDs []int64 `json:"modifier_option_ids"`
	Qty               Dec     `json:"qty"`
}

// BOCalcCosting costs a sale exactly: every line is an exact fraction, they are added, and the total is rounded to rupiah once.
func (h *Handler) BOCalcCosting(c *gin.Context) {
	b, ok := bindJSON[calcBody](c)
	if !ok {
		return
	}
	if b.ServiceID <= 0 {
		httpx.Fail(c, apperr.Invalid("A service is required"))
		return
	}
	res, err := h.Catalog.CalcCosting(c.Request.Context(), &catalogv1.CalcCostingRequest{
		ServiceId: b.ServiceID, VariantOptionIds: b.VariantOptionIDs, ModifierOptionIds: b.ModifierOptionIDs, Qty: string(b.Qty)})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	lines := make([]gin.H, 0, len(res.GetLines()))
	for _, l := range res.GetLines() {
		lines = append(lines, gin.H{"source": l.GetSource(), "source_name": l.GetSourceName(), "material_id": l.GetMaterialId(), "sku": l.GetSku(),
			"name": l.GetName(), "unit": l.GetUnit(), "per": l.GetPer(), "qty_used": l.GetQtyUsed(), "unit_cost": l.GetUnitCost(), "cost": l.GetCost()})
	}
	httpx.OK(c, gin.H{
		"service": gin.H{"id": res.GetServiceId(), "name": res.GetServiceName(), "unit": res.GetUnit()},
		"qty":     res.GetQty(), "unit_price": res.GetUnitPrice(), "revenue": res.GetRevenue(), "lines": lines,
		"cost": res.GetCost(), "gross_profit": res.GetGrossProfit(), "margin_percent": res.GetMarginPercent(),
	})
}
