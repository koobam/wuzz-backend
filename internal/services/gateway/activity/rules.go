package activity

// rule says what a state-changing route is, for the log. The table below is the whole list of what gets logged: a route that is not
// in it (a read, a presence ping, a costing calculation, a token refresh) leaves nothing.
type rule struct {
	entity string
	action string
	// The entity's id: the path parameters, joined with ":" (a BOM is "service:12"), else this key of the answer (a create) or of the request.
	idParams []string
	idKey    string
	// What the sentence is about: these keys of the answer, else of the request, as summary params.
	params []string
	// The request body is not kept (sign-in carries a phone number and a code).
	noBody bool
}

// Keyed by "METHOD /path" as the route is registered under /backoffice/v1 or /pos/v1.
var rules = map[string]rule{
	// An uploaded picture: the entity is its key; the bytes are never kept (noBody), only what the answer says about the file.
	"POST /uploads": {entity: EntityMedia, action: ActionCreate, idKey: "key", params: []string{"file_name", "content_type", "size"}, noBody: true},

	"POST /outlets":       {entity: EntityOutlet, action: ActionCreate, idKey: "id", params: []string{"name"}},
	"PATCH /outlets/:id":  {entity: EntityOutlet, action: ActionUpdate, idParams: []string{"id"}, params: []string{"name"}},
	"DELETE /outlets/:id": {entity: EntityOutlet, action: ActionDelete, idParams: []string{"id"}},

	"POST /services":       {entity: EntityService, action: ActionCreate, idKey: "id", params: []string{"name"}},
	"PATCH /services/:id":  {entity: EntityService, action: ActionUpdate, idParams: []string{"id"}, params: []string{"name"}},
	"DELETE /services/:id": {entity: EntityService, action: ActionDelete, idParams: []string{"id"}},

	"POST /variant-groups":       {entity: EntityVariantGroup, action: ActionCreate, idKey: "id", params: []string{"name"}},
	"PUT /variant-groups/:id":    {entity: EntityVariantGroup, action: ActionUpdate, idParams: []string{"id"}, params: []string{"name"}},
	"DELETE /variant-groups/:id": {entity: EntityVariantGroup, action: ActionDelete, idParams: []string{"id"}},

	"POST /modifier-groups":       {entity: EntityModifierGroup, action: ActionCreate, idKey: "id", params: []string{"name"}},
	"PUT /modifier-groups/:id":    {entity: EntityModifierGroup, action: ActionUpdate, idParams: []string{"id"}, params: []string{"name"}},
	"DELETE /modifier-groups/:id": {entity: EntityModifierGroup, action: ActionDelete, idParams: []string{"id"}},

	"POST /materials":       {entity: EntityMaterial, action: ActionCreate, idKey: "id", params: []string{"name", "sku"}},
	"PUT /materials/:id":    {entity: EntityMaterial, action: ActionUpdate, idParams: []string{"id"}, params: []string{"name", "sku"}},
	"DELETE /materials/:id": {entity: EntityMaterial, action: ActionDelete, idParams: []string{"id"}},

	"POST /banners":         {entity: EntityBanner, action: ActionCreate, idKey: "id", params: []string{"title"}},
	"PUT /banners/:id":      {entity: EntityBanner, action: ActionUpdate, idParams: []string{"id"}, params: []string{"title"}},
	"PATCH /banners/:id":    {entity: EntityBanner, action: ActionStatus, idParams: []string{"id"}, params: []string{"title", "status"}},
	"DELETE /banners/:id":   {entity: EntityBanner, action: ActionDelete, idParams: []string{"id"}, params: []string{"title"}},
	"POST /banners/reorder": {entity: EntityBanner, action: ActionReorder, params: []string{"total"}},

	// An injection names customers by id and phone number: the log keeps how many got the voucher, not who (voucher_injections has the audit).
	"POST /vouchers":            {entity: EntityVoucher, action: ActionCreate, idKey: "id", params: []string{"title", "code"}},
	"PUT /vouchers/:id":         {entity: EntityVoucher, action: ActionUpdate, idParams: []string{"id"}, params: []string{"title", "code"}},
	"PATCH /vouchers/:id":       {entity: EntityVoucher, action: ActionStatus, idParams: []string{"id"}, params: []string{"title", "status"}},
	"DELETE /vouchers/:id":      {entity: EntityVoucher, action: ActionDelete, idParams: []string{"id"}, params: []string{"title"}},
	"POST /vouchers/:id/inject": {entity: EntityVoucher, action: ActionInject, idParams: []string{"id"}, params: []string{"title", "injected_count", "target_count"}, noBody: true},

	"PUT /boms/:owner_type/:owner_id": {entity: EntityBom, action: ActionUpdate, idParams: []string{"owner_type", "owner_id"}},

	// Stock: the entity is the material; the outlet and the quantity are in the params and in the changes.
	"POST /stock/receipts":    {entity: EntityStock, action: ActionReceive, idKey: "material_id", params: []string{"outlet_id", "qty"}},
	"POST /stock/waste":       {entity: EntityStock, action: ActionWaste, idKey: "material_id", params: []string{"outlet_id", "qty", "reason"}},
	"POST /stock/adjustments": {entity: EntityStock, action: ActionAdjust, idKey: "material_id", params: []string{"outlet_id", "counted_qty"}},
	"POST /stock/transfers":   {entity: EntityStock, action: ActionTransfer, idKey: "material_id", params: []string{"from_outlet_id", "to_outlet_id", "qty"}},
	"PUT /stock/min":          {entity: EntityStock, action: ActionSetMin, idKey: "material_id", params: []string{"outlet_id", "min_qty"}},

	"POST /orders":             {entity: EntityOrder, action: ActionCreate, idKey: "id", params: []string{"code"}},
	"PATCH /orders/:id/status": {entity: EntityOrder, action: ActionStatus, idParams: []string{"id"}, params: []string{"code", "status"}},

	"PATCH /users/:id/status": {entity: EntityUser, action: ActionStatus, idParams: []string{"id"}, params: []string{"status"}},

	"POST /auth/verify-otp": {entity: EntitySession, action: ActionSignIn, noBody: true},
	"POST /auth/logout":     {entity: EntitySession, action: ActionSignOut, noBody: true},
}

// audiences are the route prefixes the log watches, and what each is called in the log.
var audiences = []struct{ prefix, name string }{
	{"/backoffice/v1", "backoffice"},
	{"/pos/v1", "pos"},
}
