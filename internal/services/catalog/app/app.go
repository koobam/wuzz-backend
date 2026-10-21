// Package app holds catalog's use cases. The Store port is implemented by
// adapters/postgres and the Discovery port by adapters/mongo; nothing here
// knows either database exists.
package app

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

type Store interface {
	ListOutlets(ctx context.Context, ids []int64, includeInactive bool) ([]domain.Outlet, error)
	GetOutlet(ctx context.Context, id int64) (domain.Outlet, error)
	CreateOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error)
	UpdateOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error)

	ListServices(ctx context.Context, includeInactive bool) ([]domain.ServiceItem, error)
	CreateService(ctx context.Context, s domain.ServiceItem) (domain.ServiceItem, error)
	UpdateService(ctx context.Context, s domain.ServiceItem) (domain.ServiceItem, error)
}

// Discovery is the read side the customer app browses: geo and text search
// over outlets, backed by MongoDB.
type Discovery interface {
	// Nearby returns up to q.Limit outlets and the cursor of the next page ("" if none).
	Nearby(ctx context.Context, q domain.NearbyQuery) ([]domain.OutletView, string, error)
	// Coverage returns the service area containing p; ok is false outside every area.
	Coverage(ctx context.Context, p domain.Point) (area domain.CoverageArea, ok bool, err error)
	// ByID returns one outlet; the distance is filled in when near is set.
	ByID(ctx context.Context, id int64, near *domain.Point) (domain.OutletView, error)
	// Services lists active services for the app; category "" means every category.
	Services(ctx context.Context, category string, limit int) ([]domain.ServiceView, error)
	ServiceDetail(ctx context.Context, id int64) (domain.ServiceDetail, error)
}

type App struct {
	Store
	Discovery Discovery
}

func New(store Store, discovery Discovery) *App { return &App{Store: store, Discovery: discovery} }

// NearbyOutlets pages through active outlets. On the first page an explicitly
// selected outlet is pinned to the top (unless a text query is narrowing the
// list), so the picker shows what the user already chose.
func (a *App) NearbyOutlets(ctx context.Context, q domain.NearbyQuery) ([]domain.OutletView, string, error) {
	if q.Limit <= 0 {
		q.Limit = domain.DefaultPageSize
	}
	if q.Limit > domain.MaxPageSize {
		q.Limit = domain.MaxPageSize
	}
	outlets, next, err := a.Discovery.Nearby(ctx, q)
	if err != nil {
		return nil, "", err
	}
	if q.Cursor != "" || q.SelectedID <= 0 || q.Text != "" {
		return outlets, next, nil
	}
	selected, err := a.Discovery.ByID(ctx, q.SelectedID, q.Near)
	if err != nil {
		// A stale or unknown selection must not break the list.
		return outlets, next, nil
	}
	pinned := make([]domain.OutletView, 0, len(outlets)+1)
	pinned = append(pinned, selected)
	for _, o := range outlets {
		if o.ID != selected.ID {
			pinned = append(pinned, o)
		}
	}
	if len(pinned) > q.Limit {
		pinned = pinned[:q.Limit]
	}
	return pinned, next, nil
}

// CreateOutlet validates before it reaches the store.
func (a *App) AddOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error) {
	o.Active = true
	if err := o.Validate(); err != nil {
		return domain.Outlet{}, err
	}
	return a.Store.CreateOutlet(ctx, o)
}

func (a *App) ChangeOutlet(ctx context.Context, o domain.Outlet) (domain.Outlet, error) {
	if err := o.Validate(); err != nil {
		return domain.Outlet{}, err
	}
	return a.Store.UpdateOutlet(ctx, o)
}

func (a *App) AddService(ctx context.Context, s domain.ServiceItem) (domain.ServiceItem, error) {
	s.Active = true
	if err := s.Validate(); err != nil {
		return domain.ServiceItem{}, err
	}
	return a.Store.CreateService(ctx, s)
}

func (a *App) ChangeService(ctx context.Context, s domain.ServiceItem) (domain.ServiceItem, error) {
	if err := s.Validate(); err != nil {
		return domain.ServiceItem{}, err
	}
	return a.Store.UpdateService(ctx, s)
}

// BrowseServices lists the services the app shows, optionally for one category.
func (a *App) BrowseServices(ctx context.Context, category string, limit int) ([]domain.ServiceView, error) {
	if category != "" && category != domain.ServiceCategoryRegular && category != domain.ServiceCategoryAlacarte {
		return nil, apperr.Invalid("service_category must be regular or alacarte")
	}
	return a.Discovery.Services(ctx, category, limit)
}

// ResolveCoverage says which service area a position is in, if any.
func (a *App) ResolveCoverage(ctx context.Context, p domain.Point) (domain.CoverageArea, bool, error) {
	return a.Discovery.Coverage(ctx, p)
}
