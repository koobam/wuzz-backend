package domain

import "testing"

func TestMayUse(t *testing.T) {
	customer := User{}
	cashier := User{Roles: []string{"outlet:3"}}
	admin := User{Roles: []string{"admin"}}
	wuzzer := User{Roles: []string{"wuzzer"}}

	cases := []struct {
		name     string
		user     User
		audience string
		want     bool
	}{
		{"customer app is open to everyone", customer, "", true},
		{"customer cannot use backoffice", customer, AudienceBackoffice, false},
		{"customer cannot use pos", customer, AudiencePOS, false},
		{"cashier can use pos", cashier, AudiencePOS, true},
		{"cashier cannot use backoffice", cashier, AudienceBackoffice, false},
		{"admin can use backoffice", admin, AudienceBackoffice, true},
		{"admin can use pos", admin, AudiencePOS, true},
		{"wuzzer can use station", wuzzer, AudienceStation, true},
		{"wuzzer cannot use pos", wuzzer, AudiencePOS, false},
		{"wuzzer cannot use backoffice", wuzzer, AudienceBackoffice, false},
		{"cashier cannot use station", cashier, AudienceStation, false},
		{"customer cannot use station", customer, AudienceStation, false},
		{"admin can use station", admin, AudienceStation, true},
		{"unknown audience is refused", admin, "nope", false},
	}
	for _, c := range cases {
		if got := c.user.MayUse(c.audience); got != c.want {
			t.Errorf("%s: MayUse(%q) = %v, want %v", c.name, c.audience, got, c.want)
		}
	}
}
