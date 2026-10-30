package backoffice

import (
	"strconv"

	"github.com/gin-gonic/gin"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

// Accounts lists the back-office admins and the staff: the two kinds of account that are not customers.
type Accounts struct{ User userv1.UserServiceClient }

type accountDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	Role      string `json:"role"`
	OutletID  int64  `json:"outlet_id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// list answers GET /admins and GET /staff; kind is "admin" or "staff".
func (h *Accounts) list(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		outlet, ok := outletFilter(c)
		if !ok {
			return
		}
		res, err := h.User.ListAccounts(c.Request.Context(), &userv1.ListAccountsRequest{
			Kind:     kind,
			Query:    c.Query("query"),
			Status:   c.Query("status"),
			Limit:    int32(httpx.IntQuery(c, "limit", 25)),
			Offset:   int32(httpx.IntQuery(c, "offset", 0)),
			OutletId: outlet, SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"),
		})
		if err != nil {
			httpx.Fail(c, httpx.Upstream(err))
			return
		}
		items := make([]accountDTO, 0, len(res.GetAccounts()))
		for _, a := range res.GetAccounts() {
			items = append(items, accountDTO{
				ID: a.GetId(), Name: a.GetName(), Phone: a.GetPhone(), Role: a.GetRole(),
				OutletID: a.GetOutletId(), Status: a.GetStatus(),
				CreatedAt: a.GetCreatedAt().AsTime().Format("2006-01-02T15:04:05.000Z"),
			})
		}
		httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
	}
}

func (h *Accounts) ListAdmins() gin.HandlerFunc { return h.list("admin") }
func (h *Accounts) ListStaff() gin.HandlerFunc  { return h.list("staff") }

// outletFilter reads ?outlet_id=: absent, empty or 0 is no filter; anything that is not a whole number, or is negative, is a 400.
func outletFilter(c *gin.Context) (int64, bool) {
	raw := c.Query("outlet_id")
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		httpx.Fail(c, apperr.Invalid("outlet_id must be a number"))
		return 0, false
	}
	if n < 0 {
		httpx.Fail(c, apperr.Invalid("outlet_id must not be negative"))
		return 0, false
	}
	return n, true
}
