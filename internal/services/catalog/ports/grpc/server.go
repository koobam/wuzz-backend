// Package grpc is catalog's gRPC transport. Reads are public; writes require
// the `admin` role from the verified access token.
package grpc

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/command"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/query"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type Server struct {
	catalogv1.UnimplementedCatalogServiceServer
	app *app.App
	// internalKey is what the order service presents to use ApplyOrderStock; empty turns that call off.
	internalKey string
}

func Register(registrar grpc.ServiceRegistrar, application *app.App, internalKey string) {
	catalogv1.RegisterCatalogServiceServer(registrar, &Server{app: application, internalKey: internalKey})
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
	if req.GetLimit() > 0 {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		res, err := s.app.Queries.PageOutlets.Handle(ctx, query.PageOutlets{Query: domain.OutletQuery{
			Q: req.GetQ(), Active: req.GetActive(), SortBy: req.GetSortBy(), SortDir: req.GetSortDir(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
		}})
		if err != nil {
			return nil, err
		}
		out := make([]*catalogv1.Outlet, 0, len(res.Outlets))
		for _, o := range res.Outlets {
			out = append(out, outletToProto(o))
		}
		return &catalogv1.ListOutletsResponse{Outlets: out, Total: res.Total}, nil
	}
	// Inactive outlets are an admin's business; everyone else sees active only.
	var within *domain.Bounds
	if b := req.GetBounds(); b != nil {
		box := domain.Bounds{South: b.GetSouth(), West: b.GetWest(), North: b.GetNorth(), East: b.GetEast()}
		if err := box.Validate(); err != nil {
			return nil, err
		}
		within = &box
	}
	outlets, err := s.app.Queries.ListOutlets.Handle(ctx, query.ListOutlets{IDs: req.GetIds(), IncludeInactive: req.GetIncludeInactive() && isAdmin(ctx), Within: within})
	if err != nil {
		return nil, err
	}
	out := make([]*catalogv1.Outlet, 0, len(outlets))
	for _, o := range outlets {
		out = append(out, outletToProto(o))
	}
	return &catalogv1.ListOutletsResponse{Outlets: out}, nil
}

func (s *Server) ListServiceCategories(ctx context.Context, _ *catalogv1.ListServiceCategoriesRequest) (*catalogv1.ListServiceCategoriesResponse, error) {
	cats, err := s.app.Queries.ListServiceCategories.Handle(ctx, query.ListServiceCategories{})
	if err != nil {
		return nil, err
	}
	out := make([]*catalogv1.ServiceCategory, 0, len(cats))
	for _, c := range cats {
		out = append(out, &catalogv1.ServiceCategory{Id: c.ID, Name: c.Name, Icon: c.Icon, SortOrder: c.SortOrder})
	}
	return &catalogv1.ListServiceCategoriesResponse{Categories: out}, nil
}

func (s *Server) ListTaxRates(ctx context.Context, _ *catalogv1.ListTaxRatesRequest) (*catalogv1.ListTaxRatesResponse, error) {
	rates, err := s.app.Queries.ListTaxRates.Handle(ctx, query.ListTaxRates{})
	if err != nil {
		return nil, err
	}
	out := make([]*catalogv1.TaxRate, 0, len(rates))
	for _, t := range rates.Types() {
		out = append(out, &catalogv1.TaxRate{TaxType: t, RateBps: rates.Rate(t)})
	}
	return &catalogv1.ListTaxRatesResponse{Rates: out}, nil
}

func (s *Server) GetOutlet(ctx context.Context, req *catalogv1.GetOutletRequest) (*catalogv1.GetOutletResponse, error) {
	o, err := s.app.Queries.GetOutlet.Handle(ctx, query.GetOutlet{ID: req.GetId()})
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
	o, err := s.app.Commands.CreateOutlet.Handle(ctx, command.CreateOutlet{Outlet: domain.Outlet{
		Name: req.GetName(), Address: req.GetAddress(), Phone: req.GetPhone(), Email: req.GetEmail(),
		Latitude: req.GetLatitude(), Longitude: req.GetLongitude(), Images: req.GetImages(),
		Category: req.GetCategory(), DeliveryTypes: req.GetDeliveryTypes(), OpenTime: req.GetOpenTime(), CloseTime: req.GetCloseTime(),
		Timezone: req.GetTimezone(), Hours: hoursFromProto(req.GetHours()),
	}})
	if err != nil {
		return nil, err
	}
	return &catalogv1.CreateOutletResponse{Outlet: outletToProto(o)}, nil
}

func (s *Server) UpdateOutlet(ctx context.Context, req *catalogv1.UpdateOutletRequest) (*catalogv1.UpdateOutletResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	o, err := s.app.Commands.UpdateOutlet.Handle(ctx, command.UpdateOutlet{Outlet: domain.Outlet{
		ID: req.GetId(), Name: req.GetName(), Address: req.GetAddress(), Phone: req.GetPhone(), Email: req.GetEmail(),
		Latitude: req.GetLatitude(), Longitude: req.GetLongitude(), Active: req.GetActive(), Images: req.GetImages(),
		Category: req.GetCategory(), DeliveryTypes: req.GetDeliveryTypes(), OpenTime: req.GetOpenTime(), CloseTime: req.GetCloseTime(),
		Timezone: req.GetTimezone(), Hours: hoursFromProto(req.GetHours()),
	}})
	if err != nil {
		return nil, err
	}
	return &catalogv1.UpdateOutletResponse{Outlet: outletToProto(o)}, nil
}

func (s *Server) DeleteOutlet(ctx context.Context, req *catalogv1.DeleteOutletRequest) (*catalogv1.DeleteOutletResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	return &catalogv1.DeleteOutletResponse{}, s.app.Commands.DeleteOutlet.Handle(ctx, command.DeleteOutlet{ID: req.GetId()})
}

func (s *Server) DeleteService(ctx context.Context, req *catalogv1.DeleteServiceRequest) (*catalogv1.DeleteServiceResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	return &catalogv1.DeleteServiceResponse{}, s.app.Commands.DeleteService.Handle(ctx, command.DeleteService{ID: req.GetId()})
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
	res, err := s.app.Queries.NearbyOutlets.Handle(ctx, query.NearbyOutlets{Query: q})
	if err != nil {
		return nil, err
	}
	outlets, next := res.Outlets, res.Next
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
	res, err := s.app.Queries.ResolveCoverage.Handle(ctx, query.ResolveCoverage{Point: p})
	if err != nil {
		return nil, err
	}
	area, ok := res.Area, res.Found
	if !ok {
		return &catalogv1.ResolveCoverageResponse{Covered: false}, nil
	}
	return &catalogv1.ResolveCoverageResponse{Covered: true, Area: &catalogv1.CoverageArea{Id: area.ID, Name: area.Name}}, nil
}

func (s *Server) BrowseServices(ctx context.Context, req *catalogv1.BrowseServicesRequest) (*catalogv1.BrowseServicesResponse, error) {
	items, err := s.app.Queries.BrowseServices.Handle(ctx, query.BrowseServices{Category: req.GetServiceCategory(), Limit: int(req.GetLimit())})
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
		PriceBeforeTax: it.PriceBeforeTax, PriceAfterTax: it.PriceAfterTax, TaxType: it.TaxType, TaxRateBps: it.TaxRateBps,
		DiscountAmount: it.DiscountAmount, PriceAfterDiscount: it.PriceAfterDiscount,
	}
}

