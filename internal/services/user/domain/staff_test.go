package domain

import "testing"

func TestAccountMayUse(t *testing.T) {
	admin := Account{Kind: KindAdmin, Role: "finance"}
	front := Account{Kind: KindStaff, Role: RoleFront, OutletID: 3}
	wuzzer := Account{Kind: KindStaff, Role: RoleWuzzer}

	cases := []struct {
		name     string
		account  Account
		audience string
		want     bool
	}{
		{"admin can use backoffice", admin, AudienceBackoffice, true},
		{"admin can use pos", admin, AudiencePOS, true},
		{"admin can use station", admin, AudienceStation, true},
		{"front can use pos", front, AudiencePOS, true},
		{"front cannot use backoffice", front, AudienceBackoffice, false},
		{"front cannot use station", front, AudienceStation, false},
		{"wuzzer can use station", wuzzer, AudienceStation, true},
		{"wuzzer cannot use pos", wuzzer, AudiencePOS, false},
		{"wuzzer cannot use backoffice", wuzzer, AudienceBackoffice, false},
		{"the customer app is not a staff audience", admin, "", false},
		{"unknown audience is refused", admin, "nope", false},
	}
	for _, c := range cases {
		if got := c.account.MayUse(c.audience); got != c.want {
			t.Errorf("%s: MayUse(%q) = %v, want %v", c.name, c.audience, got, c.want)
		}
	}
}

func TestAccountRoles(t *testing.T) {
	cases := []struct {
		account Account
		want    []string
	}{
		{Account{Kind: KindAdmin, Role: "finance"}, []string{"admin", "admin:finance"}},
		{Account{Kind: KindStaff, Role: RoleFront, OutletID: 3}, []string{"outlet:3"}},
		{Account{Kind: KindStaff, Role: RoleWuzzer}, []string{"wuzzer"}},
	}
	for _, c := range cases {
		got := c.account.Roles()
		if len(got) != len(c.want) {
			t.Fatalf("%+v: roles = %v, want %v", c.account, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%+v: roles = %v, want %v", c.account, got, c.want)
			}
		}
	}
}

func TestAccountID(t *testing.T) {
	for _, c := range []struct {
		kind string
		n    int64
	}{{KindAdmin, 7}, {KindStaff, 3}} {
		kind, n, ok := ParseAccountID(AccountID(c.kind, c.n))
		if !ok || kind != c.kind || n != c.n {
			t.Errorf("round trip of %s %d gave %s %d %v", c.kind, c.n, kind, n, ok)
		}
	}
	for _, customer := range []string{"42", "", "x:1", "a:", "a:x"} {
		if _, _, ok := ParseAccountID(customer); ok {
			t.Errorf("%q should not parse as an account id", customer)
		}
	}
}

func TestAccountValidate(t *testing.T) {
	if err := (Account{Kind: KindStaff, Role: RoleFront, Name: "Kasir"}).Validate(); err == nil {
		t.Error("front staff without an outlet should be refused")
	}
	if err := (Account{Kind: KindStaff, Role: RoleWuzzer, Name: "W", OutletID: 2}).Validate(); err == nil {
		t.Error("a wuzzer with an outlet should be refused")
	}
	if err := (Account{Kind: KindStaff, Role: RoleFront, Name: "Kasir", OutletID: 2}).Validate(); err != nil {
		t.Errorf("valid front staff refused: %v", err)
	}
}
