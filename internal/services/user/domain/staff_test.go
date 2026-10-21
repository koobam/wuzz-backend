package domain

import "testing"

func TestMayUse(t *testing.T) {
	customer := User{}
	cashier := User{Roles: []string{"outlet:3"}}
	admin := User{Roles: []string{"admin"}}

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
		{"unknown audience is refused", admin, "nope", false},
	}
	for _, c := range cases {
		if got := c.user.MayUse(c.audience); got != c.want {
			t.Errorf("%s: MayUse(%q) = %v, want %v", c.name, c.audience, got, c.want)
		}
	}
}
