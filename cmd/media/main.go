// Command media owns the pictures people upload: it inspects and stores them in the media bucket (or, in dev, on disk) and keeps
// one row per picture in its own database. It exposes only gRPC, plus a small admin surface for health and metrics.
package main

import (
	"context"
	"log/slog"
	"os"

	"google.golang.org/grpc"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/grpcx"
	"github.com/koobam/wuzz-backend/internal/pkg/media"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
	"github.com/koobam/wuzz-backend/internal/pkg/run"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	mediapg "github.com/koobam/wuzz-backend/internal/services/media/adapters/postgres"
	"github.com/koobam/wuzz-backend/internal/services/media/app"
	mediagrpc "github.com/koobam/wuzz-backend/internal/services/media/ports/grpc"
)

var version = "dev"

func main() {
	if err := realMain(); err != nil {
		slog.Error("media failed to start", slog.Any("error", err))
		os.Exit(1)
	}
}

// messageOverhead is room for the fields around a picture's bytes in one gRPC message.
const messageOverhead = 64 << 10

func realMain() error {
	ctx := context.Background()

	cfg, err := config.Load(config.ServiceMedia)
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

	verifier, err := authn.NewVerifier(cfg.Auth)
	if err != nil {
		return err
	}

	store := storage(cfg.Media)
	if store == nil {
		log.Warn("media storage is not configured (MEDIA_LOCAL_DIR, or MEDIA_ENDPOINT and the bucket settings): uploads answer unavailable")
	}
	application := app.New(mediapg.NewStore(pool), store, cfg.Media.MaxUploadBytes, log)

	grpcServer := grpcx.NewServer(cfg, log, verifier, grpc.MaxRecvMsgSize(int(cfg.Media.MaxUploadBytes)+messageOverhead))
	mediagrpc.Register(grpcServer.Registrar(), application)

	admin := observability.NewAdminServer(cfg.HTTP.Addr, cfg.Version, pool.Ping, log)

	log.Info("media starting", slog.String("env", cfg.Env), slog.String("grpc", cfg.GRPC.Addr))

	return run.Run(ctx, log, cfg.GRPC.ShutdownTimeout,
		run.Component{Name: "telemetry", Start: run.Noop, Stop: shutdownTelemetry},
		run.Component{Name: "postgres", Start: run.Noop,
			Stop: func(context.Context) error { pool.Close(); return nil }},
		run.Component{Name: "admin-http", Start: admin.Start, Stop: admin.Shutdown},
		run.Component{Name: "grpc", Start: grpcServer.Start, Stop: grpcServer.Shutdown},
	)
}

// storage is where pictures go: the dev disk, the bucket, or nil when neither is set.
func storage(cfg config.Media) media.Storage {
	switch {
	case cfg.LocalDir != "":
		return &media.Local{Dir: cfg.LocalDir, PublicBaseURL: cfg.PublicBaseURL}
	case cfg.Configured():
		return &media.S3{Endpoint: cfg.Endpoint, Bucket: cfg.Bucket, AccessKeyID: cfg.AccessKeyID, SecretAccessKey: cfg.SecretAccessKey,
			Region: cfg.Region, PublicBaseURL: cfg.PublicBaseURL}
	}
	return nil
}
