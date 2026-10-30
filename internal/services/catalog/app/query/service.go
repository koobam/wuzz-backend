package query

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Services is what the staff-side service reads need of the store.
type Services interface {
	ListServices(ctx context.Context, includeInactive bool) ([]domain.ServiceItem, error)
	ListServiceCategories(ctx context.Context) ([]domain.ServiceCategory, error)
	// PageServices is the back office's table: one page of the services the query matches, and how many match in all.
	PageServices(ctx context.Context, q domain.ServiceQuery) ([]domain.ServiceItem, int64, error)
	// TaxRates is the rate of each tax type, in basis points.
	TaxRates(ctx context.Context) (domain.TaxRates, error)
}

// ServiceDiscovery is the read side the customer app browses the service menu through.
type ServiceDiscovery interface {
	// Services lists active services for the app; category "" means every category.
	Services(ctx context.Context, category string, limit int) ([]domain.ServiceView, error)
	ServiceDetail(ctx context.Context, id int64) (domain.ServiceDetail, error)
}

// ---------- ListServices ----------

type ListServices struct{ IncludeInactive bool }

type ListServicesHandler struct{ services Services }

func NewListServicesHandler(services Services) *ListServicesHandler {
	return &ListServicesHandler{services: services}
}

func (h *ListServicesHandler) Handle(ctx context.Context, q ListServices) ([]domain.ServiceItem, error) {
	return h.services.ListServices(ctx, q.IncludeInactive)
}

// ---------- PageServices ----------

type PageServices struct{ Query domain.ServiceQuery }

type PageServicesResult struct {
	Services []domain.ServiceItem
	Total    int64
}

type PageServicesHandler struct{ services Services }

func NewPageServicesHandler(services Services) *PageServicesHandler {
	return &PageServicesHandler{services: services}
}

func (h *PageServicesHandler) Handle(ctx context.Context, q PageServices) (PageServicesResult, error) {
	if err := q.Query.Validate(); err != nil {
		return PageServicesResult{}, err
	}
	items, total, err := h.services.PageServices(ctx, q.Query)
	return PageServicesResult{Services: items, Total: total}, err
}

// ---------- ListServiceCategories ----------

type ListServiceCategories struct{}

type ListServiceCategoriesHandler struct{ services Services }

func NewListServiceCategoriesHandler(services Services) *ListServiceCategoriesHandler {
	return &ListServiceCategoriesHandler{services: services}
}

func (h *ListServiceCategoriesHandler) Handle(ctx context.Context, _ ListServiceCategories) ([]domain.ServiceCategory, error) {
	return h.services.ListServiceCategories(ctx)
}

// ---------- BrowseServices ----------

type BrowseServices struct {
	// Category is "regular", "alacarte", or "" for every category.
	Category string
	Limit    int
}

type BrowseServicesHandler struct{ discovery ServiceDiscovery }

func NewBrowseServicesHandler(discovery ServiceDiscovery) *BrowseServicesHandler {
	return &BrowseServicesHandler{discovery: discovery}
}

// Handle lists the services the app shows, optionally for one category.
func (h *BrowseServicesHandler) Handle(ctx context.Context, q BrowseServices) ([]domain.ServiceView, error) {
	if q.Category != "" && q.Category != domain.ServiceCategoryRegular && q.Category != domain.ServiceCategoryAlacarte {
		return nil, apperr.Invalid("service_category must be regular or alacarte")
	}
	return h.discovery.Services(ctx, q.Category, q.Limit)
}

// ---------- GetServiceDetail ----------

type GetServiceDetail struct{ ID int64 }

type GetServiceDetailHandler struct{ discovery ServiceDiscovery }

func NewGetServiceDetailHandler(discovery ServiceDiscovery) *GetServiceDetailHandler {
	return &GetServiceDetailHandler{discovery: discovery}
}

func (h *GetServiceDetailHandler) Handle(ctx context.Context, q GetServiceDetail) (domain.ServiceDetail, error) {
	return h.discovery.ServiceDetail(ctx, q.ID)
}

// ---------- ListTaxRates ----------

type ListTaxRates struct{}

type ListTaxRatesHandler struct{ services Services }

func NewListTaxRatesHandler(services Services) *ListTaxRatesHandler {
	return &ListTaxRatesHandler{services: services}
}

// Handle answers the rate of every tax type, so a form can preview a price after tax while it is typed.
func (h *ListTaxRatesHandler) Handle(ctx context.Context, _ ListTaxRates) (domain.TaxRates, error) {
	return h.services.TaxRates(ctx)
}
