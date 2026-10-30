package grpc

import (
	"context"
	"testing"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
)

func as(id authn.Identity) context.Context { return authn.WithIdentity(context.Background(), id) }

// The back office's voucher calls are the admin's alone; none of them reaches the application for anybody else. (The server has no
// application here, so a call that got past the check would panic.)
func TestVoucherAdminCallsNeedAnAdmin(t *testing.T) {
	s := &Server{}
	customer := as(authn.Identity{UserID: "42"})
	staff := as(authn.Identity{UserID: "s:3", Roles: []string{"outlet:1"}})
	for who, ctx := range map[string]context.Context{"nobody": context.Background(), "a customer": customer, "staff": staff} {
		calls := map[string]func() error{
			"list":   func() error { _, err := s.ListVouchers(ctx, &catalogv1.ListVouchersRequest{}); return err },
			"get":    func() error { _, err := s.GetVoucher(ctx, &catalogv1.GetVoucherRequest{Id: 1}); return err },
			"create": func() error { _, err := s.CreateVoucher(ctx, &catalogv1.SaveVoucherRequest{}); return err },
			"update": func() error { _, err := s.UpdateVoucher(ctx, &catalogv1.SaveVoucherRequest{Id: 1}); return err },
			"status": func() error { _, err := s.SetVoucherStatus(ctx, &catalogv1.SetVoucherStatusRequest{Id: 1}); return err },
			"delete": func() error { _, err := s.DeleteVoucher(ctx, &catalogv1.GetVoucherRequest{Id: 1}); return err },
			"colors": func() error { _, err := s.ListVoucherColors(ctx, &catalogv1.ListVoucherColorsRequest{}); return err },
			"grants": func() error {
				_, err := s.ListVoucherGrants(ctx, &catalogv1.ListVoucherGrantsRequest{VoucherId: 1})
				return err
			},
			"inject": func() error {
				_, err := s.InjectVoucher(ctx, &catalogv1.InjectVoucherRequest{VoucherId: 1})
				return err
			},
			"injection": func() error {
				_, err := s.ListVoucherInjections(ctx, &catalogv1.ListVoucherInjectionsRequest{})
				return err
			},
		}
		for name, call := range calls {
			err := call()
			want := apperr.KindForbidden
			if who == "nobody" {
				want = apperr.KindUnauthorized
			}
			if kindOf(err) != want {
				t.Errorf("%s, %s: %v; want %s", who, name, err, want)
			}
		}
	}
}

// The wallet is a customer's: no token is unauthorised, and an admin or staff token (they have no wallet) is forbidden.
func TestVoucherWalletNeedsACustomer(t *testing.T) {
	s := &Server{}
	for name, c := range map[string]struct {
		ctx  context.Context
		want apperr.Kind
	}{
		"nobody":                 {context.Background(), apperr.KindUnauthorized},
		"an admin":               {as(authn.Identity{UserID: "a:1", Roles: []string{"admin"}}), apperr.KindForbidden},
		"staff":                  {as(authn.Identity{UserID: "s:3", Roles: []string{"outlet:1"}}), apperr.KindForbidden},
		"a bad id":               {as(authn.Identity{UserID: "x"}), apperr.KindForbidden},
		"a zero id":              {as(authn.Identity{UserID: "0"}), apperr.KindForbidden},
		"roles on a customer id": {as(authn.Identity{UserID: "42", Roles: []string{"admin"}}), apperr.KindForbidden},
	} {
		if _, err := s.ListMyVouchers(c.ctx, &catalogv1.ListMyVouchersRequest{}); kindOf(err) != c.want {
			t.Errorf("wallet, %s: %v; want %s", name, err, c.want)
		}
		if _, err := s.ClaimVoucher(c.ctx, &catalogv1.ClaimVoucherRequest{Code: "WELCOME10"}); kindOf(err) != c.want {
			t.Errorf("claim, %s: %v; want %s", name, err, c.want)
		}
	}
	if id, err := requireCustomer(as(authn.Identity{UserID: "42"})); err != nil || id != 42 {
		t.Errorf("a customer = %d, %v", id, err)
	}
}
