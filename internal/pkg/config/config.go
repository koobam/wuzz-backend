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
	Media     Media
	Redis     Redis
	Kafka     Kafka
	Auth      Auth
	Twilio    Twilio
	Upstreams Upstreams
	Telemetry Telemetry
	Internal  Internal
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

// Typesense is the optional search index behind the list endpoints. With no URL the
// lists read Postgres directly; with one, a search that cannot reach it is an error (no quiet fallback).
type Typesense struct {
	URL    string
	APIKey string
	// ReconcileInterval is how often a service compares its index with Postgres and repairs any difference (a write the index
	// missed, a row deleted behind its back). 0 turns the sweep off.
	ReconcileInterval time.Duration
}

// Media is where uploaded pictures are kept, and the address they are served from. Only the gateway uses it. Either an
// S3-compatible bucket (Cloudflare R2, Google Cloud Storage's S3 interoperability, MinIO...) or, in dev only, a local
// directory the gateway serves itself. With neither set, uploads answer 503: there is no quiet fallback.
type Media struct {
	Endpoint        string // https://<account>.r2.cloudflarestorage.com, https://storage.googleapis.com ...
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Region          string // "auto" for R2 and GCS
	// PublicBaseURL is where a stored key is read from by browsers, without a trailing slash: https://assets.wuzz.care.
	PublicBaseURL string
	// LocalDir keeps the pictures on this machine's disk and the gateway serves them under /media (dev only).
	LocalDir       string
	MaxUploadBytes int64
}

// Configured says whether uploads can work.
func (m Media) Configured() bool { return m.LocalDir != "" || m.s3Complete() }

func (m Media) s3Complete() bool {
	return m.Endpoint != "" && m.Bucket != "" && m.AccessKeyID != "" && m.SecretAccessKey != "" && m.PublicBaseURL != ""
}

func (m Media) s3Any() bool {
	return m.Endpoint != "" || m.Bucket != "" || m.AccessKeyID != "" || m.SecretAccessKey != ""
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
	// FixedCode, when set, is every sign-in code (OTP_FIXED_CODE=000000), so dev and staging can be used
	// without reading a log. Only with DevMode, never in production: see Load.
	FixedCode string
}

// Internal is the shared secret services use with each other where there is no signed-in person to speak for them: the order
// service presents it to the catalog to take an order's materials out of stock. Dev has a built-in key; anywhere else it must
// be set (16 characters or more) on both services; production refuses to start without it.
type Internal struct {
	APIKey string
}

// DevInternalKey is the key used in dev when INTERNAL_API_KEY is not set. Never accepted outside dev.
const DevInternalKey = "dev-internal-key"

// Upstreams is where the gateway finds every backend service's gRPC address.
type Upstreams struct {
	User        string
	Catalog     string
	Transaction string
}

