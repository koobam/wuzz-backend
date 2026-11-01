package command

import (
	"context"
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

type fakeCustomers struct{ byPhone map[string]domain.User }

func (f fakeCustomers) ByPhone(_ context.Context, phone string) (domain.User, error) {
	if u, ok := f.byPhone[phone]; ok {
		return u, nil
	}
	return domain.User{}, apperr.NotFound("no such customer")
}
func (fakeCustomers) ByID(context.Context, string) (domain.User, error)    { return domain.User{}, nil }
func (fakeCustomers) ExistsUsername(context.Context, string) (bool, error) { return false, nil }
func (fakeCustomers) Create(context.Context, domain.User) (domain.User, error) {
	return domain.User{}, nil
}
func (fakeCustomers) SetStatus(context.Context, string, string) (domain.User, error) {
	return domain.User{}, nil
}

type fakeVerifications struct{ started []string }

func (f *fakeVerifications) Start(_ context.Context, phone string) error {
	f.started = append(f.started, phone)
	return nil
}
func (*fakeVerifications) Check(context.Context, string, string) (bool, error) { return false, nil }

func TestSendCustomerLoginCode(t *testing.T) {
	const phone = "6281234500001"
	customers := fakeCustomers{byPhone: map[string]domain.User{
		phone:           {Status: domain.StatusActive},
		"6281234500002": {Status: domain.StatusBanned},
		"6281234500003": {Status: domain.StatusActive, HasPin: true},
		"6281234500004": {Status: domain.StatusActive, HasPin: true, ReverifyRequired: true},
		"6281234500005": {Status: domain.StatusBanned, HasPin: true},
	}}

	cases := []struct {
		name       string
		input      string
		forceOtp   bool
		wantStart  []string
		wantMethod string
	}{
		{"an active customer without a PIN gets a code", "081234500001", false, []string{phone}, MethodOtp},
		{"the same number written with the country code", "+6281234500001", false, []string{phone}, MethodOtp},
		{"an unknown number gets no code, and no error", "081234500099", false, nil, MethodOtp},
		{"a disabled customer gets no code, and no error", "081234500002", false, nil, MethodOtp},
		{"a customer with a PIN signs in with it, no code", "081234500003", false, nil, MethodPin},
		{"a customer with a PIN who forgot it gets a code", "081234500003", true, []string{"6281234500003"}, MethodOtp},
		{"a customer flagged to verify again gets a code", "081234500004", false, []string{"6281234500004"}, MethodOtp},
		{"a disabled customer with a PIN looks like any other", "081234500005", false, nil, MethodOtp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			verifications := &fakeVerifications{}
			h := NewSendCustomerLoginCodeHandler(customers, verifications, 5*time.Minute)

			res, err := h.Handle(context.Background(), SendCustomerLoginCode{Phone: c.input, ForceOtp: c.forceOtp})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if res.Method != c.wantMethod {
				t.Errorf("method %q, want %q", res.Method, c.wantMethod)
			}
			if !res.ExpiresAt.After(time.Now()) {
				t.Errorf("expires %v is not in the future: the answer must look the same whether or not a code was sent", res.ExpiresAt)
			}
			if len(verifications.started) != len(c.wantStart) || (len(c.wantStart) == 1 && verifications.started[0] != c.wantStart[0]) {
				t.Errorf("codes started for %v, want %v", verifications.started, c.wantStart)
			}
		})
	}

	t.Run("a number that is not a phone number is refused", func(t *testing.T) {
		h := NewSendCustomerLoginCodeHandler(customers, &fakeVerifications{}, time.Minute)
		if _, err := h.Handle(context.Background(), SendCustomerLoginCode{Phone: "abc"}); err == nil {
			t.Error("expected an error for a malformed number")
		}
	})
}
