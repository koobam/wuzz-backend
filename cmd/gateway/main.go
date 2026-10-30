// Command gateway is the only public entry point: REST in, gRPC out.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
	"github.com/koobam/wuzz-backend/internal/pkg/run"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
	"github.com/koobam/wuzz-backend/internal/services/gateway/activity"
	"github.com/koobam/wuzz-backend/internal/services/gateway/clients"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest"
	"github.com/koobam/wuzz-backend/internal/services/gateway/waitlist"
)

var version = "dev"

func main() {
	if err := realMain(); err != nil {
		slog.Error("gateway failed to start", slog.Any("error", err))
		os.Exit(1)
	}
}

func realMain() error {
	ctx := context.Background()

	cfg, err := config.Load(config.ServiceGateway)
	if err != nil {
		return err
	}
	cfg.SetBuildVersion(version)

	log := observability.NewLogger(cfg.Telemetry.LogLevel, cfg.Telemetry.LogFormat, cfg.ServiceName, cfg.Version)
	shutdownTelemetry, err := observability.Setup(ctx, cfg)
	if err != nil {
		return err
	}

	rdb, err := redisx.New(ctx, cfg.Redis)
	if err != nil {
		return err
	}

	// The activity log lives in the gateway's own database. Failing to reach it stops the start (a misconfigured deploy must not run
	// unaudited); once running, a database that goes down never fails a request, it is logged and counted (see activity.Recorder).
	pool, err := postgres.New(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	store := &activity.Store{Pool: pool}
	recorder := activity.NewRecorder(store, log)

	upstreams, err := clients.Dial(ctx, cfg)
	if err != nil {
		return err
	}

	// The gateway verifies at the edge as a convenience; the user service
	// verifies again itself, which is where the real security boundary is.
	verifier, err := authn.NewVerifier(cfg.Auth)
	if err != nil {
		return err
	}

	ready := func(ctx context.Context) error { return rdb.Ping(ctx).Err() }

	engine := httpx.NewEngine(cfg, log, ready)
	rest.Register(engine, upstreams, verifier, rdb, cfg, &activity.Log{Recorder: recorder, Reader: store}, &waitlist.Store{Pool: pool})
	server := httpx.NewServer(cfg, engine, log)

	log.Info("gateway starting",
		slog.String("env", cfg.Env),
		slog.String("http", cfg.HTTP.Addr),
		slog.String("user", cfg.Upstreams.User))

	return run.Run(ctx, log, cfg.HTTP.ShutdownTimeout,
		run.Component{Name: "telemetry", Start: run.Noop, Stop: shutdownTelemetry},
		run.Component{Name: "upstream-clients", Start: run.Noop,
			Stop: func(context.Context) error { return upstreams.Close() }},
		run.Component{Name: "redis", Start: run.Noop,
			Stop: func(context.Context) error { return rdb.Close() }},
		// Components stop in reverse order, so this one, listed before http, stops after it: the last requests' entries are still
		// queued when it flushes.
		run.Component{Name: "activity-log", Start: run.Noop, Stop: func(ctx context.Context) error {
			err := recorder.Close(ctx)
			pool.Close()
			return err
		}},
		run.Component{Name: "http", Start: server.Start, Stop: server.Shutdown},
	)
}
