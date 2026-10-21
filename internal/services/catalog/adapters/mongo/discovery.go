// Package mongo adapts catalog's Discovery port to MongoDB. Outlets live in one
// collection with a 2dsphere index on `location`, so "nearest first" is an
// index-backed $geoNear, and name/address search is a case-insensitive match
// inside the same pipeline.
package mongo

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

const (
	collectionName         = "outlets"
	servicesCollectionName = "services"
	areasCollectionName    = "coverage_areas"
)

type Discovery struct {
	client   *mongo.Client
	coll     *mongo.Collection
	services *mongo.Collection
	areas    *mongo.Collection
}

// outletDoc is the stored shape. `_id` is the outlet's numeric id, the same one
// the rest of the system uses for it.
type outletDoc struct {
	ID       int64  `bson:"_id"`
	Name     string `bson:"name"`
	Address  string `bson:"address"`
	Category string `bson:"category"`
	Location struct {
		Type        string    `bson:"type"`
		Coordinates []float64 `bson:"coordinates"` // [longitude, latitude]
	} `bson:"location"`
	Images        []string `bson:"images"`
	CurrentStatus string   `bson:"current_status"`
	DeliveryTypes []string `bson:"delivery_types"`
	OpenTime      string   `bson:"open_time"`
	CloseTime     string   `bson:"close_time"`
	Active        bool     `bson:"active"`
	// Set by $geoNear, in metres.
	DistanceM *float64 `bson:"distance_m"`
}

// New connects and makes sure the indexes exist (idempotent).
func New(ctx context.Context, uri, database string) (*Discovery, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, apperr.Internal("Could not connect to MongoDB").WithCause(err)
	}
	coll := client.Database(database).Collection(collectionName)
	_, err = coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "location", Value: "2dsphere"}}},
		{Keys: bson.D{{Key: "active", Value: 1}, {Key: "name", Value: 1}}},
	})
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, apperr.Internal("Could not prepare the outlet indexes").WithCause(err)
	}
	services := client.Database(database).Collection(servicesCollectionName)
	_, err = services.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "active", Value: 1}, {Key: "service_category", Value: 1}, {Key: "sort_order", Value: 1}},
	})
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, apperr.Internal("Could not prepare the service indexes").WithCause(err)
	}
	areas := client.Database(database).Collection(areasCollectionName)
	if _, err = areas.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "geometry", Value: "2dsphere"}}}); err != nil {
		_ = client.Disconnect(ctx)
		return nil, apperr.Internal("Could not prepare the coverage indexes").WithCause(err)
	}
	return &Discovery{client: client, coll: coll, services: services, areas: areas}, nil
}

func (d *Discovery) Ping(ctx context.Context) error { return d.client.Ping(ctx, nil) }

func (d *Discovery) Close(ctx context.Context) error { return d.client.Disconnect(ctx) }

func (d *Discovery) Nearby(ctx context.Context, q domain.NearbyQuery) ([]domain.OutletView, string, error) {
	offset := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil || n < 0 {
			return nil, "", apperr.Invalid("The cursor is invalid")
		}
		offset = n
	}

	match := bson.D{{Key: "active", Value: true}}
	if q.Text != "" {
		re := bson.D{{Key: "$regex", Value: regexp.QuoteMeta(q.Text)}, {Key: "$options", Value: "i"}}
		match = append(match, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "name", Value: re}},
			bson.D{{Key: "address", Value: re}},
		}})
	}

	var pipeline mongo.Pipeline
	if q.Near != nil {
		pipeline = append(pipeline, bson.D{{Key: "$geoNear", Value: bson.D{
			{Key: "near", Value: point(*q.Near)},
			{Key: "distanceField", Value: "distance_m"},
			{Key: "spherical", Value: true},
			{Key: "query", Value: match},
		}}})
	} else {
		pipeline = append(pipeline,
			bson.D{{Key: "$match", Value: match}},
			bson.D{{Key: "$sort", Value: bson.D{{Key: "name", Value: 1}}}})
	}
	// One extra row tells us whether another page exists.
	pipeline = append(pipeline,
		bson.D{{Key: "$skip", Value: int64(offset)}},
		bson.D{{Key: "$limit", Value: int64(q.Limit + 1)}})

	cur, err := d.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, "", apperr.Internal("Could not search outlets").WithCause(err)
	}
	var docs []outletDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, "", apperr.Internal("Could not read outlets").WithCause(err)
	}

	next := ""
	if len(docs) > q.Limit {
		docs = docs[:q.Limit]
		next = strconv.Itoa(offset + q.Limit)
	}
	out := make([]domain.OutletView, 0, len(docs))
	for _, doc := range docs {
		out = append(out, doc.view(q.Near))
	}
	return out, next, nil
}

