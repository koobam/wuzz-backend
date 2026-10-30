package catalogapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

func decode[T any](body string) (T, error) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	v, ok := bindJSON[T](c)
	if ok {
		return v, nil
	}
	if len(c.Errors) > 0 {
		return v, c.Errors[0].Err
	}
	return v, nil
}

func TestDecimalsMustBeJSONStrings(t *testing.T) {
	got, err := decode[calcBody](`{"service_id": 1, "qty": "2.5", "variant_option_ids": [1]}`)
	if err != nil || got.Qty != "2.5" || got.ServiceID != 1 {
		t.Fatalf("a string is fine: %+v, %v", got, err)
	}
	for _, body := range []string{`{"service_id":1,"qty":2.5}`, `{"service_id":1,"qty":1e3}`, `{"service_id":1,"qty":3}`, `{"service_id":1,"qty":true}`} {
		_, err := decode[calcBody](body)
		ae, ok := err.(*apperr.Error)
		if !ok || ae.Kind != apperr.KindInvalid || !strings.Contains(ae.Message, "must be sent as strings") {
			t.Errorf("%s: %v; want a clear invalid error", body, err)
		}
	}
	// Money is a whole number: a fraction is refused and the field is named.
	_, err = decode[materialBody](`{"sku":"A","purchase_price":1.5,"purchase_qty":"1"}`)
	if ae, ok := err.(*apperr.Error); !ok || !strings.Contains(ae.Message, "purchase_price") {
		t.Errorf("fractional price: %v", err)
	}
	// null and a missing quantity both come through as empty, which the domain then refuses.
	if got, err := decode[calcBody](`{"service_id":1,"qty":null}`); err != nil || got.Qty != "" {
		t.Errorf("null: %+v, %v", got, err)
	}
}
