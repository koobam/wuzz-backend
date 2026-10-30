package grpc

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/command"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/query"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// InternalKeyHeader carries the key the order service presents when it asks for an order's stock to be taken out or put back.
const InternalKeyHeader = "x-internal-key"

// outletAccess says whether the caller may work with an outlet's stock: an admin may with any, outlet staff only with the
// outlet(s) their roles name. It returns who the caller is, for created_by.
func outletAccess(ctx context.Context, outlet int64) (authn.Identity, error) {
	id, err := authn.Require(ctx)
	if err != nil {
		return id, err
	}
	if id.HasRole("admin") || (outlet > 0 && id.HasRole("outlet:"+strconv.FormatInt(outlet, 10))) {
		return id, nil
	}
	return id, apperr.Forbidden("You do not have access to this outlet's stock")
}

// actor is how a movement records who booked it: the name and the account id.
func actor(id authn.Identity) string {
	if strings.TrimSpace(id.Name) == "" {
		return id.UserID
	}
	return fmt.Sprintf("%s (%s)", strings.TrimSpace(id.Name), id.UserID)
}

func stockRowToProto(r domain.StockRow) *catalogv1.StockRow {
	return &catalogv1.StockRow{
		OutletId: r.OutletID, MaterialId: r.MaterialID, Sku: r.SKU, Name: r.Name, Unit: r.Unit,
		Qty: domain.FormatExact(r.Qty, 4), Value: r.Value, AvgCost: domain.FormatRat(r.AvgCost(), displayDP),
		MinQty: domain.FormatExact(r.MinQty, 4), Low: r.Low(),
	}
}

func movementToProto(m domain.StockMovement) *catalogv1.StockMovement {
	return &catalogv1.StockMovement{
		Id: m.ID, OutletId: m.OutletID, MaterialId: m.MaterialID, Sku: m.SKU, Name: m.Name, Unit: m.Unit, Kind: m.Kind, Reason: m.Reason,
		QtyDelta: domain.FormatExact(m.QtyDelta, 4), ValueDelta: m.ValueDelta, BalanceQty: domain.FormatExact(m.BalanceQty, 4), BalanceValue: m.BalanceValue,
		RefType: m.RefType, RefId: m.RefID, RefCode: m.RefCode, Supplier: m.Supplier, DocNo: m.DocNo, Note: m.Note, CreatedBy: m.CreatedBy, CreatedAt: timestamppb.New(m.CreatedAt),
	}
}

func (s *Server) ListStock(ctx context.Context, req *catalogv1.ListStockRequest) (*catalogv1.ListStockResponse, error) {
	if _, err := outletAccess(ctx, req.GetOutletId()); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListStock.Handle(ctx, query.ListStock{Query: domain.StockQuery{OutletID: req.GetOutletId(), Q: req.GetQ(), Low: req.GetLow(),
		SortBy: req.GetSortBy(), SortDir: req.GetSortDir(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset())}})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ListStockResponse{Total: res.Total}
	for _, r := range res.Rows {
		out.Rows = append(out.Rows, stockRowToProto(r))
	}
	return out, nil
}

func (s *Server) ListMovements(ctx context.Context, req *catalogv1.ListMovementsRequest) (*catalogv1.ListMovementsResponse, error) {
	if _, err := outletAccess(ctx, req.GetOutletId()); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListMovements.Handle(ctx, query.ListMovements{Query: domain.MovementQuery{OutletID: req.GetOutletId(), MaterialID: req.GetMaterialId(), Kind: req.GetKind(),
		Q: req.GetQ(), From: req.GetFrom(), To: req.GetTo(), SortBy: req.GetSortBy(), SortDir: req.GetSortDir(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset())}})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ListMovementsResponse{Total: res.Total}
	for _, m := range res.Movements {
		out.Movements = append(out.Movements, movementToProto(m))
	}
	return out, nil
}

func (s *Server) ReceiveStock(ctx context.Context, req *catalogv1.ReceiveStockRequest) (*catalogv1.StockMovementResponse, error) {
	id, err := outletAccess(ctx, req.GetOutletId())
	if err != nil {
		return nil, err
	}
	res, err := s.app.Commands.ReceiveStock.Handle(ctx, command.ReceiveStock{Input: domain.StockReceiptInput{OutletID: req.GetOutletId(), MaterialID: req.GetMaterialId(), Qty: req.GetQty(),
		TotalCost: req.GetTotalCost(), Supplier: req.GetSupplier(), DocNo: req.GetDocNo(), Note: req.GetNote(), Key: req.GetIdempotencyKey()}, By: actor(id)})
	if err != nil {
		return nil, err
	}
	return &catalogv1.StockMovementResponse{Movement: movementToProto(res.Movement), Created: res.Created}, nil
}

