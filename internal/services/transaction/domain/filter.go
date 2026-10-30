package domain

// SortFields are the columns a listing may be ordered by; the store maps each to its own SQL, so nothing a caller
// sends ever reaches a query.
var SortFields = map[string]bool{"code": true, "created_at": true, "total": true, "status": true, "customer_name": true, "outlet_id": true, "id": true}

// ListFilter is what a listing or a summary is about. A list and a summary built from the same filter always mean the same orders.
type ListFilter struct {
	// OutletIDs limits the listing; nil means every outlet (admin only).
	OutletIDs []int64
	Status    string
	Query     string
	// From and To are days (YYYY-MM-DD, Asia/Jakarta) bounding when the order was created; either may be empty.
	From, To      string
	SortBy        string // one of SortFields
	Desc          bool
	Limit, Offset int
}
