// Command transaction owns orders. It exposes
// only gRPC, plus a small admin surface for health and metrics.
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
	txpg "github.com/koobam/wuzz-backend/internal/services/transaction/adapters/postgres"
	"github.com/koobam/wuzz-backend/internal/services/transaction/app"
	txgrpc "github.com/koobam/wuzz-backend/internal/services/transaction/ports/grpc"
)

var version = "dev"

func main() {
	if err := realMain(); err != nil {
		slog.Error("transaction failed to start", slog.Any("error", err))
		os.Exit(1)
	}
}

func realMain() error {
	ctx := context.Background()

	cfg, err := config.Load(config.ServiceTransaction)
	if err != nil {
		return err
	}
	cfg.SetBuildVersion(version)

	log := observability.NewLogger(cfg.Telemetry.LogLevel, cfg.Telemetry.LogFormat, cfg.ServiceName, cfg.Version)
	shutdownTelemetry, err := observability.Setup(ctx, cfg)
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
	if err := producer.EnsureTopics(ctx, cfg.Kafka, events.Topic(events.AggregateOrder)); err != nil {
		log.Warn("kafka topics not ensured; events will queue in the outbox", slog.Any("error", err))
	}
	relay := outbox.NewRelay(pool, producer, cfg.Kafka, log)
	relayCtx, stopRelay := context.WithCancel(ctx)
	defer stopRelay()

	verifier, err := authn.NewVerifier(cfg.Auth)
	if err != nil {
		return err
	}

	application := app.New(txpg.NewStore(pool))

	grpcServer := grpcx.NewServer(cfg, log, verifier)
	txgrpc.Register(grpcServer.Registrar(), application)

	ready := func(ctx context.Context) error { return pool.Ping(ctx) }
	admin := observability.NewAdminServer(cfg.HTTP.Addr, cfg.Version, ready, log)

	log.Info("transaction starting", slog.String("env", cfg.Env), slog.String("grpc", cfg.GRPC.Addr))

	return run.Run(ctx, log, cfg.GRPC.ShutdownTimeout,
		run.Component{Name: "telemetry", Start: run.Noop, Stop: shutdownTelemetry},
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
