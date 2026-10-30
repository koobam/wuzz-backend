package grpc

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
)

func kindOf(err error) apperr.Kind {
	if ae, ok := err.(*apperr.Error); ok {
		return ae.Kind
	}
	return ""
}

func withKey(key string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(InternalKeyHeader, key))
}

func TestApplyOrderStockAnswersOnlyToTheInternalKey(t *testing.T) {
	req := &catalogv1.ApplyOrderStockRequest{OrderId: "o", OutletId: 1, Action: "explode"}
	cases := map[string]struct {
		server string
		ctx    context.Context
		want   apperr.Kind
	}{
		"no key sent":                  {"secret", context.Background(), apperr.KindForbidden},
		"the wrong key":                {"secret", withKey("guess"), apperr.KindForbidden},
		"a longer key with the prefix": {"secret", withKey("secret!"), apperr.KindForbidden},
		"no key configured":            {"", withKey(""), apperr.KindForbidden},
		"an admin without the key":     {"secret", authn.WithIdentity(context.Background(), authn.Identity{UserID: "a:1", Roles: []string{"admin"}}), apperr.KindForbidden},
		"the right key, a bad action":  {"secret", withKey("secret"), apperr.KindInvalid},
	}
	for name, c := range cases {
		s := &Server{internalKey: c.server}
		if _, err := s.ApplyOrderStock(c.ctx, req); kindOf(err) != c.want {
			t.Errorf("%s: %v; want %s", name, err, c.want)
		}
	}
	// The order and the outlet are required.
	s := &Server{internalKey: "secret"}
	if _, err := s.ApplyOrderStock(withKey("secret"), &catalogv1.ApplyOrderStockRequest{Action: "consume"}); kindOf(err) != apperr.KindInvalid {
		t.Errorf("no order: %v", err)
	}
}

func TestOutletAccess(t *testing.T) {
	ctx := func(roles ...string) context.Context {
		return authn.WithIdentity(context.Background(), authn.Identity{UserID: "u", Roles: roles})
	}
	for name, c := range map[string]struct {
		ctx    context.Context
		outlet int64
		ok     bool
	}{
		"an admin, any outlet":          {ctx("admin"), 9, true},
		"an admin, every outlet":        {ctx("admin"), 0, true},
		"front staff at their outlet":   {ctx("outlet:3"), 3, true},
		"front staff at another":        {ctx("outlet:3"), 4, false},
		"front staff asking for all":    {ctx("outlet:3"), 0, false},
		"a wuzzer":                      {ctx("wuzzer"), 3, false},
		"nobody signed in":              {context.Background(), 3, false},
		"an admin assigned to outlet 1": {ctx("admin", "outlet:1"), 1, true},
	} {
		_, err := outletAccess(c.ctx, c.outlet)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", name, err, c.ok)
		}
	}
	if got := actor(authn.Identity{UserID: "s:7", Name: " Kasir Pusat "}); got != "Kasir Pusat (s:7)" {
		t.Errorf("actor = %q", got)
	}
	if got := actor(authn.Identity{UserID: "s:7"}); got != "s:7" {
		t.Errorf("actor without a name = %q", got)
	}
}