func (s *Server) WasteStock(ctx context.Context, req *catalogv1.WasteStockRequest) (*catalogv1.StockMovementResponse, error) {
	id, err := outletAccess(ctx, req.GetOutletId())
	if err != nil {
		return nil, err
	}
	res, err := s.app.Commands.WasteStock.Handle(ctx, command.WasteStock{Input: domain.StockWasteInput{OutletID: req.GetOutletId(), MaterialID: req.GetMaterialId(), Qty: req.GetQty(),
		Reason: req.GetReason(), Note: req.GetNote(), Key: req.GetIdempotencyKey()}, By: actor(id)})
	if err != nil {
		return nil, err
	}
	return &catalogv1.StockMovementResponse{Movement: movementToProto(res.Movement), Created: res.Created}, nil
}

func (s *Server) AdjustStock(ctx context.Context, req *catalogv1.AdjustStockRequest) (*catalogv1.StockMovementResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id, _ := authn.FromContext(ctx)
	res, err := s.app.Commands.CountStock.Handle(ctx, command.CountStock{Input: domain.StockAdjustInput{OutletID: req.GetOutletId(), MaterialID: req.GetMaterialId(), CountedQty: req.GetCountedQty(),
		Note: req.GetNote(), Key: req.GetIdempotencyKey()}, By: actor(id)})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.StockMovementResponse{Created: res.Created}
	if res.Movement != nil {
		out.Movement = movementToProto(*res.Movement)
	}
	return out, nil
}

func (s *Server) TransferStock(ctx context.Context, req *catalogv1.TransferStockRequest) (*catalogv1.TransferStockResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id, _ := authn.FromContext(ctx)
	res, err := s.app.Commands.TransferStock.Handle(ctx, command.TransferStock{Input: domain.StockTransferInput{FromOutletID: req.GetFromOutletId(), ToOutletID: req.GetToOutletId(),
		MaterialID: req.GetMaterialId(), Qty: req.GetQty(), Note: req.GetNote(), Key: req.GetIdempotencyKey()}, By: actor(id)})
	if err != nil {
		return nil, err
	}
	return &catalogv1.TransferStockResponse{Out: movementToProto(res.Out), In: movementToProto(res.In), Created: res.Created}, nil
}

func (s *Server) SetMinStock(ctx context.Context, req *catalogv1.SetMinStockRequest) (*catalogv1.StockRowResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	row, err := s.app.Commands.SetMinStock.Handle(ctx, command.SetMinStock{OutletID: req.GetOutletId(), MaterialID: req.GetMaterialId(), MinQty: req.GetMinQty()})
	if err != nil {
		return nil, err
	}
	return &catalogv1.StockRowResponse{Row: stockRowToProto(row)}, nil
}

// ApplyOrderStock is for the order service: it takes an order's materials out of stock, or puts them back. It is not for people,
// so it answers only to the internal key, compared in constant time; with no key configured it answers to nobody.
func (s *Server) ApplyOrderStock(ctx context.Context, req *catalogv1.ApplyOrderStockRequest) (*catalogv1.ApplyOrderStockResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	given := ""
	if v := md.Get(InternalKeyHeader); len(v) > 0 {
		given = v[0]
	}
	if s.internalKey == "" || subtle.ConstantTimeCompare([]byte(given), []byte(s.internalKey)) != 1 {
		return nil, apperr.Forbidden("This call is for internal services only")
	}
	o := domain.StockOrder{OrderID: req.GetOrderId(), OutletID: req.GetOutletId(), Code: req.GetOrderCode()}
	if o.OrderID == "" || o.OutletID <= 0 {
		return nil, apperr.Invalid("An order id and an outlet are required")
	}
	for _, l := range req.GetLines() {
		o.Lines = append(o.Lines, domain.OrderLineUse{ServiceID: l.GetServiceId(), Qty: l.GetQty(), VariantOptionIDs: l.GetVariantOptionIds(), ModifierOptionIDs: l.GetModifierOptionIds()})
	}
	by := req.GetActor()
	if by == "" {
		by = "system"
	}
	var n int
	var err error
	switch req.GetAction() {
	case "consume":
		n, err = s.app.Commands.ConsumeOrder.Handle(ctx, command.ConsumeOrder{Order: o, By: by})
	case "reverse":
		n, err = s.app.Commands.ReverseOrder.Handle(ctx, command.ReverseOrder{Order: o, By: by})
	default:
		return nil, apperr.Invalid("The action must be consume or reverse")
	}
	if err != nil {
		return nil, err
	}
	return &catalogv1.ApplyOrderStockResponse{Movements: int32(n)}, nil
}