func (s *Server) GetServiceDetail(ctx context.Context, req *catalogv1.GetServiceDetailRequest) (*catalogv1.GetServiceDetailResponse, error) {
	d, err := s.app.Queries.GetServiceDetail.Handle(ctx, query.GetServiceDetail{ID: req.GetId()})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ServiceDetail{Service: serviceViewToProto(d.ServiceView)}
	out.Tiers = tiersToProto(d.Tiers)
	out.Sections = sectionsToProto(d.Sections)
	return &catalogv1.GetServiceDetailResponse{Detail: out}, nil
}

func (s *Server) ListServices(ctx context.Context, req *catalogv1.ListServicesRequest) (*catalogv1.ListServicesResponse, error) {
	if req.GetLimit() > 0 {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		res, err := s.app.Queries.PageServices.Handle(ctx, query.PageServices{Query: domain.ServiceQuery{
			Q: req.GetQ(), CategoryID: req.GetCategoryId(), Unit: req.GetUnit(), Active: req.GetActive(), TaxType: req.GetTaxType(), DiscountType: req.GetDiscountType(),
			SortBy: req.GetSortBy(), SortDir: req.GetSortDir(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
		}})
		if err != nil {
			return nil, err
		}
		out := make([]*catalogv1.ServiceItem, 0, len(res.Services))
		for _, it := range res.Services {
			out = append(out, serviceToProto(it))
		}
		return &catalogv1.ListServicesResponse{Services: out, Total: res.Total}, nil
	}
	items, err := s.app.Queries.ListServices.Handle(ctx, query.ListServices{IncludeInactive: req.GetIncludeInactive() && isAdmin(ctx)})
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
	it, err := s.app.Commands.CreateService.Handle(ctx, command.CreateService{Service: domain.ServiceItem{
		Name: req.GetName(), Unit: req.GetUnit(), Price: req.GetPrice(), CategoryID: req.GetCategoryId(), TaxType: req.GetTaxType(), InternalNotes: req.GetInternalNotes(),
		DiscountType: req.GetDiscountType(), DiscountValue: req.GetDiscountValue(),
		VariantGroupIDs: idsFromProto(req.GetVariantGroupIds()), ModifierGroupIDs: idsFromProto(req.GetModifierGroupIds()),
	}})
	if err != nil {
		return nil, err
	}
	return &catalogv1.CreateServiceResponse{Service: serviceToProto(it)}, nil
}

func (s *Server) UpdateService(ctx context.Context, req *catalogv1.UpdateServiceRequest) (*catalogv1.UpdateServiceResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	it, err := s.app.Commands.UpdateService.Handle(ctx, command.UpdateService{Service: domain.ServiceItem{
		ID: req.GetId(), Name: req.GetName(), Unit: req.GetUnit(), Price: req.GetPrice(), Active: req.GetActive(), CategoryID: req.GetCategoryId(), TaxType: req.GetTaxType(), InternalNotes: req.GetInternalNotes(),
		DiscountType: req.GetDiscountType(), DiscountValue: req.GetDiscountValue(),
		VariantGroupIDs: idsFromProto(req.GetVariantGroupIds()), ModifierGroupIDs: idsFromProto(req.GetModifierGroupIds()),
	}})
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
	now := time.Now()
	// open_time and close_time are today's hours on the outlet's clock (empty on a closed day); the whole week is in hours.
	open, closeAt, _ := o.TodayHours(now)
	if len(o.Hours) == 0 {
		open, closeAt = o.OpenTime, o.CloseTime
	}
	return &catalogv1.Outlet{
		Id: o.ID, Name: o.Name, Address: o.Address, Phone: o.Phone, Email: o.Email,
		Latitude: o.Latitude, Longitude: o.Longitude, Active: o.Active, Images: o.Images,
		Category: o.Category, DeliveryTypes: o.DeliveryTypes, OpenTime: open, CloseTime: closeAt, IsOpenNow: o.IsOpenNow(now),
		Timezone: o.Timezone, Hours: hoursToProto(o.Hours),
		CreatedAt: timestamppb.New(o.CreatedAt), UpdatedAt: timestamppb.New(o.UpdatedAt),
	}
}

func hoursToProto(h []domain.DayHours) []*catalogv1.OutletDay {
	out := make([]*catalogv1.OutletDay, 0, len(h))
	for _, d := range h {
		out = append(out, &catalogv1.OutletDay{Weekday: int32(d.Weekday), Closed: d.Closed, Open_24H: d.Open24h, OpenTime: d.OpenTime, CloseTime: d.CloseTime})
	}
	return out
}

func hoursFromProto(h []*catalogv1.OutletDay) []domain.DayHours {
	out := make([]domain.DayHours, 0, len(h))
	for _, d := range h {
		out = append(out, domain.DayHours{Weekday: int(d.GetWeekday()), Closed: d.GetClosed(), Open24h: d.GetOpen_24H(), OpenTime: d.GetOpenTime(), CloseTime: d.GetCloseTime()})
	}
	return out
}

func serviceToProto(s domain.ServiceItem) *catalogv1.ServiceItem {
	return &catalogv1.ServiceItem{
		Id: s.ID, Name: s.Name, Unit: s.Unit, Price: s.PriceAfterTax, Active: s.Active,
		PriceBeforeTax: s.Price, PriceAfterTax: s.PriceAfterTax, TaxRateBps: s.TaxRateBps, InternalNotes: s.InternalNotes,
		CategoryId: s.CategoryID, Icon: s.Icon, Description: s.Description, TaxType: s.TaxType,
		DiscountType: s.DiscountType, DiscountValue: s.DiscountValue, DiscountAmount: s.DiscountAmount, PriceAfterDiscount: s.PriceAfterDiscount,
		VariantGroups: variantGroupsToProto(s.VariantGroups), ModifierGroups: modifierGroupsToProto(s.ModifierGroups),
		CreatedAt: timestamppb.New(s.CreatedAt), UpdatedAt: timestamppb.New(s.UpdatedAt),
	}
}

func tiersToProto(in []domain.ServiceTier) []*catalogv1.ServiceTier {
	var out []*catalogv1.ServiceTier
	for _, t := range in {
		tier := &catalogv1.ServiceTier{Id: t.ID, Code: t.Code, Name: t.Name, SortOrder: t.SortOrder}
		if p := t.Pricing; p != nil {
			tier.Pricing = &catalogv1.ServiceTierPricing{
				Id: p.ID, ServiceId: p.ServiceID, TierId: p.TierID, Sku: p.SKU,
				EstimatedTime: p.EstimatedTime, EstimatedTimeUnit: p.EstimatedTimeUnit,
				Price: p.Price, Discount: p.Discount, DiscountType: p.DiscountType, Unit: p.Unit,
			}
		}
		out = append(out, tier)
	}
	return out
}

func sectionsToProto(in []domain.ServiceSection) []*catalogv1.ServiceSection {
	var out []*catalogv1.ServiceSection
	for _, sec := range in {
		section := &catalogv1.ServiceSection{Title: sec.Title, Description: sec.Description, Type: sec.Type}
		for _, a := range sec.Addons {
			section.Addons = append(section.Addons, &catalogv1.ServiceAddon{
				Id: a.ID, Name: a.Name, Description: a.Description, Price: a.Price, Unit: a.Unit,
				Discount: a.Discount, DiscountType: a.DiscountType, Icon: a.Icon,
			})
		}
		out = append(out, section)
	}
	return out
}

// idsFromProto is nil when the list was not given at all (an update keeps the stored links), and non-nil, possibly empty,
// when it was.
func idsFromProto(in *catalogv1.IdList) []int64 {
	if in == nil {
		return nil
	}
	return append([]int64{}, in.GetIds()...)
}

func serviceRefsToProto(in []domain.ServiceRef) []*catalogv1.ServiceRef {
	var out []*catalogv1.ServiceRef
	for _, s := range in {
		out = append(out, &catalogv1.ServiceRef{Id: s.ID, Name: s.Name})
	}
	return out
}

func variantGroupToProto(g domain.VariantGroup) *catalogv1.VariantGroup {
	out := &catalogv1.VariantGroup{Id: g.ID, Name: g.Name, InternalNotes: g.InternalNotes, SortIndex: g.SortIndex, Services: serviceRefsToProto(g.Services),
		CreatedAt: timestamppb.New(g.CreatedAt), UpdatedAt: timestamppb.New(g.UpdatedAt)}
	for _, o := range g.Options {
		out.Options = append(out.Options, &catalogv1.VariantOption{
			Id: o.ID, Name: o.Name, PriceDelta: o.PriceDelta, IsDefault: o.IsDefault, Active: o.Active, SortIndex: o.SortIndex,
			EstimatedTime: o.EstimatedTime, EstimatedTimeUnit: o.EstimatedTimeUnit,
		})
	}
	return out
}

func variantGroupsToProto(in []domain.VariantGroup) []*catalogv1.VariantGroup {
	var out []*catalogv1.VariantGroup
	for _, g := range in {
		out = append(out, variantGroupToProto(g))
	}
	return out
}

func variantGroupFromProto(id int64, in *catalogv1.VariantGroup) domain.VariantGroup {
	g := domain.VariantGroup{ID: id, Name: in.GetName(), InternalNotes: in.GetInternalNotes()}
	for _, o := range in.GetOptions() {
		g.Options = append(g.Options, domain.VariantOption{
			ID: o.GetId(), Name: o.GetName(), PriceDelta: o.GetPriceDelta(), IsDefault: o.GetIsDefault(), Active: o.GetActive(),
			EstimatedTime: o.GetEstimatedTime(), EstimatedTimeUnit: o.GetEstimatedTimeUnit(),
		})
	}
	return g
}

func modifierGroupToProto(g domain.ModifierGroup) *catalogv1.ModifierGroup {
	out := &catalogv1.ModifierGroup{Id: g.ID, Name: g.Name, InternalNotes: g.InternalNotes, Min: g.Min, Max: g.Max, Services: serviceRefsToProto(g.Services),
		CreatedAt: timestamppb.New(g.CreatedAt), UpdatedAt: timestamppb.New(g.UpdatedAt)}
	for _, o := range g.Options {
		out.Options = append(out.Options, &catalogv1.ModifierOption{
			Id: o.ID, Name: o.Name, Description: o.Description, Price: o.Price, Active: o.Active, SortIndex: o.SortIndex,
		})
	}
	return out
}

func modifierGroupsToProto(in []domain.ModifierGroup) []*catalogv1.ModifierGroup {
	var out []*catalogv1.ModifierGroup
	for _, g := range in {
		out = append(out, modifierGroupToProto(g))
	}
	return out
}

func modifierGroupFromProto(id int64, in *catalogv1.ModifierGroup) domain.ModifierGroup {
	g := domain.ModifierGroup{ID: id, Name: in.GetName(), InternalNotes: in.GetInternalNotes(), Min: in.GetMin()}
	if in.Max != nil {
		m := in.GetMax()
		g.Max = &m
	}
	for _, o := range in.GetOptions() {
		g.Options = append(g.Options, domain.ModifierOption{ID: o.GetId(), Name: o.GetName(), Description: o.GetDescription(), Price: o.GetPrice(), Active: o.GetActive()})
	}
	return g
}

// ---- variant and modifier groups: reads need a signed-in admin too, since they carry the private notes ----

func (s *Server) ListVariantGroups(ctx context.Context, req *catalogv1.ListVariantGroupsRequest) (*catalogv1.ListVariantGroupsResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListVariantGroups.Handle(ctx, query.ListVariantGroups{Query: domain.GroupQuery{Q: req.GetQ(), SortBy: req.GetSortBy(), SortDir: req.GetSortDir(),
		Limit: int(req.GetLimit()), Offset: int(req.GetOffset()), ServiceID: req.GetServiceId(), Usage: req.GetUsage()}})
	if err != nil {
		return nil, err
	}
	return &catalogv1.ListVariantGroupsResponse{Groups: variantGroupsToProto(res.Groups), Total: res.Total}, nil
}

func (s *Server) GetVariantGroup(ctx context.Context, req *catalogv1.GetVariantGroupRequest) (*catalogv1.VariantGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	g, err := s.app.Queries.GetVariantGroup.Handle(ctx, query.GetVariantGroup{ID: req.GetId()})
	if err != nil {
		return nil, err
	}
	return &catalogv1.VariantGroupResponse{Group: variantGroupToProto(g)}, nil
}

func (s *Server) CreateVariantGroup(ctx context.Context, req *catalogv1.SaveVariantGroupRequest) (*catalogv1.VariantGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	g, err := s.app.Commands.CreateVariantGroup.Handle(ctx, command.CreateVariantGroup{Group: variantGroupFromProto(0, req.GetGroup())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.VariantGroupResponse{Group: variantGroupToProto(g)}, nil
}

func (s *Server) UpdateVariantGroup(ctx context.Context, req *catalogv1.SaveVariantGroupRequest) (*catalogv1.VariantGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	g, err := s.app.Commands.UpdateVariantGroup.Handle(ctx, command.UpdateVariantGroup{Group: variantGroupFromProto(req.GetId(), req.GetGroup())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.VariantGroupResponse{Group: variantGroupToProto(g)}, nil
}

func (s *Server) DeleteVariantGroup(ctx context.Context, req *catalogv1.DeleteGroupRequest) (*catalogv1.DeleteGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	return &catalogv1.DeleteGroupResponse{}, s.app.Commands.DeleteVariantGroup.Handle(ctx, command.DeleteVariantGroup{ID: req.GetId()})
}

func (s *Server) ListModifierGroups(ctx context.Context, req *catalogv1.ListModifierGroupsRequest) (*catalogv1.ListModifierGroupsResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListModifierGroups.Handle(ctx, query.ListModifierGroups{Query: domain.GroupQuery{Q: req.GetQ(), SortBy: req.GetSortBy(), SortDir: req.GetSortDir(),
		Requirement: req.GetRequirement(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
		ServiceID: req.GetServiceId(), Usage: req.GetUsage()}})
	if err != nil {
		return nil, err
	}
	return &catalogv1.ListModifierGroupsResponse{Groups: modifierGroupsToProto(res.Groups), Total: res.Total}, nil
}

func (s *Server) GetModifierGroup(ctx context.Context, req *catalogv1.GetModifierGroupRequest) (*catalogv1.ModifierGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	g, err := s.app.Queries.GetModifierGroup.Handle(ctx, query.GetModifierGroup{ID: req.GetId()})
	if err != nil {
		return nil, err
	}
	return &catalogv1.ModifierGroupResponse{Group: modifierGroupToProto(g)}, nil
}

func (s *Server) CreateModifierGroup(ctx context.Context, req *catalogv1.SaveModifierGroupRequest) (*catalogv1.ModifierGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	g, err := s.app.Commands.CreateModifierGroup.Handle(ctx, command.CreateModifierGroup{Group: modifierGroupFromProto(0, req.GetGroup())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.ModifierGroupResponse{Group: modifierGroupToProto(g)}, nil
}

func (s *Server) UpdateModifierGroup(ctx context.Context, req *catalogv1.SaveModifierGroupRequest) (*catalogv1.ModifierGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	g, err := s.app.Commands.UpdateModifierGroup.Handle(ctx, command.UpdateModifierGroup{Group: modifierGroupFromProto(req.GetId(), req.GetGroup())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.ModifierGroupResponse{Group: modifierGroupToProto(g)}, nil
}

func (s *Server) DeleteModifierGroup(ctx context.Context, req *catalogv1.DeleteGroupRequest) (*catalogv1.DeleteGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	return &catalogv1.DeleteGroupResponse{}, s.app.Commands.DeleteModifierGroup.Handle(ctx, command.DeleteModifierGroup{ID: req.GetId()})
}