// Coverage finds the active service area whose polygon contains p.
func (d *Discovery) Coverage(ctx context.Context, p domain.Point) (domain.CoverageArea, bool, error) {
	var doc struct {
		ID   int64  `bson:"_id"`
		Name string `bson:"name"`
	}
	err := d.areas.FindOne(ctx, bson.D{
		{Key: "active", Value: true},
		{Key: "geometry", Value: bson.D{{Key: "$geoIntersects", Value: bson.D{{Key: "$geometry", Value: point(p)}}}}},
	}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.CoverageArea{}, false, nil
	}
	if err != nil {
		return domain.CoverageArea{}, false, apperr.Internal("Could not resolve the service area").WithCause(err)
	}
	return domain.CoverageArea{ID: doc.ID, Name: doc.Name}, true, nil
}

func (d *Discovery) ByID(ctx context.Context, id int64, near *domain.Point) (domain.OutletView, error) {
	var doc outletDoc
	err := d.coll.FindOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "active", Value: true}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.OutletView{}, apperr.NotFound("Outlet not found")
	}
	if err != nil {
		return domain.OutletView{}, apperr.Internal("Could not load the outlet").WithCause(err)
	}
	return doc.view(near), nil
}

func point(p domain.Point) bson.D {
	return bson.D{
		{Key: "type", Value: "Point"},
		{Key: "coordinates", Value: bson.A{p.Longitude, p.Latitude}},
	}
}

