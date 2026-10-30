package command

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Services is what the service writes need of the store.
type Services interface {
	CreateService(ctx context.Context, s domain.ServiceItem) (domain.ServiceItem, error)
	UpdateService(ctx context.Context, s domain.ServiceItem) (domain.ServiceItem, error)
	// DeleteService removes the service with its group links and its own recipe.
	DeleteService(ctx context.Context, id int64) error
}

// ---------- CreateService ----------

type CreateService struct{ Service domain.ServiceItem }

type CreateServiceHandler struct{ services Services }

func NewCreateServiceHandler(services Services) *CreateServiceHandler {
	return &CreateServiceHandler{services: services}
}

// Handle validates the service before it reaches the store. A new service is always active, and untaxed and undiscounted unless it says otherwise.
func (h *CreateServiceHandler) Handle(ctx context.Context, cmd CreateService) (domain.ServiceItem, error) {
	s := cmd.Service
	s.Active = true
	if s.TaxType == "" {
		s.TaxType = domain.TaxNone
	}
	if s.DiscountType == "" {
		s.DiscountType = domain.DiscountNone
	}
	if err := s.Validate(); err != nil {
		return domain.ServiceItem{}, err
	}
	return h.services.CreateService(ctx, s)
}

// ---------- UpdateService ----------

type UpdateService struct{ Service domain.ServiceItem }

type UpdateServiceHandler struct{ services Services }

func NewUpdateServiceHandler(services Services) *UpdateServiceHandler {
	return &UpdateServiceHandler{services: services}
}

func (h *UpdateServiceHandler) Handle(ctx context.Context, cmd UpdateService) (domain.ServiceItem, error) {
	s := cmd.Service
	if err := s.Validate(); err != nil {
		return domain.ServiceItem{}, err
	}
	return h.services.UpdateService(ctx, s)
}

// ---------- DeleteService ----------

type DeleteService struct{ ID int64 }

type DeleteServiceHandler struct{ services Services }

func NewDeleteServiceHandler(services Services) *DeleteServiceHandler {
	return &DeleteServiceHandler{services: services}
}

func (h *DeleteServiceHandler) Handle(ctx context.Context, cmd DeleteService) error {
	return h.services.DeleteService(ctx, cmd.ID)
}
