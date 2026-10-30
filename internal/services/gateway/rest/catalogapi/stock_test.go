package catalogapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/orders"
)

func router(roles ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(httpx.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(authn.WithIdentity(c.Request.Context(), authn.Identity{UserID: "u", Roles: roles}))
	})
	r.Use(orders.PinOutlet())
	return r
}

// The POS is about the account's own outlet whatever the request says; the back office is about the one it asks for.
func TestPOSStockIsPinnedToTheAccountsOutlet(t *testing.T) {
	var got int64
	probe := func(sc scope) gin.HandlerFunc {
		return func(c *gin.Context) { got = sc(c, 99); c.Status(http.StatusNoContent) }
	}
	for name, c := range map[string]struct {
		roles []string
		sc    scope
		want  int64
	}{
		"front staff, the request is overridden":     {[]string{"outlet:3"}, ownOutlet, 3},
		"an admin with an outlet, likewise":          {[]string{"admin", "outlet:1"}, ownOutlet, 1},
		"an account with two outlets keeps the ask":  {[]string{"outlet:1", "outlet:2"}, ownOutlet, 99},
		"the back office takes what it is asked":     {[]string{"admin"}, anyOutlet, 99},
		"even an assigned admin, in the back office": {[]string{"admin", "outlet:1"}, anyOutlet, 99},
	} {
		got = 0
		r := router(c.roles...)
		r.GET("/x", probe(c.sc))
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
		if got != c.want {
			t.Errorf("%s: outlet %d; want %d", name, got, c.want)
		}
	}
}

func TestHeadOfficeOnlyAnswers403(t *testing.T) {
	r := router("outlet:3")
	r.POST("/stock/adjustments", HeadOfficeOnly)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/stock/adjustments", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", rec.Code)
	}
}
