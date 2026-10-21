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
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

type Server struct {
	transactionv1.UnimplementedTransactionServiceServer
	app *app.App
}

func Register(registrar grpc.ServiceRegistrar, application *app.App) {
	transactionv1.RegisterTransactionServiceServer(registrar, &Server{app: application})
}

func callerFrom(ctx context.Context) (app.Caller, error) {
	id, err := authn.Require(ctx)
	if err != nil {
		return app.Caller{}, err
	}
	c := app.Caller{UserID: id.UserID, Admin: id.HasRole("admin")}
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
		lines = append(lines, domain.Line{ItemID: l.GetItemId(), Name: l.GetName(), Unit: l.GetUnit(), Price: l.GetPrice(), Qty: l.GetQty()})
	}
	o, created, err := s.app.Create(ctx, caller, req.GetId(), req.GetOutletId(), req.GetCustomerName(), req.GetCustomerPhone(), lines)
	if err != nil {
		return nil, err
	}
	return &transactionv1.CreateOrderResponse{Order: toProto(o), Created: created}, nil
}

func (s *Server) GetOrder(ctx context.Context, req *transactionv1.GetOrderRequest) (*transactionv1.GetOrderResponse, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	o, err := s.app.Get(ctx, caller, req.GetId())
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
	orders, total, err := s.app.List(ctx, caller, req.GetOutletId(), req.GetStatus(), int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, err
	}
	out := make([]*transactionv1.Order, 0, len(orders))
	for _, o := range orders {
		out = append(out, toProto(o))
	}
	return &transactionv1.ListOrdersResponse{Orders: out, Total: total}, nil
}

func (s *Server) UpdateOrderStatus(ctx context.Context, req *transactionv1.UpdateOrderStatusRequest) (*transactionv1.UpdateOrderStatusResponse, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	o, err := s.app.SetStatus(ctx, caller, req.GetId(), req.GetStatus())
	if err != nil {
		return nil, err
	}
	return &transactionv1.UpdateOrderStatusResponse{Order: toProto(o)}, nil
}

func toProto(o domain.Order) *transactionv1.Order {
	lines := make([]*transactionv1.OrderLine, 0, len(o.Lines))
	for _, l := range o.Lines {
		lines = append(lines, &transactionv1.OrderLine{ItemId: l.ItemID, Name: l.Name, Unit: l.Unit, Price: l.Price, Qty: l.Qty})
	}
	return &transactionv1.Order{
		Id: o.ID, OutletId: o.OutletID, CustomerName: o.CustomerName, CustomerPhone: o.CustomerPhone,
		Lines: lines, Total: o.Total, Status: o.Status, CreatedBy: o.CreatedBy, Version: o.Version,
		CreatedAt: timestamppb.New(o.CreatedAt), UpdatedAt: timestamppb.New(o.UpdatedAt),
	}
}
