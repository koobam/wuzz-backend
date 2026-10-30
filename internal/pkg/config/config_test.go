package config

import (
	"strings"
	"testing"
)

func TestJWTKeyRequirementsOutsideDev(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("TWILIO_DEV_MODE", "true")
	t.Setenv("JWT_PUBLIC_KEYS", "pub")
	t.Setenv("JWT_PRIVATE_KEY", "")
	t.Setenv("INTERNAL_API_KEY", "a-long-enough-internal-key")

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

func TestFixedOTPCode(t *testing.T) {
	t.Setenv("JWT_PUBLIC_KEYS", "pub")
	t.Setenv("JWT_PRIVATE_KEY", "priv")
	cases := []struct {
		name, env, devMode, code string
		ok                       bool
	}{
		{"dev", "dev", "true", "000000", true},
		{"staging in dev mode", "staging", "true", "000000", true},
		{"nothing set", "production", "true", "", true},
		{"production refuses it", "production", "true", "000000", false},
		{"it needs dev mode", "staging", "false", "000000", false},
		{"it must be six digits", "staging", "true", "12345", false},
		{"digits only", "staging", "true", "12345a", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("APP_ENV", c.env)
			t.Setenv("TWILIO_DEV_MODE", c.devMode)
			t.Setenv("OTP_FIXED_CODE", c.code)
			_, err := Load(ServiceUser)
			if (err == nil) != c.ok {
				t.Errorf("Load error = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestInternalAPIKey(t *testing.T) {
	t.Setenv("JWT_PUBLIC_KEYS", "pub")
	t.Setenv("JWT_PRIVATE_KEY", "priv")
	t.Setenv("TWILIO_DEV_MODE", "true")
	t.Setenv("OTP_FIXED_CODE", "")
	cases := []struct {
		name, env, key string
		svc            Service
		ok             bool
		want           string
	}{
		{"dev has a built-in key", "dev", "", ServiceCatalog, true, DevInternalKey},
		{"dev keeps one that is set", "dev", "my-own-key", ServiceTransaction, true, "my-own-key"},
		{"staging may start without it", "staging", "", ServiceCatalog, true, ""},
		{"production needs it", "production", "", ServiceCatalog, false, ""},
		{"production refuses a short one", "production", "short", ServiceTransaction, false, ""},
		{"production refuses the dev key", "production", DevInternalKey, ServiceCatalog, false, ""},
		{"production accepts a real one", "production", "0123456789abcdef", ServiceTransaction, true, "0123456789abcdef"},
		{"the gateway does not use it", "production", "", ServiceGateway, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("APP_ENV", c.env)
			t.Setenv("INTERNAL_API_KEY", c.key)
			cfg, err := Load(c.svc)
			if (err == nil) != c.ok {
				t.Fatalf("Load error = %v, want ok=%v", err, c.ok)
			}
			if err == nil && cfg.Internal.APIKey != c.want {
				t.Errorf("key = %q, want %q", cfg.Internal.APIKey, c.want)
			}
		})
	}
}

func TestMediaConfig(t *testing.T) {
	t.Setenv("JWT_PUBLIC_KEYS", "pub")
	t.Setenv("JWT_PRIVATE_KEY", "priv")
	t.Setenv("TWILIO_DEV_MODE", "true")
	t.Setenv("INTERNAL_API_KEY", "a-long-enough-internal-key")
	all := map[string]string{
		"MEDIA_ENDPOINT": "https://r2.example", "MEDIA_BUCKET": "b", "MEDIA_ACCESS_KEY_ID": "i", "MEDIA_SECRET_ACCESS_KEY": "s", "MEDIA_PUBLIC_BASE_URL": "https://assets.wuzz.care/",
	}
	cases := []struct {
		name       string
		env        string
		set        map[string]string
		ok         bool
		configured bool
	}{
		{"nothing set: uploads off", "staging", nil, true, false},
		{"a full bucket", "staging", all, true, true},
		{"a bucket missing its secret", "staging", map[string]string{"MEDIA_ENDPOINT": "https://r2.example", "MEDIA_BUCKET": "b", "MEDIA_ACCESS_KEY_ID": "i", "MEDIA_PUBLIC_BASE_URL": "https://x"}, false, false},
		{"only an endpoint", "production", map[string]string{"MEDIA_ENDPOINT": "https://r2.example"}, false, false},
		{"a local directory in dev", "dev", map[string]string{"MEDIA_LOCAL_DIR": "/tmp/m", "MEDIA_PUBLIC_BASE_URL": "http://localhost:8092/media"}, true, true},
		{"a local directory without its address", "dev", map[string]string{"MEDIA_LOCAL_DIR": "/tmp/m"}, false, false},
		{"a local directory in staging is refused", "staging", map[string]string{"MEDIA_LOCAL_DIR": "/tmp/m", "MEDIA_PUBLIC_BASE_URL": "https://x"}, false, false},
		{"a silly size limit", "staging", map[string]string{"MEDIA_MAX_UPLOAD_BYTES": "10"}, false, false},
	}
	for _, c := range cases {
		for _, k := range []string{"MEDIA_ENDPOINT", "MEDIA_BUCKET", "MEDIA_ACCESS_KEY_ID", "MEDIA_SECRET_ACCESS_KEY", "MEDIA_PUBLIC_BASE_URL", "MEDIA_LOCAL_DIR", "MEDIA_MAX_UPLOAD_BYTES"} {
			t.Setenv(k, "")
		}
		for k, v := range c.set {
			t.Setenv(k, v)
		}
		t.Setenv("APP_ENV", c.env)
		cfg, err := Load(ServiceGateway)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
			continue
		}
		if err == nil && cfg.Media.Configured() != c.configured {
			t.Errorf("%s: Configured() = %v, want %v", c.name, cfg.Media.Configured(), c.configured)
		}
		if err == nil && strings.HasSuffix(cfg.Media.PublicBaseURL, "/") {
			t.Errorf("%s: the public base URL keeps its trailing slash: %q", c.name, cfg.Media.PublicBaseURL)
		}
	}
	// Other services never need media, whatever is half set.
	t.Setenv("APP_ENV", "staging")
	t.Setenv("MEDIA_ENDPOINT", "https://r2.example")
	if _, err := Load(ServiceCatalog); err != nil {
		t.Errorf("catalog should ignore media settings: %v", err)
	}
}
