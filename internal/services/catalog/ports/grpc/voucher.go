package grpc

import (
	"context"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/command"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/query"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// voucherToProto is a voucher at the instant now: is_live is the server's judgement, never the client's.
func voucherToProto(v domain.Voucher, now time.Time) *catalogv1.Voucher {
	out := &catalogv1.Voucher{
		Id: v.ID, Code: v.Code, Title: v.Title, Description: v.Description, InternalNotes: v.InternalNotes, DiscountType: v.DiscountType, Value: v.Value,
		MaxDiscount: v.MaxDiscount, MinSpend: v.MinSpend, MaxClaims: v.MaxClaims, ClaimsCount: v.ClaimsCount, UsageLimit: v.UsageLimit, Status: v.Status,
		Color: v.Color, ImageUrl: v.ImageURL, StartsAt: optTime(v.StartsAt), EndsAt: optTime(v.EndsAt), OutletIds: v.OutletIDs, ServiceIds: v.ServiceIDs,
		IsLive: v.IsLive(now), Type: v.Type(), CreatedAt: timestamppb.New(v.CreatedAt), UpdatedAt: timestamppb.New(v.UpdatedAt),
	}
	for _, o := range v.Outlets {
		out.Outlets = append(out.Outlets, &catalogv1.BannerOutlet{Id: o.ID, Name: o.Name})
	}
	for _, s := range v.Services {
		out.Services = append(out.Services, &catalogv1.VoucherService{Id: s.ID, Name: s.Name})
	}
	return out
}

func voucherInput(in *catalogv1.Voucher) domain.VoucherInput {
	return domain.VoucherInput{Code: in.GetCode(), Title: in.GetTitle(), Description: in.GetDescription(), InternalNotes: in.GetInternalNotes(),
		DiscountType: in.GetDiscountType(), Status: in.GetStatus(), Color: in.GetColor(), ImageURL: in.GetImageUrl(), Value: in.GetValue(), MaxDiscount: in.GetMaxDiscount(),
		MinSpend: in.GetMinSpend(), MaxClaims: in.GetMaxClaims(), UsageLimit: in.GetUsageLimit(), StartsAt: fromOptTime(in.GetStartsAt()), EndsAt: fromOptTime(in.GetEndsAt()),
		OutletIDs: in.GetOutletIds(), ServiceIDs: in.GetServiceIds()}
}

func injectionToProto(i domain.Injection) *catalogv1.VoucherInjection {
	return &catalogv1.VoucherInjection{Id: i.ID, VoucherId: i.VoucherID, VoucherTitle: i.VoucherTitle, VoucherCode: i.VoucherCode, ActorId: i.ActorID, ActorName: i.ActorName,
		TargetCount: i.Target, InjectedCount: i.Injected, SkippedAlreadyHeld: i.AlreadyHeld, SkippedOverLimit: i.OverLimit, SkippedNotFound: i.NotFound,
		SkippedInactive: i.Inactive, CreatedAt: timestamppb.New(i.CreatedAt)}
}

// myVoucherToProto is a wallet entry for the customer: nothing private leaves here.
func myVoucherToProto(w domain.WalletVoucher) *catalogv1.MyVoucher {
	out := &catalogv1.MyVoucher{
		Id: w.ID, Code: w.Code, Title: w.Title, Description: w.Description, DiscountType: w.DiscountType, Value: w.Value, MaxDiscount: w.MaxDiscount,
		MinSpend: w.MinSpend, Color: w.Color, ImageUrl: w.ImageURL, StartsAt: optTime(w.StartsAt), ExpiresAt: optTime(w.ExpiresAt()), OutletIds: w.OutletIDs,
		ServiceIds: w.ServiceIDs, Source: w.Source, ClaimedAt: optTime(w.GrantedAt), UsesRemaining: w.UsesRemaining, UsageLimit: w.UsageLimit,
	}
	for _, o := range w.Outlets {
		out.Outlets = append(out.Outlets, &catalogv1.BannerOutlet{Id: o.ID, Name: o.Name})
	}
	for _, s := range w.Services {
		out.Services = append(out.Services, &catalogv1.VoucherService{Id: s.ID, Name: s.Name})
	}
	return out
}

// requireCustomer is the identity of a customer: a verified token whose subject is a customer id and that carries no staff role.
func requireCustomer(ctx context.Context) (int64, error) {
	id, err := authn.Require(ctx)
	if err != nil {
		return 0, err
	}
	n, perr := strconv.ParseInt(id.UserID, 10, 64)
	if perr != nil || n <= 0 || len(id.Roles) > 0 {
		return 0, apperr.Forbidden("Only a customer has a voucher wallet")
	}
	return n, nil
}

func (s *Server) ListVouchers(ctx context.Context, req *catalogv1.ListVouchersRequest) (*catalogv1.ListVouchersResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListVouchers.Handle(ctx, query.ListVouchers{Query: domain.VoucherQuery{Q: req.GetQ(), Status: req.GetStatus(), Live: req.GetLive(),
		Period: req.GetPeriod(), DiscountType: req.GetDiscountType(), OutletID: req.GetOutletId(), SortBy: req.GetSortBy(), SortDir: req.GetSortDir(),
		Limit: int(req.GetLimit()), Offset: int(req.GetOffset())}})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ListVouchersResponse{Total: res.Total}
	for _, v := range res.Vouchers {
		out.Vouchers = append(out.Vouchers, voucherToProto(v, res.Now))
	}
	return out, nil
}

func (s *Server) GetVoucher(ctx context.Context, req *catalogv1.GetVoucherRequest) (*catalogv1.VoucherResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	v, err := s.app.Queries.GetVoucher.Handle(ctx, query.GetVoucher{ID: req.GetId()})
	if err != nil {
		return nil, err
	}
	return &catalogv1.VoucherResponse{Voucher: voucherToProto(v, time.Now())}, nil
}

func (s *Server) CreateVoucher(ctx context.Context, req *catalogv1.SaveVoucherRequest) (*catalogv1.VoucherResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	v, err := s.app.Commands.CreateVoucher.Handle(ctx, command.CreateVoucher{Input: voucherInput(req.GetVoucher())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.VoucherResponse{Voucher: voucherToProto(v, time.Now())}, nil
}

func (s *Server) UpdateVoucher(ctx context.Context, req *catalogv1.SaveVoucherRequest) (*catalogv1.VoucherResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	v, err := s.app.Commands.UpdateVoucher.Handle(ctx, command.UpdateVoucher{ID: req.GetId(), Input: voucherInput(req.GetVoucher())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.VoucherResponse{Voucher: voucherToProto(v, time.Now())}, nil
}

func (s *Server) SetVoucherStatus(ctx context.Context, req *catalogv1.SetVoucherStatusRequest) (*catalogv1.VoucherResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	v, err := s.app.Commands.SetVoucherStatus.Handle(ctx, command.SetVoucherStatus{ID: req.GetId(), Status: req.GetStatus()})
	if err != nil {
		return nil, err
	}
	return &catalogv1.VoucherResponse{Voucher: voucherToProto(v, time.Now())}, nil
}

func (s *Server) DeleteVoucher(ctx context.Context, req *catalogv1.GetVoucherRequest) (*catalogv1.DeleteGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := s.app.Commands.DeleteVoucher.Handle(ctx, command.DeleteVoucher{ID: req.GetId()}); err != nil {
		return nil, err
	}
	return &catalogv1.DeleteGroupResponse{}, nil
}

func (s *Server) ListVoucherColors(ctx context.Context, _ *catalogv1.ListVoucherColorsRequest) (*catalogv1.ListVoucherColorsResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	return &catalogv1.ListVoucherColorsResponse{Colors: domain.VoucherColors}, nil
}

func (s *Server) ListVoucherGrants(ctx context.Context, req *catalogv1.ListVoucherGrantsRequest) (*catalogv1.ListVoucherGrantsResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListVoucherGrants.Handle(ctx, query.ListVoucherGrants{Query: domain.GrantQuery{VoucherID: req.GetVoucherId(), Q: req.GetQ(),
		Limit: int(req.GetLimit()), Offset: int(req.GetOffset())}})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ListVoucherGrantsResponse{Total: res.Total}
	for _, g := range res.Grants {
		out.Grants = append(out.Grants, &catalogv1.VoucherGrant{Id: g.ID, VoucherId: g.VoucherID, CustomerId: g.CustomerID, CustomerName: g.CustomerName,
			CustomerPhone: g.CustomerPhone, Source: g.Source, GrantedBy: g.GrantedBy, GrantedAt: timestamppb.New(g.GrantedAt), ExpiresAt: optTime(g.ExpiresAt),
			UsesRemaining: g.UsesRemaining})
	}
	return out, nil
}

// InjectVoucher puts the voucher in the wallets of customers the user service has already resolved; the actor is the admin the token names.
func (s *Server) InjectVoucher(ctx context.Context, req *catalogv1.InjectVoucherRequest) (*catalogv1.InjectVoucherResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	who, _ := authn.FromContext(ctx)
	in := domain.InjectInput{VoucherID: req.GetVoucherId(), NotFound: req.GetNotFound(), ExpiresAt: fromOptTime(req.GetExpiresAt())}
	for _, c := range req.GetCustomers() {
		in.Customers = append(in.Customers, domain.InjectCustomer{ID: c.GetId(), Name: c.GetName(), Phone: c.GetPhone(), Active: c.GetActive()})
	}
	res, err := s.app.Commands.InjectVoucher.Handle(ctx, command.InjectVoucher{Input: in, Actor: command.Actor{ID: who.UserID, Name: who.Name}})
	if err != nil {
		return nil, err
	}
	return &catalogv1.InjectVoucherResponse{Injection: injectionToProto(res)}, nil
}

func (s *Server) ListVoucherInjections(ctx context.Context, req *catalogv1.ListVoucherInjectionsRequest) (*catalogv1.ListVoucherInjectionsResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListInjections.Handle(ctx, query.ListInjections{Query: domain.InjectionQuery{VoucherID: req.GetVoucherId(),
		Limit: int(req.GetLimit()), Offset: int(req.GetOffset())}})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ListVoucherInjectionsResponse{Total: res.Total}
	for _, i := range res.Injections {
		out.Injections = append(out.Injections, injectionToProto(i))
	}
	return out, nil
}

// ListMyVouchers is the signed-in customer's wallet.
func (s *Server) ListMyVouchers(ctx context.Context, _ *catalogv1.ListMyVouchersRequest) (*catalogv1.ListMyVouchersResponse, error) {
	id, err := requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.app.Queries.ListWallet.Handle(ctx, query.ListWallet{CustomerID: id})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ListMyVouchersResponse{Vouchers: []*catalogv1.MyVoucher{}}
	for _, w := range items {
		out.Vouchers = append(out.Vouchers, myVoucherToProto(w))
	}
	return out, nil
}

// ClaimVoucher puts the voucher with the typed code in the signed-in customer's wallet. The customer is the token's, never the request's.
func (s *Server) ClaimVoucher(ctx context.Context, req *catalogv1.ClaimVoucherRequest) (*catalogv1.MyVoucherResponse, error) {
	id, err := requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	w, err := s.app.Commands.ClaimVoucher.Handle(ctx, command.ClaimVoucher{Input: domain.ClaimInput{Code: req.GetCode(), CustomerID: id,
		CustomerName: req.GetCustomerName(), CustomerPhone: req.GetCustomerPhone()}})
	if err != nil {
		return nil, err
	}
	return &catalogv1.MyVoucherResponse{Voucher: myVoucherToProto(w)}, nil
}
