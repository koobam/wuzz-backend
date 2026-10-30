package command

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Banners is what the banner writes need of the store.
type Banners interface {
	// CreateBanner puts the banner last in the carousel. It refuses a service, outlet or limit that does not exist.
	CreateBanner(ctx context.Context, b domain.Banner) (domain.Banner, error)
	// UpdateBanner replaces everything but the banner's place in the carousel.
	UpdateBanner(ctx context.Context, b domain.Banner) (domain.Banner, error)
	// SetBannerStatus switches a banner on or off; it refuses to switch one on that has no picture.
	SetBannerStatus(ctx context.Context, id int64, status string) (domain.Banner, error)
	DeleteBanner(ctx context.Context, id int64) error
	// ReorderBanners writes the carousel's order; ids must be every banner, once, and the list is read back in the new order.
	ReorderBanners(ctx context.Context, ids []int64) error
}

// ---------- CreateBanner ----------

type CreateBanner struct{ Input domain.BannerInput }

type CreateBannerHandler struct{ banners Banners }

func NewCreateBannerHandler(banners Banners) *CreateBannerHandler {
	return &CreateBannerHandler{banners: banners}
}

func (h *CreateBannerHandler) Handle(ctx context.Context, cmd CreateBanner) (domain.Banner, error) {
	b, err := domain.ParseBanner(cmd.Input)
	if err != nil {
		return domain.Banner{}, err
	}
	return h.banners.CreateBanner(ctx, b)
}

// ---------- UpdateBanner ----------

type UpdateBanner struct {
	ID    int64
	Input domain.BannerInput
}

type UpdateBannerHandler struct{ banners Banners }

func NewUpdateBannerHandler(banners Banners) *UpdateBannerHandler {
	return &UpdateBannerHandler{banners: banners}
}

func (h *UpdateBannerHandler) Handle(ctx context.Context, cmd UpdateBanner) (domain.Banner, error) {
	b, err := domain.ParseBanner(cmd.Input)
	if err != nil {
		return domain.Banner{}, err
	}
	b.ID = cmd.ID
	return h.banners.UpdateBanner(ctx, b)
}

// ---------- SetBannerStatus ----------

type SetBannerStatus struct {
	ID     int64
	Status string
}

type SetBannerStatusHandler struct{ banners Banners }

func NewSetBannerStatusHandler(banners Banners) *SetBannerStatusHandler {
	return &SetBannerStatusHandler{banners: banners}
}

func (h *SetBannerStatusHandler) Handle(ctx context.Context, cmd SetBannerStatus) (domain.Banner, error) {
	if cmd.Status != domain.BannerActive && cmd.Status != domain.BannerInactive {
		return domain.Banner{}, apperr.Invalid("The status must be active or inactive")
	}
	return h.banners.SetBannerStatus(ctx, cmd.ID, cmd.Status)
}

// ---------- DeleteBanner ----------

type DeleteBanner struct{ ID int64 }

type DeleteBannerHandler struct{ banners Banners }

func NewDeleteBannerHandler(banners Banners) *DeleteBannerHandler {
	return &DeleteBannerHandler{banners: banners}
}

func (h *DeleteBannerHandler) Handle(ctx context.Context, cmd DeleteBanner) error {
	return h.banners.DeleteBanner(ctx, cmd.ID)
}

// ---------- ReorderBanners ----------

type ReorderBanners struct{ IDs []int64 }

type ReorderBannersHandler struct{ banners Banners }

func NewReorderBannersHandler(banners Banners) *ReorderBannersHandler {
	return &ReorderBannersHandler{banners: banners}
}

func (h *ReorderBannersHandler) Handle(ctx context.Context, cmd ReorderBanners) error {
	return h.banners.ReorderBanners(ctx, cmd.IDs)
}
