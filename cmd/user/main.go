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
	"github.com/koobam/wuzz-backend/internal/pkg/grpcx"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
	"github.com/koobam/wuzz-backend/internal/pkg/run"
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

	users := userpg.NewUsers(pool)
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

	application := app.New(users, pending, sessions, verifications, signer, cfg.Auth.AccessTokenTTL, cfg.Auth.VerificationCodeTTL)

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
		run.Component{Name: "admin-http", Start: admin.Start, Stop: admin.Shutdown},
		run.Component{Name: "grpc", Start: grpcServer.Start, Stop: grpcServer.Shutdown},
	)
}
