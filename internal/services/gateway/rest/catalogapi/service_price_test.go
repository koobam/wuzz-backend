package catalogapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func i64(n int64) *int64 { return &n }

// A price read back from the list is the one AFTER tax; saving it as if it were the one before would add tax on tax.
// So a service with tax must say price_before_tax; `price` is accepted only where the two are the same number.
func TestPriceBeforeTaxOfABody(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    serviceBody
		taxType string
		stored  int64
		want    int64
		refused bool
	}{
		{"before tax, with tax", serviceBody{PriceBeforeTax: i64(10000)}, "ppn", 0, 10000, false},
		{"before tax wins over price", serviceBody{PriceBeforeTax: i64(10000), Price: i64(11100)}, "ppn", 0, 10000, false},
		{"price without tax", serviceBody{Price: i64(7000)}, "none", 0, 7000, false},
		{"price on create, tax left out", serviceBody{Price: i64(7000)}, "", 0, 7000, false},
		{"price with tax is refused", serviceBody{Price: i64(11100)}, "ppn", 0, 0, true},
		{"neither: an update keeps the stored one", serviceBody{}, "ppn", 12345, 12345, false},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPatch, "/", nil)
		got, ok := priceBeforeTax(c, tc.body, tc.taxType, tc.stored)
		if ok == tc.refused {
			t.Errorf("%s: ok=%v, refused want %v", tc.name, ok, tc.refused)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("%s: price = %d, want %d", tc.name, got, tc.want)
		}
	}
}