func (o outletDoc) view(near *domain.Point) domain.OutletView {
	v := domain.OutletView{
		ID: o.ID, Name: o.Name, Address: o.Address, Category: o.Category,
		Images: o.Images, CurrentStatus: o.CurrentStatus, DeliveryTypes: o.DeliveryTypes,
		OpenTime: o.OpenTime, CloseTime: o.CloseTime,
	}
	if len(o.Location.Coordinates) == 2 {
		v.Longitude, v.Latitude = o.Location.Coordinates[0], o.Location.Coordinates[1]
	}
	switch {
	case o.DistanceM != nil:
		v.HasDistance, v.DistanceKm = true, round2(*o.DistanceM/1000)
	case near != nil:
		// Not from $geoNear (e.g. a pinned outlet fetched by id): work it out.
		v.HasDistance = true
		v.DistanceKm = round2(haversineKm(*near, domain.Point{Latitude: v.Latitude, Longitude: v.Longitude}))
	}
	return v
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

func haversineKm(a, b domain.Point) float64 {
	const earthKm = 6371.0088
	rad := math.Pi / 180
	dLat := (b.Latitude - a.Latitude) * rad
	dLng := (b.Longitude - a.Longitude) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a.Latitude*rad)*math.Cos(b.Latitude*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthKm * math.Asin(math.Sqrt(h))
}

type serviceDoc struct {
	ID              int64        `bson:"_id"`
	Name            string       `bson:"name"`
	Description     string       `bson:"description"`
	Price           int64        `bson:"price"`
	Unit            string       `bson:"unit"`
	ServiceType     string       `bson:"service_type"`
	ServiceCategory string       `bson:"service_category"`
	MinKg           int32        `bson:"min_kg"`
	MaxKg           int32        `bson:"max_kg"`
	Discount        float64      `bson:"discount"`
	DiscountType    string       `bson:"discount_type"`
	Icon            string       `bson:"icon"`
	Image           string       `bson:"image"`
	Tiers           []tierDoc    `bson:"tiers"`
	Sections        []sectionDoc `bson:"sections"`
}

type pricingDoc struct {
	ID                int64   `bson:"id"`
	ServiceID         int64   `bson:"service_id"`
	TierID            int64   `bson:"tier_id"`
	SKU               string  `bson:"sku"`
	EstimatedTime     int32   `bson:"estimated_time"`
	EstimatedTimeUnit string  `bson:"estimated_time_unit"`
	Price             int64   `bson:"price"`
	Discount          float64 `bson:"discount"`
	DiscountType      string  `bson:"discount_type"`
	Unit              string  `bson:"unit"`
}

type tierDoc struct {
	ID        int64       `bson:"id"`
	Code      string      `bson:"code"`
	Name      string      `bson:"name"`
	SortOrder int32       `bson:"sort_order"`
	Pricing   *pricingDoc `bson:"pricing"`
}

type addonDoc struct {
	ID           int64   `bson:"id"`
	Name         string  `bson:"name"`
	Description  string  `bson:"description"`
	Price        int64   `bson:"price"`
	Unit         string  `bson:"unit"`
	Discount     float64 `bson:"discount"`
	DiscountType string  `bson:"discount_type"`
	Icon         string  `bson:"icon"`
}

type sectionDoc struct {
	Title       string     `bson:"title"`
	Description string     `bson:"description"`
	Type        string     `bson:"type"`
	Addons      []addonDoc `bson:"addons"`
}

func (d *Discovery) Services(ctx context.Context, category string, limit int) ([]domain.ServiceView, error) {
	filter := bson.D{{Key: "active", Value: true}}
	if category != "" {
		filter = append(filter, bson.E{Key: "service_category", Value: category})
	}
	opts := options.Find().SetSort(bson.D{{Key: "sort_order", Value: 1}, {Key: "_id", Value: 1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cur, err := d.services.Find(ctx, filter, opts)
	if err != nil {
		return nil, apperr.Internal("Could not list services").WithCause(err)
	}
	var docs []serviceDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, apperr.Internal("Could not read services").WithCause(err)
	}
	out := make([]domain.ServiceView, 0, len(docs))
	for _, s := range docs {
		out = append(out, s.view())
	}
	return out, nil
}

func (s serviceDoc) view() domain.ServiceView {
	return domain.ServiceView{
		ID: s.ID, Name: s.Name, Description: s.Description, Price: s.Price, Unit: s.Unit,
		ServiceType: s.ServiceType, ServiceCategory: s.ServiceCategory, MinKg: s.MinKg, MaxKg: s.MaxKg,
		Discount: s.Discount, DiscountType: s.DiscountType, Icon: s.Icon, Image: s.Image,
	}
}

func (d *Discovery) ServiceDetail(ctx context.Context, id int64) (domain.ServiceDetail, error) {
	var doc serviceDoc
	err := d.services.FindOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "active", Value: true}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.ServiceDetail{}, apperr.NotFound("Service not found")
	}
	if err != nil {
		return domain.ServiceDetail{}, apperr.Internal("Could not load the service").WithCause(err)
	}
	detail := domain.ServiceDetail{ServiceView: doc.view()}
	for _, t := range doc.Tiers {
		tier := domain.ServiceTier{ID: t.ID, Code: t.Code, Name: t.Name, SortOrder: t.SortOrder}
		if p := t.Pricing; p != nil {
			tier.Pricing = &domain.ServiceTierPricing{
				ID: p.ID, ServiceID: p.ServiceID, TierID: p.TierID, SKU: p.SKU,
				EstimatedTime: p.EstimatedTime, EstimatedTimeUnit: p.EstimatedTimeUnit,
				Price: p.Price, Discount: p.Discount, DiscountType: p.DiscountType, Unit: p.Unit,
			}
		}
		detail.Tiers = append(detail.Tiers, tier)
	}
	for _, sec := range doc.Sections {
		section := domain.ServiceSection{Title: sec.Title, Description: sec.Description, Type: sec.Type}
		for _, a := range sec.Addons {
			section.Addons = append(section.Addons, domain.ServiceAddon{
				ID: a.ID, Name: a.Name, Description: a.Description, Price: a.Price, Unit: a.Unit,
				Discount: a.Discount, DiscountType: a.DiscountType, Icon: a.Icon,
			})
		}
		detail.Sections = append(detail.Sections, section)
	}
	return detail, nil
}
