// Package grpc is catalog's gRPC transport. Reads are public; writes require
// the `admin` role from the verified access token.
package grpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type Server struct {
	catalogv1.UnimplementedCatalogServiceServer
	app *app.App
}

func Register(registrar grpc.ServiceRegistrar, application *app.App) {
	catalogv1.RegisterCatalogServiceServer(registrar, &Server{app: application})
}

func isAdmin(ctx context.Context) bool {
	id, ok := authn.FromContext(ctx)
	return ok && id.HasRole("admin")
}

func requireAdmin(ctx context.Context) error {
	if _, err := authn.Require(ctx); err != nil {
		return err
	}
	if !isAdmin(ctx) {
		return apperr.Forbidden("Administrator access is required")
	}
	return nil
}

func (s *Server) ListOutlets(ctx context.Context, req *catalogv1.ListOutletsRequest) (*catalogv1.ListOutletsResponse, error) {
	// Inactive outlets are an admin's business; everyone else sees active only.
	outlets, err := s.app.ListOutlets(ctx, req.GetIds(), req.GetIncludeInactive() && isAdmin(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]*catalogv1.Outlet, 0, len(outlets))
	for _, o := range outlets {
		out = append(out, outletToProto(o))
	}
	return &catalogv1.ListOutletsResponse{Outlets: out}, nil
}

func (s *Server) GetOutlet(ctx context.Context, req *catalogv1.GetOutletRequest) (*catalogv1.GetOutletResponse, error) {
	o, err := s.app.GetOutlet(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if !o.Active && !isAdmin(ctx) {
		return nil, apperr.NotFound("Outlet not found")
	}
	return &catalogv1.GetOutletResponse{Outlet: outletToProto(o)}, nil
}

func (s *Server) CreateOutlet(ctx context.Context, req *catalogv1.CreateOutletRequest) (*catalogv1.CreateOutletResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	o, err := s.app.AddOutlet(ctx, domain.Outlet{
		Name: req.GetName(), Address: req.GetAddress(), Phone: req.GetPhone(),
		Latitude: req.GetLatitude(), Longitude: req.GetLongitude(),
	})
	if err != nil {
		return nil, err
	}
	return &catalogv1.CreateOutletResponse{Outlet: outletToProto(o)}, nil
}

func (s *Server) UpdateOutlet(ctx context.Context, req *catalogv1.UpdateOutletRequest) (*catalogv1.UpdateOutletResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	o, err := s.app.ChangeOutlet(ctx, domain.Outlet{
		ID: req.GetId(), Name: req.GetName(), Address: req.GetAddress(), Phone: req.GetPhone(),
		Latitude: req.GetLatitude(), Longitude: req.GetLongitude(), Active: req.GetActive(),
	})
	if err != nil {
		return nil, err
	}
	return &catalogv1.UpdateOutletResponse{Outlet: outletToProto(o)}, nil
}

func (s *Server) NearbyOutlets(ctx context.Context, req *catalogv1.NearbyOutletsRequest) (*catalogv1.NearbyOutletsResponse, error) {
	q := domain.NearbyQuery{
		Limit: int(req.GetLimit()), Cursor: req.GetCursor(),
		SelectedID: req.GetSelectedOutletId(), Text: strings.TrimSpace(req.GetQuery()),
	}
	if req.GetHasLocation() {
		p, err := point(req.GetLatitude(), req.GetLongitude())
		if err != nil {
			return nil, err
		}
		q.Near = &p
	}
	outlets, next, err := s.app.NearbyOutlets(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]*catalogv1.OutletView, 0, len(outlets))
	for _, o := range outlets {
		out = append(out, outletViewToProto(o))
	}
	return &catalogv1.NearbyOutletsResponse{Outlets: out, Next: next}, nil
}

func (s *Server) ResolveCoverage(ctx context.Context, req *catalogv1.ResolveCoverageRequest) (*catalogv1.ResolveCoverageResponse, error) {
	p, err := point(req.GetLatitude(), req.GetLongitude())
	if err != nil {
		return nil, err
	}
	area, ok, err := s.app.ResolveCoverage(ctx, p)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &catalogv1.ResolveCoverageResponse{Covered: false}, nil
	}
	return &catalogv1.ResolveCoverageResponse{Covered: true, Area: &catalogv1.CoverageArea{Id: area.ID, Name: area.Name}}, nil
}

