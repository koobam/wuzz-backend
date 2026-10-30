// Package grpc is transaction's gRPC transport. The caller is whoever the
// verified access token names; their outlets come from its `outlet:<id>` roles.
package grpc

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	transactionv1 "github.com/koobam/wuzz-backend/gen/wuzz/transaction/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/transaction/app"
	"github.com/koobam/wuzz-backend/internal/services/transaction/app/command"
	"github.com/koobam/wuzz-backend/internal/services/transaction/app/query"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

type Server struct {
	transactionv1.UnimplementedTransactionServiceServer
	app *app.App
}

func Register(registrar grpc.ServiceRegistrar, application *app.App) {
	transactionv1.RegisterTransactionServiceServer(registrar, &Server{app: application})
}

func callerFrom(ctx context.Context) (domain.Caller, error) {
	id, err := authn.Require(ctx)
	if err != nil {
		return domain.Caller{}, err
	}
	c := domain.Caller{UserID: id.UserID, Admin: id.HasRole("admin")}
	for _, r := range id.Roles {
		if rest, ok := strings.CutPrefix(r, "outlet:"); ok {
			if n, err := strconv.ParseInt(rest, 10, 64); err == nil {
				c.Outlets = append(c.Outlets, n)
			}
		}
	}
	return c, nil
}

func (s *Server) CreateOrder(ctx context.Context, req *transactionv1.CreateOrderRequest) (*transactionv1.CreateOrderResponse, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	lines := make([]domain.Line, 0, len(req.GetLines()))
	for _, l := range req.GetLines() {
		line := domain.Line{ItemID: l.GetItemId(), Name: l.GetName(), Unit: l.GetUnit(), Price: l.GetPrice(), Qty: l.GetQty(), Variant: l.GetVariant(),
			VariantOptionIDs: l.GetVariantOptionIds(), ModifierOptionIDs: l.GetModifierOptionIds()}
		for _, m := range l.GetModifiers() {
			line.Modifiers = append(line.Modifiers, domain.Modifier{Group: m.GetGroup(), Name: m.GetName(), Price: m.GetPrice()})
		}
		lines = append(lines, line)
	}
	res, err := s.app.Commands.CreateOrder.Handle(ctx, command.CreateOrder{Caller: caller, ID: req.GetId(), OutletID: req.GetOutletId(),
		CustomerName: req.GetCustomerName(), CustomerPhone: req.GetCustomerPhone(), Code: req.GetCode(), Lines: lines})
	if err != nil {
		return nil, err
	}
	return &transactionv1.CreateOrderResponse{Order: toProto(res.Order), Created: res.Created}, nil
}

func (s *Server) GetOrder(ctx context.Context, req *transactionv1.GetOrderRequest) (*transactionv1.GetOrderResponse, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	o, err := s.app.Queries.GetOrder.Handle(ctx, query.GetOrder{Caller: caller, ID: req.GetId()})
	if err != nil {
		return nil, err
	}
	return &transactionv1.GetOrderResponse{Order: toProto(o)}, nil
}

func (s *Server) ListOrders(ctx context.Context, req *transactionv1.ListOrdersRequest) (*transactionv1.ListOrdersResponse, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListOrders.Handle(ctx, query.ListOrders{Caller: caller, OutletID: req.GetOutletId(), Status: req.GetStatus(), Query: req.GetQuery(),
		SortBy: req.GetSortBy(), SortDir: req.GetSortDir(), From: req.GetFrom(), To: req.GetTo(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset())})
	if err != nil {
		return nil, err
	}
	out := make([]*transactionv1.Order, 0, len(res.Orders))
	for _, o := range res.Orders {
		out = append(out, toProto(o))
	}
	return &transactionv1.ListOrdersResponse{Orders: out, Total: res.Total}, nil
}

func (s *Server) OrderStats(ctx context.Context, req *transactionv1.OrderStatsRequest) (*transactionv1.OrderStatsResponse, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.app.Queries.OrderStats.Handle(ctx, query.OrderStats{Caller: caller, OutletID: req.GetOutletId(), Days: int(req.GetDays()), From: req.GetFrom(), To: req.GetTo()})
	if err != nil {
		return nil, err
	}
	out := &transactionv1.OrderStatsResponse{Revenue: st.Revenue}
	for _, status := range domain.Statuses {
		out.ByStatus = append(out.ByStatus, &transactionv1.StatusCount{Status: status, Orders: st.ByStatus[status]})
	}
	for _, d := range st.Days {
		out.Days = append(out.Days, &transactionv1.DayStats{Date: d.Date, Orders: d.Orders, Fulfilled: d.Fulfilled, Cancelled: d.Cancelled})
	}
	return out, nil
}

func (s *Server) UpdateOrderStatus(ctx context.Context, req *transactionv1.UpdateOrderStatusRequest) (*transactionv1.UpdateOrderStatusResponse, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	o, err := s.app.Commands.SetOrderStatus.Handle(ctx, command.SetOrderStatus{Caller: caller, ID: req.GetId(), Status: req.GetStatus()})
	if err != nil {
		return nil, err
	}
	return &transactionv1.UpdateOrderStatusResponse{Order: toProto(o)}, nil
}

func toProto(o domain.Order) *transactionv1.Order {
	lines := make([]*transactionv1.OrderLine, 0, len(o.Lines))
	for _, l := range o.Lines {
		line := &transactionv1.OrderLine{ItemId: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty, Variant: l.Variant,
			VariantOptionIds: l.VariantOptionIDs, ModifierOptionIds: l.ModifierOptionIDs}
		for _, m := range l.Modifiers {
			line.Modifiers = append(line.Modifiers, &transactionv1.LineModifier{Group: m.Group, Name: m.Name, Price: m.Price})
		}
		lines = append(lines, line)
	}
	return &transactionv1.Order{
		Id: o.ID, Code: o.Code, OutletId: o.OutletID, CustomerName: o.CustomerName, CustomerPhone: o.CustomerPhone,
		Lines: lines, Total: o.Total, Status: o.Status, CreatedBy: o.CreatedBy, Version: o.Version,
		CreatedAt: timestamppb.New(o.CreatedAt), UpdatedAt: timestamppb.New(o.UpdatedAt),
	}
}

// OrderSummary totals the orders under the same filters as ListOrders.
func (s *Server) OrderSummary(ctx context.Context, req *transactionv1.OrderSummaryRequest) (*transactionv1.OrderSummaryResponse, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	sum, err := s.app.Queries.SummarizeOrders.Handle(ctx, query.SummarizeOrders{Caller: caller, OutletID: req.GetOutletId(), Status: req.GetStatus(), Query: req.GetQuery(), From: req.GetFrom(), To: req.GetTo()})
	if err != nil {
		return nil, err
	}
	return &transactionv1.OrderSummaryResponse{
		Orders: sum.Orders, Fulfilled: sum.Fulfilled, Cancelled: sum.Cancelled, Open: sum.Open, Counted: sum.Counted, Value: sum.Value,
		AvgValue: sum.AvgValue, Lines: sum.Lines, AvgLines: sum.AvgLines, CancelledPercent: sum.CancelledPercent, FulfilledPercent: sum.FulfilledPercent,
	}, nil
}
