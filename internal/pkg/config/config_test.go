package config

import "testing"

func TestJWTKeyRequirementsOutsideDev(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("TWILIO_DEV_MODE", "true")
	t.Setenv("JWT_PUBLIC_KEYS", "pub")
	t.Setenv("JWT_PRIVATE_KEY", "")

	// A verifier-only service must start without the private key.
	for _, svc := range []Service{ServiceGateway, ServiceCatalog, ServiceTransaction} {
		if _, err := Load(svc); err != nil {
			t.Errorf("%s should not need JWT_PRIVATE_KEY: %v", svc, err)
		}
	}
	// The signer must have it.
	if _, err := Load(ServiceUser); err == nil {
		t.Error("user must require JWT_PRIVATE_KEY outside dev")
	}
	// And nobody starts without public keys.
	t.Setenv("JWT_PUBLIC_KEYS", "")
	if _, err := Load(ServiceGateway); err == nil {
		t.Error("gateway must require JWT_PUBLIC_KEYS outside dev")
	}
}