func (s *Server) BrowseServices(ctx context.Context, req *catalogv1.BrowseServicesRequest) (*catalogv1.BrowseServicesResponse, error) {
	items, err := s.app.BrowseServices(ctx, req.GetServiceCategory(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := make([]*catalogv1.ServiceView, 0, len(items))
	for _, it := range items {
		out = append(out, serviceViewToProto(it))
	}
	return &catalogv1.BrowseServicesResponse{Services: out}, nil
}

func serviceViewToProto(it domain.ServiceView) *catalogv1.ServiceView {
	return &catalogv1.ServiceView{
		Id: it.ID, Name: it.Name, Description: it.Description, Price: it.Price, Unit: it.Unit,
		ServiceType: it.ServiceType, ServiceCategory: it.ServiceCategory, MinKg: it.MinKg, MaxKg: it.MaxKg,
		Discount: it.Discount, DiscountType: it.DiscountType, Icon: it.Icon, Image: it.Image,
	}
}

func (s *Server) GetServiceDetail(ctx context.Context, req *catalogv1.GetServiceDetailRequest) (*catalogv1.GetServiceDetailResponse, error) {
	d, err := s.app.Discovery.ServiceDetail(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ServiceDetail{Service: serviceViewToProto(d.ServiceView)}
	for _, t := range d.Tiers {
		tier := &catalogv1.ServiceTier{Id: t.ID, Code: t.Code, Name: t.Name, SortOrder: t.SortOrder}
		if p := t.Pricing; p != nil {
			tier.Pricing = &catalogv1.ServiceTierPricing{
				Id: p.ID, ServiceId: p.ServiceID, TierId: p.TierID, Sku: p.SKU,
				EstimatedTime: p.EstimatedTime, EstimatedTimeUnit: p.EstimatedTimeUnit,
				Price: p.Price, Discount: p.Discount, DiscountType: p.DiscountType, Unit: p.Unit,
			}
		}
		out.Tiers = append(out.Tiers, tier)
	}
	for _, sec := range d.Sections {
		section := &catalogv1.ServiceSection{Title: sec.Title, Description: sec.Description, Type: sec.Type}
		for _, a := range sec.Addons {
			section.Addons = append(section.Addons, &catalogv1.ServiceAddon{
				Id: a.ID, Name: a.Name, Description: a.Description, Price: a.Price, Unit: a.Unit,
				Discount: a.Discount, DiscountType: a.DiscountType, Icon: a.Icon,
			})
		}
		out.Sections = append(out.Sections, section)
	}
	return &catalogv1.GetServiceDetailResponse{Detail: out}, nil
}

func (s *Server) ListServices(ctx context.Context, req *catalogv1.ListServicesRequest) (*catalogv1.ListServicesResponse, error) {
	items, err := s.app.ListServices(ctx, req.GetIncludeInactive() && isAdmin(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]*catalogv1.ServiceItem, 0, len(items))
	for _, it := range items {
		out = append(out, serviceToProto(it))
	}
	return &catalogv1.ListServicesResponse{Services: out}, nil
}

func (s *Server) CreateService(ctx context.Context, req *catalogv1.CreateServiceRequest) (*catalogv1.CreateServiceResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	it, err := s.app.AddService(ctx, domain.ServiceItem{Name: req.GetName(), Unit: req.GetUnit(), Price: req.GetPrice()})
	if err != nil {
		return nil, err
	}
	return &catalogv1.CreateServiceResponse{Service: serviceToProto(it)}, nil
}

func (s *Server) UpdateService(ctx context.Context, req *catalogv1.UpdateServiceRequest) (*catalogv1.UpdateServiceResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	it, err := s.app.ChangeService(ctx, domain.ServiceItem{
		ID: req.GetId(), Name: req.GetName(), Unit: req.GetUnit(), Price: req.GetPrice(), Active: req.GetActive(),
	})
	if err != nil {
		return nil, err
	}
	return &catalogv1.UpdateServiceResponse{Service: serviceToProto(it)}, nil
}

func point(lat, lng float64) (domain.Point, error) {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return domain.Point{}, apperr.Invalid("Latitude or longitude is out of range")
	}
	return domain.Point{Latitude: lat, Longitude: lng}, nil
}

func outletViewToProto(o domain.OutletView) *catalogv1.OutletView {
	return &catalogv1.OutletView{
		Id: o.ID, Name: o.Name, Address: o.Address, Category: o.Category,
		Latitude: o.Latitude, Longitude: o.Longitude, Images: o.Images,
		CurrentStatus: o.CurrentStatus, DeliveryTypes: o.DeliveryTypes,
		OpenTime: o.OpenTime, CloseTime: o.CloseTime,
		HasDistance: o.HasDistance, DistanceKm: o.DistanceKm,
	}
}

func outletToProto(o domain.Outlet) *catalogv1.Outlet {
	return &catalogv1.Outlet{
		Id: o.ID, Name: o.Name, Address: o.Address, Phone: o.Phone,
		Latitude: o.Latitude, Longitude: o.Longitude, Active: o.Active,
		CreatedAt: timestamppb.New(o.CreatedAt), UpdatedAt: timestamppb.New(o.UpdatedAt),
	}
}

func serviceToProto(s domain.ServiceItem) *catalogv1.ServiceItem {
	return &catalogv1.ServiceItem{
		Id: s.ID, Name: s.Name, Unit: s.Unit, Price: s.Price, Active: s.Active,
		CreatedAt: timestamppb.New(s.CreatedAt), UpdatedAt: timestamppb.New(s.UpdatedAt),
	}
}
