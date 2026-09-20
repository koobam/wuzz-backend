// Package config loads every service's configuration from the environment.
// One shape for all services — a service reads only the fields it needs, but
// they all agree on the same variable names, so a shared docker-compose
// service-env block works.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Service string

const (
	ServiceGateway Service = "gateway"
	ServiceUser    Service = "user"
)

type Config struct {
	ServiceName string
	Service     Service
	Env         string // dev | staging | production
	Version     string

	HTTP      HTTP
	GRPC      GRPC
	Postgres  Postgres
	Redis     Redis
	Auth      Auth
	Twilio    Twilio
	Upstreams Upstreams
	Telemetry Telemetry
}

func (c *Config) SetBuildVersion(v string) { c.Version = v }

type HTTP struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	TrustedProxies  []string
	CORSOrigins     []string
	CookieSecure    bool
}

type GRPC struct {
	Addr            string
	ShutdownTimeout time.Duration
}

type Postgres struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
}

type Redis struct {
	Addr string
}

type Auth struct {
	Issuer               string
	JWTPrivateKey        string
	JWTPublicKeys        string
	AccessTokenTTL       time.Duration
	RefreshTokenTTL      time.Duration
	VerificationCodeTTL  time.Duration
	VerifiedPhoneTTL     time.Duration
}

type Twilio struct {
	AccountSID string
	AuthToken  string
	VerifySID  string
	DevMode    bool
}

// Upstreams is where the gateway finds every backend service's gRPC address.
type Upstreams struct {
	User string
}

type Telemetry struct {
	LogLevel  string
	LogFormat string
}

func Load(service Service) (*Config, error) {
	env := getenv("APP_ENV", "dev")

	cfg := &Config{
		ServiceName: "wuzz-" + string(service),
		Service:     service,
		Env:         env,
		Version:     "dev",
		HTTP: HTTP{
			Addr:            getenv("HTTP_ADDR", ":8080"),
			ReadTimeout:     10 * time.Second,
			WriteTimeout:    10 * time.Second,
			ShutdownTimeout: 15 * time.Second,
			TrustedProxies:  splitCSV(getenv("TRUSTED_PROXIES", "")),
			CORSOrigins:     splitCSV(getenv("CORS_ORIGINS", "http://localhost:5174")),
			CookieSecure:    getenvBool("COOKIE_SECURE", env != "dev"),
		},
		GRPC: GRPC{
			Addr:            getenv("GRPC_ADDR", ":9090"),
			ShutdownTimeout: 15 * time.Second,
		},
		Postgres: Postgres{
			// Port 5433: docker-compose maps the local dev postgres there, not
			// 5432 — something else on dev machines here already listens on
			// 5432 (an SSH tunnel to a remote Postgres).
			DSN: getenv("POSTGRES_DSN", "postgres://wuzz:wuzz@localhost:5433/wuzz?sslmode=disable"),
			MaxConns:        int32(getenvInt("POSTGRES_MAX_CONNS", 10)),
			MinConns:        int32(getenvInt("POSTGRES_MIN_CONNS", 1)),
			MaxConnLifetime: time.Hour,
		},
		Redis: Redis{
			// Port 6380: see the DSN comment above — same reason.
			Addr: getenv("REDIS_ADDR", "localhost:6380"),
		},
		Auth: Auth{
			Issuer:              getenv("JWT_ISSUER", "wuzz-backend"),
			JWTPrivateKey:       os.Getenv("JWT_PRIVATE_KEY"),
			JWTPublicKeys:       os.Getenv("JWT_PUBLIC_KEYS"),
			AccessTokenTTL:      getenvDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
			RefreshTokenTTL:     getenvDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
			VerificationCodeTTL: getenvDuration("VERIFICATION_CODE_TTL", 5*time.Minute),
			VerifiedPhoneTTL:    getenvDuration("VERIFIED_PHONE_TTL", 10*time.Minute),
		},
		Twilio: Twilio{
			AccountSID: os.Getenv("TWILIO_ACCOUNT_SID"),
			AuthToken:  os.Getenv("TWILIO_AUTH_TOKEN"),
			VerifySID:  os.Getenv("TWILIO_VERIFY_SID"),
			DevMode:    getenvBool("TWILIO_DEV_MODE", env == "dev"),
		},
		Upstreams: Upstreams{
			User: getenv("USER_ADDR", "localhost:9090"),
		},
		Telemetry: Telemetry{
			LogLevel:  getenv("LOG_LEVEL", "info"),
			LogFormat: getenv("LOG_FORMAT", "text"),
		},
	}

	// Left empty outside dev is refused here; inside dev, authn.NewSigner and
	// authn.NewVerifier fall back to a fixed development keypair themselves
	// (see authn/keys.go) — config only enforces that production cannot.
	if (cfg.Auth.JWTPrivateKey == "" || cfg.Auth.JWTPublicKeys == "") && env != "dev" {
		return nil, fmt.Errorf("config: JWT_PRIVATE_KEY and JWT_PUBLIC_KEYS are required outside dev")
	}

	if service == ServiceUser && !cfg.Twilio.DevMode && cfg.Twilio.AccountSID == "" && env != "dev" {
		return nil, fmt.Errorf("config: TWILIO_ACCOUNT_SID is required outside dev (or set TWILIO_DEV_MODE=true)")
	}

	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
