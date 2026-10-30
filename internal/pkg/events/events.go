// Package events defines the envelope every domain event travels in.
//
// One envelope for all services means a projection can route on Type and
// deduplicate on ID without knowing which service produced the event.
package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Envelope wraps a domain event with the metadata the read side needs.
type Envelope struct {
	// ID is the deduplication key. At-least-once delivery means a consumer
	// will see the same event twice; it must recognise it.
	ID            string `json:"id"`
	Type          string `json:"type"`
	AggregateID   string `json:"aggregateId"`
	AggregateType string `json:"aggregateType"`
	// Version is the aggregate's version after this event. A consumer applies
	// an event only if it is newer than what it already holds, which is what
	// makes out-of-order redelivery safe.
	Version    int64           `json:"version"`
	OccurredAt time.Time       `json:"occurredAt"`
	Payload    json.RawMessage `json:"payload"`
}

// Event types. Keep them stable: they are the contract between the command
// services and every consumer, and renaming one silently stops a consumer.
const (
	TypeUserRegistered = "user.registered"

	TypeOutletCreated = "outlet.created"
	TypeOutletUpdated = "outlet.updated"
	TypeOutletDeleted = "outlet.deleted"

	TypeServiceCreated = "service.created"
	TypeServiceUpdated = "service.updated"
	TypeServiceDeleted = "service.deleted"

	TypeOrderCreated       = "order.created"
	TypeOrderStatusChanged = "order.status_changed"
)

// Aggregate types, used as the partition-key namespace and the topic name.
const (
	AggregateUser    = "user"
	AggregateOutlet  = "outlet"
	AggregateService = "service"
	AggregateOrder   = "order"
)

func New(eventType, aggregateType, aggregateID string, version int64, payload any) (Envelope, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("events: marshal %s: %w", eventType, err)
	}
	return Envelope{
		ID:            uuid.NewString(),
		Type:          eventType,
		AggregateID:   aggregateID,
		AggregateType: aggregateType,
		Version:       version,
		OccurredAt:    time.Now().UTC(),
		Payload:       encoded,
	}, nil
}

// Decode unmarshals the payload into T.
func Decode[T any](e Envelope) (T, error) {
	var out T
	if err := json.Unmarshal(e.Payload, &out); err != nil {
		return out, fmt.Errorf("events: decode payload of %s: %w", e.Type, err)
	}
	return out, nil
}

// Topic maps an aggregate to its Kafka topic. One topic per aggregate, not per
// event type: ordering is only guaranteed within a partition, and every event
// about one aggregate has to stay in order relative to the others.
func Topic(aggregateType string) string {
	return aggregateType + ".events"
}

// UserRegistered is the payload of TypeUserRegistered. Deliberately small and
// free of anything sensitive: consumers that need more ask the user service.
type UserRegistered struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// OutletChanged is the full snapshot carried by outlet.created and
// outlet.updated, so a consumer rebuilding from the start of the topic needs no
// other source.
type OutletChanged struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Address   string  `json:"address"`
	Phone     string  `json:"phone"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Active    bool    `json:"active"`
}

// OutletDeleted and ServiceDeleted are the payloads of outlet.deleted and service.deleted: only the id, because the row is gone.
type OutletDeleted struct {
	ID int64 `json:"id"`
}

type ServiceDeleted struct {
	ID int64 `json:"id"`
}

// ServiceChanged is the snapshot carried by service.created / service.updated.
type ServiceChanged struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Unit   string `json:"unit"`
	Price  int64  `json:"price"`
	Active bool   `json:"active"`
}

// OrderLine and OrderChanged are the snapshot carried by order.created and
// order.status_changed.
type OrderLine struct {
	ItemID int64   `json:"itemId"`
	Name   string  `json:"name"`
	Unit   string  `json:"unit"`
	Price  int64   `json:"price"`
	Qty    float64 `json:"qty"`
}

type OrderChanged struct {
	ID            string      `json:"id"`
	Code          string      `json:"code,omitempty"`
	OutletID      int64       `json:"outletId"`
	CustomerName  string      `json:"customerName"`
	CustomerPhone string      `json:"customerPhone"`
	Lines         []OrderLine `json:"lines"`
	Total         int64       `json:"total"`
	Status        string      `json:"status"`
	CreatedBy     string      `json:"createdBy"`
}
