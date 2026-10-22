package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/services/gateway/clients"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/docs"
)

// fakeUser answers the one call /auth/me makes, so the test can tell "let in" from "refused".
type fakeUser struct{ userv1.UserServiceClient }

func (fakeUser) GetUser(context.Context, *userv1.GetUserRequest, ...grpc.CallOption) (*userv1.GetUserResponse, error) {
	return &userv1.GetUserResponse{User: &userv1.User{Id: "1", Name: "Test"}}, nil
}

// TestAudiences checks who each staff audience lets in, with real signed tokens and the real
// router: a token for one audience must not open another.
func TestAudiences(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Env: "dev", Auth: config.Auth{Issuer: "wuzz-backend", AccessTokenTTL: time.Hour}}
	signer, err := authn.NewSigner(cfg.Auth)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := authn.NewVerifier(cfg.Auth)
	if err != nil {
		t.Fatal(err)
	}

	// ErrorHandler is what turns the error a handler attaches into its HTTP status; the
	// real server installs it for every route.
	engine := gin.New()
	engine.Use(httpx.ErrorHandler())
	Register(engine, &clients.Clients{User: fakeUser{}}, verifier, nil, cfg)

	token := func(roles ...string) string {
		tok, err := signer.Issue(authn.Identity{UserID: "u1", Name: "T", Roles: roles})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	get := func(path, tok string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec.Code
	}

	cases := []struct {
		name  string
		path  string
		token string
		want  int
	}{
		{"station: no token", "/station/v1/auth/me", "", http.StatusUnauthorized},
		{"station: a customer is refused", "/station/v1/auth/me", token(), http.StatusForbidden},
		{"station: outlet staff is refused", "/station/v1/auth/me", token("outlet:3"), http.StatusForbidden},
		{"station: a wuzzer gets in", "/station/v1/auth/me", token("wuzzer"), http.StatusOK},
		{"station: an admin gets in", "/station/v1/auth/me", token("admin"), http.StatusOK},

		{"pos: a wuzzer is refused", "/pos/v1/auth/me", token("wuzzer"), http.StatusForbidden},
		{"pos: outlet staff gets in", "/pos/v1/auth/me", token("outlet:3"), http.StatusOK},
		{"backoffice: a wuzzer is refused", "/backoffice/v1/auth/me", token("wuzzer"), http.StatusForbidden},
		{"backoffice: outlet staff is refused", "/backoffice/v1/auth/me", token("outlet:3"), http.StatusForbidden},

		{"pos orders: a wuzzer is refused", "/pos/v1/orders", token("wuzzer"), http.StatusForbidden},
		{"backoffice users: a wuzzer is refused", "/backoffice/v1/users", token("wuzzer"), http.StatusForbidden},
	}
	for _, c := range cases {
		if got := get(c.path, c.token); got != c.want {
			t.Errorf("%s: GET %s = %d, want %d", c.name, c.path, got, c.want)
		}
	}
}

// TestSpecMatchesRoutes keeps the hand-written OpenAPI file honest: every versioned route the
// gateway serves must be documented, and nothing documented may be missing from the router.
func TestSpecMatchesRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Env: "dev", Auth: config.Auth{Issuer: "wuzz-backend", AccessTokenTTL: time.Hour}}
	verifier, err := authn.NewVerifier(cfg.Auth)
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	Register(engine, &clients.Clients{User: fakeUser{}}, verifier, nil, cfg)

	spec, err := docs.Operations()
	if err != nil {
		t.Fatal(err)
	}
	param := regexp.MustCompile(`:([A-Za-z_]+)`)
	routed := map[string]bool{}
	for _, r := range engine.Routes() {
		versioned := false
		for _, prefix := range []string{"/app/v1/", "/backoffice/v1/", "/pos/v1/", "/station/v1/"} {
			versioned = versioned || strings.HasPrefix(r.Path, prefix)
		}
		// /app/v1/dev/login exists only in dev; it is deliberately not documented.
		if !versioned || r.Path == "/app/v1/dev/login" {
			continue
		}
		routed[r.Method+" "+param.ReplaceAllString(r.Path, "{$1}")] = true
	}
	for op := range routed {
		if !spec[op] {
			t.Errorf("route %s is not in openapi.yaml", op)
		}
	}
	for op := range spec {
		if !routed[op] {
			t.Errorf("openapi.yaml documents %s, which the router does not serve", op)
		}
	}
}
