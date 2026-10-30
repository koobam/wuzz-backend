package grpc

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/command"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/query"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

func optTime(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func fromOptTime(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}

// bannerToProto is a banner at the instant now: is_live is the server's judgement, never the client's.
func bannerToProto(b domain.Banner, now time.Time) *catalogv1.Banner {
	out := &catalogv1.Banner{
		Id: b.ID, Title: b.Title, InternalNotes: b.InternalNotes, ImageUrl: b.ImageURL, TargetType: b.TargetType, TargetValue: b.TargetValue,
		Status: b.Status, SortIndex: b.SortIndex, StartsAt: optTime(b.StartsAt), EndsAt: optTime(b.EndsAt), OutletIds: b.OutletIDs,
		IsLive: b.IsLive(now), Deeplink: b.Deeplink(), CreatedAt: timestamppb.New(b.CreatedAt), UpdatedAt: timestamppb.New(b.UpdatedAt),
	}
	for _, o := range b.Outlets {
		out.Outlets = append(out.Outlets, &catalogv1.BannerOutlet{Id: o.ID, Name: o.Name})
	}
	return out
}

func bannerInput(in *catalogv1.Banner) domain.BannerInput {
	return domain.BannerInput{Title: in.GetTitle(), InternalNotes: in.GetInternalNotes(), ImageURL: in.GetImageUrl(), TargetType: in.GetTargetType(),
		TargetValue: in.GetTargetValue(), Status: in.GetStatus(), StartsAt: fromOptTime(in.GetStartsAt()), EndsAt: fromOptTime(in.GetEndsAt()), OutletIDs: in.GetOutletIds()}
}

func (s *Server) ListBanners(ctx context.Context, req *catalogv1.ListBannersRequest) (*catalogv1.ListBannersResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListBanners.Handle(ctx, query.ListBanners{Query: domain.BannerQuery{Q: req.GetQ(), Status: req.GetStatus(), Live: req.GetLive(),
		OutletID: req.GetOutletId(), SortBy: req.GetSortBy(), SortDir: req.GetSortDir(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset())}})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ListBannersResponse{Total: res.Total}
	for _, b := range res.Banners {
		out.Banners = append(out.Banners, bannerToProto(b, res.Now))
	}
	return out, nil
}

func (s *Server) GetBanner(ctx context.Context, req *catalogv1.GetBannerRequest) (*catalogv1.BannerResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	b, err := s.app.Queries.GetBanner.Handle(ctx, query.GetBanner{ID: req.GetId()})
	if err != nil {
		return nil, err
	}
	return &catalogv1.BannerResponse{Banner: bannerToProto(b, time.Now())}, nil
}

func (s *Server) CreateBanner(ctx context.Context, req *catalogv1.SaveBannerRequest) (*catalogv1.BannerResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	b, err := s.app.Commands.CreateBanner.Handle(ctx, command.CreateBanner{Input: bannerInput(req.GetBanner())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.BannerResponse{Banner: bannerToProto(b, time.Now())}, nil
}

func (s *Server) UpdateBanner(ctx context.Context, req *catalogv1.SaveBannerRequest) (*catalogv1.BannerResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	b, err := s.app.Commands.UpdateBanner.Handle(ctx, command.UpdateBanner{ID: req.GetId(), Input: bannerInput(req.GetBanner())})
	if err != nil {
		return nil, err
	}
	return &catalogv1.BannerResponse{Banner: bannerToProto(b, time.Now())}, nil
}

func (s *Server) SetBannerStatus(ctx context.Context, req *catalogv1.SetBannerStatusRequest) (*catalogv1.BannerResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	b, err := s.app.Commands.SetBannerStatus.Handle(ctx, command.SetBannerStatus{ID: req.GetId(), Status: req.GetStatus()})
	if err != nil {
		return nil, err
	}
	return &catalogv1.BannerResponse{Banner: bannerToProto(b, time.Now())}, nil
}

func (s *Server) DeleteBanner(ctx context.Context, req *catalogv1.GetBannerRequest) (*catalogv1.DeleteGroupResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := s.app.Commands.DeleteBanner.Handle(ctx, command.DeleteBanner{ID: req.GetId()}); err != nil {
		return nil, err
	}
	return &catalogv1.DeleteGroupResponse{}, nil
}

// ReorderBanners writes the order and answers with the whole list in it.
func (s *Server) ReorderBanners(ctx context.Context, req *catalogv1.ReorderBannersRequest) (*catalogv1.ListBannersResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := s.app.Commands.ReorderBanners.Handle(ctx, command.ReorderBanners{IDs: req.GetIds()}); err != nil {
		return nil, err
	}
	return s.ListBanners(ctx, &catalogv1.ListBannersRequest{SortBy: "sort_index", Limit: 100})
}

func (s *Server) ListBannerScreens(ctx context.Context, _ *catalogv1.ListBannerScreensRequest) (*catalogv1.ListBannerScreensResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	return &catalogv1.ListBannerScreensResponse{Screens: domain.BannerScreens}, nil
}

// ListLiveBanners is public: the customer app reads it without signing in. Nothing private leaves here — the internal note and the
// status are blanked, and so is the outlet limit.
func (s *Server) ListLiveBanners(ctx context.Context, req *catalogv1.ListLiveBannersRequest) (*catalogv1.ListLiveBannersResponse, error) {
	banners, err := s.app.Queries.ListLiveBanners.Handle(ctx, query.ListLiveBanners{OutletID: req.GetOutletId()})
	if err != nil {
		return nil, err
	}
	out := &catalogv1.ListLiveBannersResponse{}
	for _, b := range banners {
		out.Banners = append(out.Banners, &catalogv1.Banner{Id: b.ID, Title: b.Title, ImageUrl: b.ImageURL, TargetType: b.TargetType, TargetValue: b.TargetValue,
			SortIndex: b.SortIndex, IsLive: true, Deeplink: b.Deeplink()})
	}
	return out, nil
}
