package grpc

import (
	"context"
	"math/big"

	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/command"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/query"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Materials, recipes and costing. Quantities and percentages cross the wire as exact decimal strings; the strings of costs
// are rounded half away from zero to 6 places for display only (see domain/decimal.go).

const displayDP = 6

func materialToProto(m domain.Material) *catalogv1.Material {
	return &catalogv1.Material{
		Id: m.ID, Sku: m.SKU, Name: m.Name, Category: m.Category, Unit: m.Unit, PurchasePrice: m.PurchasePrice,
		PurchaseQty: domain.FormatExact(m.PurchaseQty, 4), UnitCost: domain.FormatRat(m.UnitCost(), displayDP),
		Active: m.Active, Notes: m.Notes, UsedIn: m.UsedIn,
		CreatedAt: timestamppb.New(m.CreatedAt), UpdatedAt: timestamppb.New(m.UpdatedAt),
	}
}

func materialInput(in *catalogv1.Material) domain.MaterialInput {
	return domain.MaterialInput{SKU: in.GetSku(), Name: in.GetName(), Category: in.GetCategory(), Unit: in.GetUnit(),
		PurchasePrice: in.GetPurchasePrice(), PurchaseQty: in.GetPurchaseQty(), Active: in.GetActive(), Notes: in.GetNotes()}
}

func (s *Server) ListMaterials(ctx context.Context, req *catalogv1.ListMaterialsRequest) (*catalogv1.ListMaterialsResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListMaterials.Handle(ctx, query.ListMaterials{Query: domain.MaterialQuery{Q: req.GetQ(), Category: req.GetCategory(), Active: req.GetActive(),
		SortBy: req.GetSortBy(), SortDir: req.GetSortDir(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset())}})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ListMaterialsResponse{Total: res.Total}
	for _, m := range res.Materials {
		out.Materials = append(out.Materials, materialToProto(m))
	}
	return out, nil
}

func (s *Server) GetMaterial(ctx context.Context, req *catalogv1.GetMaterialRequest) (*catalogv1.MaterialResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	m, err := s.app.Queries.GetMaterial.Handle(ctx, query.GetMaterial{ID: req.GetId()})
	if err != nil {
		return nil, err
	}
	return &catalogv1.MaterialResponse{Material: materialToProto(m)}, nil
}

func (s *Server) CreateMaterial(ctx context.Context, req *catalogv1.SaveMaterialRequest) (*catalogv1.MaterialResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	m, err := s.app.Commands.CreateMaterial.Handle(ctx, command.CreateMaterial{Input: materialInput(req.GetMaterial())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.MaterialResponse{Material: materialToProto(m)}, nil
}

func (s *Server) UpdateMaterial(ctx context.Context, req *catalogv1.SaveMaterialRequest) (*catalogv1.MaterialResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	m, err := s.app.Commands.UpdateMaterial.Handle(ctx, command.UpdateMaterial{ID: req.GetId(), Input: materialInput(req.GetMaterial())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.MaterialResponse{Material: materialToProto(m)}, nil
}

func (s *Server) DeleteMaterial(ctx context.Context, req *catalogv1.GetMaterialRequest) (*catalogv1.DeleteGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	return &catalogv1.DeleteGroupResponse{}, s.app.Commands.DeleteMaterial.Handle(ctx, command.DeleteMaterial{ID: req.GetId()})
}

// bomLineToProto: unit_cost is the display of the exact fraction; cost is the exact cost of one unit sold (a per-line
// line: once), also display-rounded.
func bomLineToProto(l domain.BOMLine) *catalogv1.BomLine {
	return &catalogv1.BomLine{
		MaterialId: l.MaterialID, Sku: l.SKU, Name: l.Name, Unit: l.Unit, Qty: domain.FormatExact(l.Qty, 4), Per: l.Per,
		LossPercent: domain.FormatExact(l.LossPercent, 2), UnitCost: domain.FormatRat(l.UnitCost, displayDP),
		Cost: domain.FormatRat(l.CostOf(big.NewRat(1, 1)), displayDP),
	}
}

func bomToProto(ownerType string, ownerID int64, lines []domain.BOMLine) *catalogv1.Bom {
	out := &catalogv1.Bom{OwnerType: ownerType, OwnerId: ownerID}
	for _, l := range lines {
		out.Lines = append(out.Lines, bomLineToProto(l))
	}
	perUnit, perLine := domain.SumByPer(lines)
	out.Cost, out.LineCost = domain.FormatRat(perUnit, displayDP), domain.FormatRat(perLine, displayDP)
	return out
}

func (s *Server) GetBom(ctx context.Context, req *catalogv1.BomRef) (*catalogv1.Bom, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	lines, err := s.app.Queries.GetRecipe.Handle(ctx, query.GetRecipe{OwnerType: req.GetOwnerType(), OwnerID: req.GetOwnerId()})
	if err != nil {
		return nil, err
	}
	return bomToProto(req.GetOwnerType(), req.GetOwnerId(), lines), nil
}

func (s *Server) SetBom(ctx context.Context, req *catalogv1.SetBomRequest) (*catalogv1.Bom, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	in := make([]domain.BOMLineInput, 0, len(req.GetLines()))
	for _, l := range req.GetLines() {
		in = append(in, domain.BOMLineInput{MaterialID: l.GetMaterialId(), Qty: l.GetQty(), Per: l.GetPer(), LossPercent: l.GetLossPercent()})
	}
	lines, err := s.app.Commands.SetRecipe.Handle(ctx, command.SetRecipe{OwnerType: req.GetOwner().GetOwnerType(), OwnerID: req.GetOwner().GetOwnerId(), Lines: in})
	if err != nil {
		return nil, err
	}
	return bomToProto(req.GetOwner().GetOwnerType(), req.GetOwner().GetOwnerId(), lines), nil
}

func (s *Server) GetServiceCosting(ctx context.Context, req *catalogv1.GetServiceCostingRequest) (*catalogv1.ServiceCosting, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	c, err := s.app.Queries.GetServiceCosting.Handle(ctx, query.GetServiceCosting{ServiceID: req.GetServiceId()})
	if err != nil {
		return nil, err
	}
	perUnit, perLine := domain.SumByPer(c.Base)
	out := &catalogv1.ServiceCosting{
		ServiceId: c.Service.ID, ServiceName: c.Service.Name, Unit: c.Service.Unit, Price: c.Service.Price,
		BasePerUnit: domain.FormatRat(perUnit, displayDP), BasePerLine: domain.FormatRat(perLine, displayDP),
	}
	for _, l := range c.Base {
		out.BaseLines = append(out.BaseLines, bomLineToProto(l))
	}
	component := func(id int64, name string, price int64, lines []domain.BOMLine) *catalogv1.CostedComponent {
		u, l := domain.SumByPer(lines)
		return &catalogv1.CostedComponent{Id: id, Name: name, Price: price, PerUnit: domain.FormatRat(u, displayDP), PerLine: domain.FormatRat(l, displayDP)}
	}
	for _, g := range c.Service.VariantGroups {
		cg := &catalogv1.CostedGroup{Id: g.ID, Name: g.Name}
		for _, o := range g.Options {
			if o.Active {
				cg.Options = append(cg.Options, component(o.ID, o.Name, o.PriceDelta, c.Variant[o.ID]))
			}
		}
		out.VariantGroups = append(out.VariantGroups, cg)
	}
	for _, g := range c.Service.ModifierGroups {
		cg := &catalogv1.CostedGroup{Id: g.ID, Name: g.Name}
		for _, o := range g.Options {
			if o.Active {
				cg.Options = append(cg.Options, component(o.ID, o.Name, o.Price, c.Modifier[o.ID]))
			}
		}
		out.ModifierGroups = append(out.ModifierGroups, cg)
	}
	return out, nil
}

func (s *Server) CalcCosting(ctx context.Context, req *catalogv1.CalcCostingRequest) (*catalogv1.CalcCostingResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	calc, err := s.app.Queries.CalcCosting.Handle(ctx, query.CalcCosting{ServiceID: req.GetServiceId(), Input: domain.CalcInput{
		VariantOptionIDs: req.GetVariantOptionIds(), ModifierOptionIDs: req.GetModifierOptionIds(), Qty: req.GetQty()}})
	if err != nil {
		return nil, err
	}
	c, res := calc.Context, calc.Result
	out := &catalogv1.CalcCostingResponse{
		ServiceId: c.Service.ID, ServiceName: c.Service.Name, Unit: c.Service.Unit, Qty: domain.FormatExact(res.Qty, 4),
		UnitPrice: res.UnitPrice, Revenue: res.Revenue, Cost: res.Cost, GrossProfit: res.GrossProfit, MarginPercent: res.MarginPercent,
	}
	for _, l := range res.Lines {
		out.Lines = append(out.Lines, &catalogv1.CostedLine{
			Source: l.Source, SourceName: l.SourceName, MaterialId: l.Line.MaterialID, Sku: l.Line.SKU, Name: l.Line.Name, Unit: l.Line.Unit,
			Per: l.Line.Per, QtyUsed: domain.FormatRat(l.QtyUsed, displayDP), UnitCost: domain.FormatRat(l.Line.UnitCost, displayDP),
			Cost: domain.FormatRat(l.Cost, displayDP),
		})
	}
	return out, nil
}
