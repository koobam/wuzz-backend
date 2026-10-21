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
	ServiceGateway     Service = "gateway"
	ServiceUser        Service = "user"
	ServiceCatalog     Service = "catalog"
	ServiceTransaction Service = "transaction"
)

type Config struct {
	ServiceName string
	Service     Service
	Env         string // dev | staging | production
	Version     string

	HTTP      HTTP
	GRPC      GRPC
	Postgres  Postgres
	Typesense Typesense
	Redis     Redis
	Kafka     Kafka
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

// Typesense is the optional search engine behind service search. With no URL the
// catalog falls back to matching the service list in memory.
type Typesense struct {
	URL    string
	APIKey string
}

type Redis struct {
	Addr string
}

type Auth struct {
	Issuer              string
	JWTPrivateKey       string
	JWTPublicKeys       string
	AccessTokenTTL      time.Duration
	RefreshTokenTTL     time.Duration
	VerificationCodeTTL time.Duration
	VerifiedPhoneTTL    time.Duration
}

type Kafka struct {
	Brokers     []string
	TopicPrefix string
	// TopicPartitions is what EnsureTopics creates a topic with. Ordering holds
	// only within a partition and events are keyed by aggregate id, so this is
	// the consumers' parallelism: raising it later is possible, lowering it is not.
	TopicPartitions int32
	// TopicReplication of -1 means the broker's own default.
	TopicReplication int16
	// OutboxInterval is the backstop sweep of a command service's outbox;
	// LISTEN/NOTIFY wakes the relay sooner in the normal case.
	OutboxInterval time.Duration
	OutboxBatch    int
}

type Twilio struct {
	AccountSID string
	AuthToken  string
	VerifySID  string
	DevMode    bool
}

// Upstreams is where the gateway finds every backend service's gRPC address.
type Upstreams struct {
	User        string
	Catalog     string
	Transaction string
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
			Addr:            getenv("HTTP_ADDR", devAddr(env, service).http),
			ReadTimeout:     10 * time.Second,
			WriteTimeout:    10 * time.Second,
			ShutdownTimeout: 15 * time.Second,
			TrustedProxies:  splitCSV(getenv("TRUSTED_PROXIES", "")),
			CORSOrigins:     splitCSV(getenv("CORS_ORIGINS", "http://localhost:5174")),
			CookieSecure:    getenvBool("COOKIE_SECURE", env != "dev"),
		},
		GRPC: GRPC{
			Addr:            getenv("GRPC_ADDR", devAddr(env, service).grpc),
			ShutdownTimeout: 15 * time.Second,
		},
		Postgres: Postgres{
			// Port 5433: docker-compose maps the local dev postgres there, not
			// 5432 — something else on dev machines here already listens on
			// 5432 (an SSH tunnel to a remote Postgres).
			DSN:             getenv("POSTGRES_DSN", DefaultDSN(service)),
			MaxConns:        int32(getenvInt("POSTGRES_MAX_CONNS", 10)),
			MinConns:        int32(getenvInt("POSTGRES_MIN_CONNS", 1)),
			MaxConnLifetime: time.Hour,
		},
		Typesense: Typesense{
			// Empty by default: search then runs in memory, so nothing extra is
			// needed to run the service locally.
			URL:    getenv("TYPESENSE_URL", ""),
			APIKey: getenv("TYPESENSE_API_KEY", ""),
		},
		Redis: Redis{
			// Port 6380: see the DSN comment above — same reason.
			Addr: getenv("REDIS_ADDR", "localhost:6380"),
		},
		Kafka: Kafka{
			Brokers:          splitCSV(getenv("KAFKA_BROKERS", "localhost:29092")),
			TopicPrefix:      getenv("KAFKA_TOPIC_PREFIX", "wuzz"),
			TopicPartitions:  int32(getenvInt("KAFKA_TOPIC_PARTITIONS", 3)),
			TopicReplication: int16(getenvInt("KAFKA_TOPIC_REPLICATION", -1)),
			OutboxInterval:   getenvDuration("OUTBOX_INTERVAL", 2*time.Second),
			OutboxBatch:      getenvInt("OUTBOX_BATCH", 100),
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
			User:        getenv("USER_ADDR", "localhost:9090"),
			Catalog:     getenv("CATALOG_ADDR", "localhost:9091"),
			Transaction: getenv("TRANSACTION_ADDR", "localhost:9092"),
		},
		Telemetry: Telemetry{
			LogLevel:  getenv("LOG_LEVEL", "info"),
			LogFormat: getenv("LOG_FORMAT", "text"),
		},
	}

	// Left empty outside dev is refused here; inside dev, authn.NewSigner and
	// authn.NewVerifier fall back to a fixed development keypair themselves
	// (see authn/keys.go) — config only enforces that production cannot.
	// Only `user` signs tokens, so only `user` may (and must) hold the private
	// key. Every service verifies, so every service needs the public keys.
	if cfg.Auth.JWTPublicKeys == "" && env != "dev" {
		return nil, fmt.Errorf("config: JWT_PUBLIC_KEYS is required outside dev")
	}
	if service == ServiceUser && cfg.Auth.JWTPrivateKey == "" && env != "dev" {
		return nil, fmt.Errorf("config: JWT_PRIVATE_KEY is required for the user service outside dev")
	}

	if service == ServiceUser && !cfg.Twilio.DevMode && cfg.Twilio.AccountSID == "" && env != "dev" {
		return nil, fmt.Errorf("config: TWILIO_ACCOUNT_SID is required outside dev (or set TWILIO_DEV_MODE=true)")
	}

	return cfg, nil
}

type addrs struct{ http, grpc string }

// devAddr gives every service its own ports when running side by side on one
// machine (APP_ENV=dev). Outside dev each service is its own container, so they
// all use :8080 / :9090 and are told apart by their Service name.
func devAddr(env string, service Service) addrs {
	if env != "dev" {
		return addrs{http: ":8080", grpc: ":9090"}
	}
	switch service {
	case ServiceUser:
		return addrs{http: ":8091", grpc: ":9090"}
	case ServiceCatalog:
		return addrs{http: ":8093", grpc: ":9091"}
	case ServiceTransaction:
		return addrs{http: ":8094", grpc: ":9092"}
	default: // gateway
		return addrs{http: ":8092", grpc: ":9090"}
	}
}

// DefaultDSN is the local database for a service: one database per service,
// all on the dev Postgres. (user keeps the original `wuzz` database.)
func DefaultDSN(service Service) string {
	name := "wuzz"
	if service == ServiceCatalog || service == ServiceTransaction {
		name = "wuzz_" + string(service)
	}
	return "postgres://wuzz:wuzz@localhost:5433/" + name + "?sslmode=disable"
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
