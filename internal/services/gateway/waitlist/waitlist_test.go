package waitlist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type fake struct{ got []string }

func (f *fake) Add(_ context.Context, email, lang, _ string) error {
	f.got = append(f.got, email+"|"+lang)
	return nil
}
func (f *fake) List(context.Context, int, int) ([]Entry, int, error) { return nil, 0, nil }

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"Ana@Example.COM": "ana@example.com", "  a@b.co ": "a@b.co"} {
		if got, ok := Normalize(in); !ok || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "nope", "a@b", "Ana <a@b.co>", "a@b.co, c@d.co", strings.Repeat("a", 250) + "@b.co"} {
		if _, ok := Normalize(in); ok {
			t.Errorf("Normalize(%q) accepted", in)
		}
	}
}

func TestCreate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := &fake{}
	r := gin.New()
	r.POST("/w", (&Handler{Repo: f}).Create)
	post := func(body string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/w", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w.Code
	}
	if c := post(`{"email":"Ana@Example.com","language":"en"}`); c != http.StatusCreated {
		t.Fatalf("valid: %d", c)
	}
	if len(f.got) != 1 || f.got[0] != "ana@example.com|en" {
		t.Fatalf("saved %v", f.got)
	}
	if c := post(`{"email":"not-an-email"}`); c == http.StatusCreated || len(f.got) != 1 {
		t.Fatalf("invalid accepted: %d %v", c, f.got)
	}
}
