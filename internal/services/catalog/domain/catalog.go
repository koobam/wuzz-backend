// Package domain holds catalog's business rules: outlets and the laundry
// services sold. No Postgres, gRPC or JSON in here.
package domain

import (
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

type Outlet struct {
	ID        int64
	Name      string
	Address   string
	Phone     string
	Latitude  float64
	Longitude float64
	Active    bool
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate normalises and checks an outlet before it is stored.
func (o *Outlet) Validate() error {
	o.Name = strings.TrimSpace(o.Name)
	o.Address = strings.TrimSpace(o.Address)
	o.Phone = strings.TrimSpace(o.Phone)
	if o.Name == "" {
		return apperr.Invalid("An outlet name is required")
	}
	if o.Latitude < -90 || o.Latitude > 90 || o.Longitude < -180 || o.Longitude > 180 {
		return apperr.Invalid("Latitude or longitude is out of range")
	}
	return nil
}

// Units a service can be priced in.
const (
	UnitKg  = "kg"
	UnitPcs = "pcs"
)

type ServiceItem struct {
	ID        int64
	Name      string
	Unit      string
	Price     int64 // whole rupiah
	Active    bool
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *ServiceItem) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return apperr.Invalid("A service name is required")
	}
	if s.Unit != UnitKg && s.Unit != UnitPcs {
		return apperr.Invalid("unit must be kg or pcs")
	}
	if s.Price < 0 {
		return apperr.Invalid("price cannot be negative")
	}
	return nil
}
