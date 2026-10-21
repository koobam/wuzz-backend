// Command catalog owns outlets and the laundry services they sell. It exposes
// only gRPC, plus a small admin surface for health and metrics.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/errtrack"
	"github.com/koobam/wuzz-backend/internal/pkg/events"
	"github.com/koobam/wuzz-backend/internal/pkg/grpcx"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
	"github.com/koobam/wuzz-backend/internal/pkg/outbox"
	"github.com/koobam/wuzz-backend/internal/pkg/run"
	"github.com/koobam/wuzz-backend/internal/platform/kafkax"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	catalogpg "github.com/koobam/wuzz-backend/internal/services/catalog/adapters/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app"
	cataloggrpc "github.com/koobam/wuzz-backend/internal/services/catalog/ports/grpc"
)

var version = "dev"

func main() {
	if err := realMain(); err != nil {
		slog.Error("catalog failed to start", slog.Any("error", err))
		os.Exit(1)
	}
}

func realMain() error {
	ctx := context.Background()

	cfg, err := config.Load(config.ServiceCatalog)
	if err != nil {
		return err
	}
	cfg.SetBuildVersion(version)

	log := observability.NewLogger(cfg.Telemetry.LogLevel, cfg.Telemetry.LogFormat, cfg.ServiceName, cfg.Version)

	// Off unless SENTRY_DSN is set; then every ERROR log also becomes a Sentry issue.
	flushErrors, err := errtrack.Init(cfg)
	if err != nil {
		return err
	}
	log = errtrack.Wrap(log)
	shutdownTelemetry, err := observability.Setup(ctx, cfg.ServiceName)
	if err != nil {
		return err
	}

	pool, err := postgres.New(ctx, cfg.Postgres)
	if err != nil {
		return err
	}

	producer, err := kafkax.NewProducer(cfg.Kafka)
	if err != nil {
		return err
	}
	if err := producer.EnsureTopics(ctx, cfg.Kafka, events.Topic(events.AggregateOutlet), events.Topic(events.AggregateService)); err != nil {
		log.Warn("kafka topics not ensured; events will queue in the outbox", slog.Any("error", err))
	}
	relay := outbox.NewRelay(pool, producer, cfg.Kafka, log)
	relayCtx, stopRelay := context.WithCancel(ctx)
	defer stopRelay()

	verifier, err := authn.NewVerifier(cfg.Auth)
	if err != nil {
		return err
	}

	application := app.New(catalogpg.NewStore(pool), catalogpg.NewDiscovery(pool))

	grpcServer := grpcx.NewServer(cfg, log, verifier)
	cataloggrpc.Register(grpcServer.Registrar(), application)

	admin := observability.NewAdminServer(cfg.HTTP.Addr, cfg.Version, pool.Ping, log)

	log.Info("catalog starting", slog.String("env", cfg.Env), slog.String("grpc", cfg.GRPC.Addr))

	return run.Run(ctx, log, cfg.GRPC.ShutdownTimeout,
		run.Component{Name: "telemetry", Start: run.Noop, Stop: shutdownTelemetry},
		run.Component{Name: "errtrack", Start: run.Noop, Stop: flushErrors},
		run.Component{Name: "postgres", Start: run.Noop,
			Stop: func(context.Context) error { pool.Close(); return nil }},
		run.Component{Name: "kafka", Start: run.Noop,
			Stop: func(context.Context) error { producer.Close(); return nil }},
		run.Component{Name: "outbox-relay",
			Start: func() error { return relay.Run(relayCtx) },
			Stop:  func(context.Context) error { stopRelay(); return nil }},
		run.Component{Name: "admin-http", Start: admin.Start, Stop: admin.Shutdown},
		run.Component{Name: "grpc", Start: grpcServer.Start, Stop: grpcServer.Shutdown},
	)
}
