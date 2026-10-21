// Command user is the command side of authentication: it verifies phone
// numbers, mints sessions, and owns the users table. It exposes no public
// HTTP — only gRPC, plus a small admin surface for health and metrics.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/events"
	"github.com/koobam/wuzz-backend/internal/pkg/grpcx"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
	"github.com/koobam/wuzz-backend/internal/pkg/outbox"
	"github.com/koobam/wuzz-backend/internal/pkg/run"
	"github.com/koobam/wuzz-backend/internal/platform/kafkax"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
	userpg "github.com/koobam/wuzz-backend/internal/services/user/adapters/postgres"
	userredis "github.com/koobam/wuzz-backend/internal/services/user/adapters/redis"
	"github.com/koobam/wuzz-backend/internal/services/user/adapters/verification"
	"github.com/koobam/wuzz-backend/internal/services/user/app"
	"github.com/koobam/wuzz-backend/internal/services/user/app/command"
	usergrpc "github.com/koobam/wuzz-backend/internal/services/user/ports/grpc"
)

var version = "dev"

func main() {
	if err := realMain(); err != nil {
		slog.Error("user failed to start", slog.Any("error", err))
		os.Exit(1)
	}
}

func realMain() error {
	ctx := context.Background()

	cfg, err := config.Load(config.ServiceUser)
	if err != nil {
		return err
	}
	cfg.SetBuildVersion(version)

	log := observability.NewLogger(cfg.Telemetry.LogLevel, cfg.Telemetry.LogFormat, cfg.ServiceName, cfg.Version)
	shutdownTelemetry, err := observability.Setup(ctx, cfg.ServiceName)
	if err != nil {
		return err
	}

	pool, err := postgres.New(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	rdb, err := redisx.New(ctx, cfg.Redis)
	if err != nil {
		return err
	}

	producer, err := kafkax.NewProducer(cfg.Kafka)
	if err != nil {
		return err
	}
	// Topics are created here with a chosen partition count rather than left to
	// a broker's auto-create default. A broker that is down at boot is not
	// fatal: Append still commits to the outbox, and the relay catches up.
	if err := producer.EnsureTopics(ctx, cfg.Kafka, events.Topic(events.AggregateUser)); err != nil {
		log.Warn("kafka topics not ensured; events will queue in the outbox", slog.Any("error", err))
	}
	relay := outbox.NewRelay(pool, producer, cfg.Kafka, log)
	relayCtx, stopRelay := context.WithCancel(ctx)
	defer stopRelay()

	users := userpg.NewUsers(pool)
	members := userpg.NewMembers(pool)
	sessions := userredis.NewSessions(rdb, cfg.Auth.RefreshTokenTTL)
	pending := userredis.NewPendingRegistrations(rdb)

	// config.Load has already refused to start outside dev without Twilio
	// credentials (unless TWILIO_DEV_MODE is on), so this falls back only
	// where a fallback is wanted: dev, or staging with dev mode set to skip
	// paying for SMS.
	var verifications command.Verifications
	if cfg.Twilio.DevMode || cfg.Twilio.AccountSID == "" {
		log.Warn("phone verification is not sending SMS: using the logging verifier",
			slog.Bool("dev_mode", cfg.Twilio.DevMode))
		verifications = verification.NewLogging(rdb, log, cfg.Auth.VerificationCodeTTL)
	} else {
		verifications = verification.NewTwilio(cfg.Twilio)
	}

	// The only process in the system holding a private key.
	signer, err := authn.NewSigner(cfg.Auth)
	if err != nil {
		return err
	}
	verifier, err := authn.NewVerifier(cfg.Auth)
	if err != nil {
		return err
	}

	application := app.New(users, members, pending, sessions, verifications, signer, cfg.Auth.AccessTokenTTL, cfg.Auth.VerificationCodeTTL)

	// Development sign-in without a code: only in a dev environment that is
	// already skipping SMS. Anywhere else the handler stays nil.
	if cfg.Env == "dev" && cfg.Twilio.DevMode {
		log.Warn("development sign-in is ENABLED (APP_ENV=dev): DevLogin signs in any customer by phone")
		application.Commands.DevLogin = command.NewDevLoginHandler(users, sessions, signer, cfg.Auth.AccessTokenTTL)
	}

	grpcServer := grpcx.NewServer(cfg, log, verifier)
	usergrpc.Register(grpcServer.Registrar(), application)

	ready := func(ctx context.Context) error { return pool.Ping(ctx) }
	admin := observability.NewAdminServer(cfg.HTTP.Addr, cfg.Version, ready, log)

	log.Info("user starting", slog.String("env", cfg.Env), slog.String("grpc", cfg.GRPC.Addr))

	return run.Run(ctx, log, cfg.GRPC.ShutdownTimeout,
		run.Component{Name: "telemetry", Start: run.Noop, Stop: shutdownTelemetry},
		run.Component{Name: "postgres", Start: run.Noop,
			Stop: func(context.Context) error { pool.Close(); return nil }},
		run.Component{Name: "redis", Start: run.Noop,
			Stop: func(context.Context) error { return rdb.Close() }},
		run.Component{Name: "kafka", Start: run.Noop,
			Stop: func(context.Context) error { producer.Close(); return nil }},
		run.Component{Name: "outbox-relay",
			Start: func() error { return relay.Run(relayCtx) },
			Stop:  func(context.Context) error { stopRelay(); return nil }},
		run.Component{Name: "admin-http", Start: admin.Start, Stop: admin.Shutdown},
		run.Component{Name: "grpc", Start: grpcServer.Start, Stop: grpcServer.Shutdown},
	)
}
