package query

import (
	"context"
	"time"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Banners is what the banner reads need of the store.
type Banners interface {
	ListBanners(ctx context.Context, q domain.BannerQuery) ([]domain.Banner, int64, error)
	GetBanner(ctx context.Context, id int64) (domain.Banner, error)
	// ListLiveBanners are the banners on a phone at now that apply to the outlet (0 = none known: only those for every outlet), in the
	// carousel's order.
	ListLiveBanners(ctx context.Context, outletID int64, now time.Time) ([]domain.Banner, error)
}

// ---------- ListBanners ----------

type ListBanners struct{ Query domain.BannerQuery }

type ListBannersResult struct {
	Banners []domain.Banner
	Total   int64
	// Now is the clock the list was judged by, so each banner's liveness is read at the same instant as the filter.
	Now time.Time
}

type ListBannersHandler struct {
	banners Banners
	now     func() time.Time
}

func NewListBannersHandler(banners Banners) *ListBannersHandler {
	return &ListBannersHandler{banners: banners, now: time.Now}
}

func (h *ListBannersHandler) Handle(ctx context.Context, q ListBanners) (ListBannersResult, error) {
	if err := q.Query.Validate(); err != nil {
		return ListBannersResult{}, err
	}
	q.Query.Now = h.now()
	items, total, err := h.banners.ListBanners(ctx, q.Query)
	return ListBannersResult{Banners: items, Total: total, Now: q.Query.Now}, err
}

// ---------- GetBanner ----------

type GetBanner struct{ ID int64 }

type GetBannerHandler struct{ banners Banners }

func NewGetBannerHandler(banners Banners) *GetBannerHandler {
	return &GetBannerHandler{banners: banners}
}

func (h *GetBannerHandler) Handle(ctx context.Context, q GetBanner) (domain.Banner, error) {
	return h.banners.GetBanner(ctx, q.ID)
}

// ---------- ListLiveBanners ----------

type ListLiveBanners struct{ OutletID int64 }

type ListLiveBannersHandler struct {
	banners Banners
	now     func() time.Time
}

func NewListLiveBannersHandler(banners Banners) *ListLiveBannersHandler {
	return &ListLiveBannersHandler{banners: banners, now: time.Now}
}

func (h *ListLiveBannersHandler) Handle(ctx context.Context, q ListLiveBanners) ([]domain.Banner, error) {
	return h.banners.ListLiveBanners(ctx, q.OutletID, h.now())
}
