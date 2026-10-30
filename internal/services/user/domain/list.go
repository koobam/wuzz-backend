package domain

// ListFilter narrows, orders and pages a listing of users or of accounts.
type ListFilter struct {
	Query, Status string
	// OutletID keeps only the accounts tied to that outlet; 0 = no filter. Admins and staff only.
	OutletID int64
	// SortBy is a column from the whitelist of the listing (checked by the handler, mapped to SQL by the store); SortDir is asc or desc.
	SortBy, SortDir string
	Limit, Offset   int
}
