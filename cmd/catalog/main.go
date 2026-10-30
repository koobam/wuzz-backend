// Command catalog owns outlets and the laundry services they sell. It exposes
// only gRPC, plus a small admin surface for health and metrics.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/events"
	"github.com/koobam/wuzz-backend/internal/pkg/grpcx"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
	"github.com/koobam/wuzz-backend/internal/pkg/outbox"
	"github.com/koobam/wuzz-backend/internal/pkg/run"
	"github.com/koobam/wuzz-backend/internal/pkg/search"
	"github.com/koobam/wuzz-backend/internal/platform/kafkax"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	catalogpg "github.com/koobam/wuzz-backend/internal/services/catalog/adapters/postgres"
	"github.com/koobam/wuzz-backend/internal/services/catalog/adapters/searchindex"
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

	store := catalogpg.NewStore(pool)
	client := search.New(cfg.Typesense.URL, cfg.Typesense.APIKey)
	ix := searchindex.New(client, log)
	if ix.Enabled() {
		// A service configured for search starts only with the index ready: every collection exists at the current schema and is filled.
		if err := client.WaitReady(ctx, 30*time.Second); err != nil {
			return fmt.Errorf("typesense is configured (TYPESENSE_URL) but not reachable: %w", err)
		}
		if err := ix.Ensure(ctx, store.Everything); err != nil {
			return fmt.Errorf("search index: %w", err)
		}
		store.WithIndex(ix, log)
	}
	application := app.New(store, catalogpg.NewDiscovery(pool))

	grpcServer := grpcx.NewServer(cfg, log, verifier)
	cataloggrpc.Register(grpcServer.Registrar(), application, cfg.Internal.APIKey)

	admin := observability.NewAdminServer(cfg.HTTP.Addr, cfg.Version, pool.Ping, log)

	log.Info("catalog starting", slog.String("env", cfg.Env), slog.String("grpc", cfg.GRPC.Addr))

	sweepCtx, stopSweep := context.WithCancel(ctx)
	defer stopSweep()
	reconcile := func(ctx context.Context) error {
		return ix.Reconcile(ctx, store.Everything)
	}

	return run.Run(ctx, log, cfg.GRPC.ShutdownTimeout,
		run.Component{Name: "search-reconcile",
			Start: func() error {
				if ix.Enabled() {
					search.Every(sweepCtx, cfg.Typesense.ReconcileInterval, log, "reconcile", reconcile)
				}
				return nil
			},
			Stop: func(context.Context) error { stopSweep(); return nil }},
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
