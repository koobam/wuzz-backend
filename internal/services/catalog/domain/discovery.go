package domain

// OutletView is an outlet as the customer app browses it. Unlike Outlet it
// carries the discovery-only fields (status, category, photos, hours,
// fulfilment) and, when the caller supplied a location, the distance to it.
type OutletView struct {
	ID            int64
	Name          string
	Address       string
	Category      string // laundry | caffe | all
	Latitude      float64
	Longitude     float64
	Images        []string
	CurrentStatus string   // quiet | moderate | busy
	DeliveryTypes []string // pickup | delivery
	OpenTime      string   // wall-clock HH:MM
	CloseTime     string
	HasDistance   bool
	DistanceKm    float64
}

// Point is a position on the map.
type Point struct{ Latitude, Longitude float64 }

// NearbyQuery asks for a page of outlets, nearest first when Near is set and by
// name otherwise.
type NearbyQuery struct {
	Near       *Point
	Limit      int
	Cursor     string
	SelectedID int64
	Text       string
}

const (
	DefaultPageSize = 10
	MaxPageSize     = 50
)

// ServiceView is a laundry service as the customer app browses it.
type ServiceView struct {
	ID              int64
	Name            string
	Description     string
	Price           int64
	Unit            string // kg | pcs
	ServiceType     string // multiple | single
	ServiceCategory string // regular | alacarte
	MinKg, MaxKg    int32
	Discount        float64
	DiscountType    string // percentage | fixed | ""
	Icon            string
	Image           string
}

const (
	ServiceCategoryRegular  = "regular"
	ServiceCategoryAlacarte = "alacarte"
)

type ServiceTierPricing struct {
	ID, ServiceID, TierID int64
	SKU                   string
	EstimatedTime         int32
	EstimatedTimeUnit     string
	Price                 int64
	Discount              float64
	DiscountType          string
	Unit                  string
}

type ServiceTier struct {
	ID        int64
	Code      string
	Name      string
	SortOrder int32
	Pricing   *ServiceTierPricing
}

type ServiceAddon struct {
	ID           int64
	Name         string
	Description  string
	Price        int64
	Unit         string
	Discount     float64
	DiscountType string
	Icon         string
}

type ServiceSection struct {
	Title       string
	Description string
	Type        string // multiple | single
	Addons      []ServiceAddon
}

// ServiceDetail is a service with its tiers and add-on sections.
type ServiceDetail struct {
	ServiceView
	Tiers    []ServiceTier
	Sections []ServiceSection
}

// CoverageArea is a region the service operates in, such as "Jakarta Barat".
type CoverageArea struct {
	ID   int64
	Name string
}
