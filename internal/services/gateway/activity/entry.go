// Package activity is the gateway's activity log: who did what, through the back office or the POS.
//
// A middleware watches every state-changing route (rules.go), and after the response has gone out hands one Entry to the
// Recorder, which writes it to the gateway's own Postgres table off the request's path. The table is append-only. Reading it back
// is GET /backoffice/v1/activity (handler.go).
package activity

import (
	"encoding/json"
	"time"
)

// What was done. The apps translate "activity.<entity_type>.<action>" with the summary params into a sentence.
const (
	ActionCreate   = "create"
	ActionUpdate   = "update"
	ActionDelete   = "delete"
	ActionStatus   = "status"
	ActionReceive  = "receive"
	ActionWaste    = "waste"
	ActionAdjust   = "adjust"
	ActionTransfer = "transfer"
	ActionSetMin   = "set_min"
	ActionReorder  = "reorder"
	ActionInject   = "inject"
	ActionSignIn   = "sign_in"
	ActionSignOut  = "sign_out"
)

// What it was done to.
const (
	EntityOutlet        = "outlet"
	EntityService       = "service"
	EntityVariantGroup  = "variant_group"
	EntityModifierGroup = "modifier_group"
	EntityMaterial      = "material"
	EntityBanner        = "banner"
	EntityVoucher       = "voucher"
	EntityBom           = "bom"
	EntityStock         = "stock"
	EntityOrder         = "order"
	EntityUser          = "user"
	EntityMedia         = "media"
	EntitySession       = "session"
)

const (
	OutcomeOK     = "ok"
	OutcomeFailed = "failed"
)

type Actor struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

// Entry is one row of the log.
type Entry struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Audience   string    `json:"audience"`
	Actor      Actor     `json:"actor"`
	Action     string    `json:"action"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Summary    Summary   `json:"summary"`
	// The request body with secrets removed; null when there was nothing worth keeping.
	Changes   json.RawMessage `json:"changes"`
	Outcome   string          `json:"outcome"`
	Status    int             `json:"status"`
	RequestID string          `json:"request_id"`
	IP        string          `json:"ip"`
}

type Summary struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params"`
}