type Telemetry struct {
	LogLevel  string
	LogFormat string
	// OTLPEndpoint is the collector's OTLP/gRPC address (host:port). Empty
	// turns tracing off, which is the right default for tests and a bare `go run`.
	OTLPEndpoint string
	// TraceRatio is the share of new traces kept, 0..1.
	TraceRatio float64
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
			// Empty by default: lists then read Postgres, so nothing extra is
			// needed to run the service locally.
			URL:               getenv("TYPESENSE_URL", ""),
			APIKey:            getenv("TYPESENSE_API_KEY", ""),
			ReconcileInterval: getenvDuration("TYPESENSE_RECONCILE_INTERVAL", 5*time.Minute),
		},
		Media: Media{
			Endpoint:        strings.TrimRight(getenv("MEDIA_ENDPOINT", ""), "/"),
			Bucket:          getenv("MEDIA_BUCKET", ""),
			AccessKeyID:     os.Getenv("MEDIA_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("MEDIA_SECRET_ACCESS_KEY"),
			Region:          getenv("MEDIA_REGION", "auto"),
			PublicBaseURL:   strings.TrimRight(getenv("MEDIA_PUBLIC_BASE_URL", ""), "/"),
			LocalDir:        getenv("MEDIA_LOCAL_DIR", ""),
			MaxUploadBytes:  int64(getenvInt("MEDIA_MAX_UPLOAD_BYTES", 5<<20)),
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
			FixedCode:  os.Getenv("OTP_FIXED_CODE"),
		},
		Upstreams: Upstreams{
			User:        getenv("USER_ADDR", "localhost:9090"),
			Catalog:     getenv("CATALOG_ADDR", "localhost:9091"),
			Transaction: getenv("TRANSACTION_ADDR", "localhost:9092"),
		},
		Telemetry: Telemetry{
			LogLevel:  getenv("LOG_LEVEL", "info"),
			LogFormat: getenv("LOG_FORMAT", "text"),

			OTLPEndpoint: getenv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
			TraceRatio:   getenvFloat("OTEL_TRACE_RATIO", 1),
		},
		Internal: Internal{APIKey: os.Getenv("INTERNAL_API_KEY")},
	}
	if err := cfg.Media.validate(env, service); err != nil {
		return nil, err
	}
	if cfg.Internal.APIKey == "" && env == "dev" {
		cfg.Internal.APIKey = DevInternalKey
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

	if service == ServiceUser && cfg.Twilio.FixedCode != "" {
		switch {
		case env == "production":
			return nil, fmt.Errorf("config: OTP_FIXED_CODE must not be set in production: anyone could sign in as anyone")
		case !cfg.Twilio.DevMode:
			return nil, fmt.Errorf("config: OTP_FIXED_CODE only works with TWILIO_DEV_MODE=true")
		case !sixDigits(cfg.Twilio.FixedCode):
			return nil, fmt.Errorf("config: OTP_FIXED_CODE must be exactly six digits")
		}
	}

	// Production must have a real key. Elsewhere outside dev an empty key is allowed (the services start, orders work) but the
	// order -> stock link stays off, and the order service says so in its log until the key is set.
	if (service == ServiceCatalog || service == ServiceTransaction) && env == "production" {
		if len(cfg.Internal.APIKey) < 16 || cfg.Internal.APIKey == DevInternalKey {
			return nil, fmt.Errorf("config: INTERNAL_API_KEY is required in production, at least 16 characters, and must not be the development key")
		}
	}

	return cfg, nil
}

func sixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
	if service == ServiceCatalog || service == ServiceTransaction || service == ServiceGateway {
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

func getenvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			return f
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

// validate refuses a half-set media configuration at start-up (a deploy that forgot one variable must not look like "uploads
// are off") and keeps the local directory out of anything but dev. Only the gateway uses media; the other services ignore it.
func (m Media) validate(env string, service Service) error {
	if service != ServiceGateway {
		return nil
	}
	if m.LocalDir != "" {
		if env != "dev" {
			return fmt.Errorf("config: MEDIA_LOCAL_DIR is for dev only; use MEDIA_ENDPOINT and the bucket settings in %s", env)
		}
		if m.PublicBaseURL == "" {
			return fmt.Errorf("config: MEDIA_PUBLIC_BASE_URL is required with MEDIA_LOCAL_DIR (the address the gateway serves /media at)")
		}
		return nil
	}
	if m.s3Any() && !m.s3Complete() {
		return fmt.Errorf("config: media is half set: MEDIA_ENDPOINT, MEDIA_BUCKET, MEDIA_ACCESS_KEY_ID, MEDIA_SECRET_ACCESS_KEY and MEDIA_PUBLIC_BASE_URL are all required together")
	}
	if m.MaxUploadBytes < 1<<10 || m.MaxUploadBytes > 50<<20 {
		return fmt.Errorf("config: MEDIA_MAX_UPLOAD_BYTES must be between 1 KiB and 50 MiB")
	}
	return nil
}
