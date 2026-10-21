// Package orders is the gateway's REST surface over the transaction service,
// shared by the POS (scoped to the staff member's outlets) and the back-office
// (everything). Scoping is enforced by the transaction service from the token.
package orders

import (
	"github.com/gin-gonic/gin"

	transactionv1 "github.com/koobam/wuzz-backend/gen/wuzz/transaction/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

type Handler struct {
	Tx transactionv1.TransactionServiceClient
}

type lineDTO struct {
	ItemID int64   `json:"item_id"`
	Name   string  `json:"name" binding:"required"`
	Unit   string  `json:"unit"`
	Price  int64   `json:"price"`
	Qty    float64 `json:"qty"`
}

type orderDTO struct {
	ID            string    `json:"id"`
	OutletID      int64     `json:"outlet_id"`
	CustomerName  string    `json:"customer_name"`
	CustomerPhone string    `json:"customer_phone"`
	Lines         []lineDTO `json:"lines"`
	Total         int64     `json:"total"`
	Status        string    `json:"status"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     string    `json:"created_at"`
	UpdatedAt     string    `json:"updated_at"`
}

func toDTO(o *transactionv1.Order) orderDTO {
	lines := make([]lineDTO, 0, len(o.GetLines()))
	for _, l := range o.GetLines() {
		lines = append(lines, lineDTO{ItemID: l.GetItemId(), Name: l.GetName(), Unit: l.GetUnit(), Price: l.GetPrice(), Qty: l.GetQty()})
	}
	return orderDTO{
		ID: o.GetId(), OutletID: o.GetOutletId(), CustomerName: o.GetCustomerName(), CustomerPhone: o.GetCustomerPhone(),
		Lines: lines, Total: o.GetTotal(), Status: o.GetStatus(), CreatedBy: o.GetCreatedBy(),
		CreatedAt: o.GetCreatedAt().AsTime().Format(timeLayout), UpdatedAt: o.GetUpdatedAt().AsTime().Format(timeLayout),
	}
}

type createBody struct {
	ID            string    `json:"id" binding:"required"`
	OutletID      int64     `json:"outlet_id" binding:"required"`
	CustomerName  string    `json:"customer_name" binding:"required"`
	CustomerPhone string    `json:"customer_phone"`
	Lines         []lineDTO `json:"lines" binding:"required,min=1,dive"`
}

// Create is idempotent on the body's id: the POS retries until it hears back.
// 201 for a new order, 200 when the id already existed.
func (h *Handler) Create(c *gin.Context) {
	b, ok := httpx.Bind[createBody](c)
	if !ok {
		return
	}
	lines := make([]*transactionv1.OrderLine, 0, len(b.Lines))
	for _, l := range b.Lines {
		lines = append(lines, &transactionv1.OrderLine{ItemId: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty})
	}
	res, err := h.Tx.CreateOrder(c.Request.Context(), &transactionv1.CreateOrderRequest{
		Id: b.ID, OutletId: b.OutletID, CustomerName: b.CustomerName, CustomerPhone: b.CustomerPhone, Lines: lines})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	if res.GetCreated() {
		httpx.Created(c, toDTO(res.GetOrder()))
		return
	}
	httpx.OK(c, toDTO(res.GetOrder()))
}

func (h *Handler) Get(c *gin.Context) {
	res, err := h.Tx.GetOrder(c.Request.Context(), &transactionv1.GetOrderRequest{Id: c.Param("id")})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, toDTO(res.GetOrder()))
}

func (h *Handler) List(c *gin.Context) {
	res, err := h.Tx.ListOrders(c.Request.Context(), &transactionv1.ListOrdersRequest{
		OutletId: int64(httpx.IntQuery(c, "outlet_id", 0)),
		Status:   c.Query("status"),
		Limit:    int32(httpx.IntQuery(c, "limit", 25)),
		Offset:   int32(httpx.IntQuery(c, "offset", 0)),
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	items := make([]orderDTO, 0, len(res.GetOrders()))
	for _, o := range res.GetOrders() {
		items = append(items, toDTO(o))
	}
	httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
}

type statusBody struct {
	Status string `json:"status" binding:"required"`
}

func (h *Handler) SetStatus(c *gin.Context) {
	b, ok := httpx.Bind[statusBody](c)
	if !ok {
		return
	}
	res, err := h.Tx.UpdateOrderStatus(c.Request.Context(), &transactionv1.UpdateOrderStatusRequest{Id: c.Param("id"), Status: b.Status})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, toDTO(res.GetOrder()))
}
