package grpcx

import (
	"context"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
)

type Server struct {
	server *grpc.Server
	addr   string
	log    *slog.Logger
}

// NewServer builds a gRPC server with the standard interceptor chain: token
// verification, then panic recovery, then error-kind mapping so an *apperr
// returned by a handler reaches the caller as the matching gRPC status code
// instead of an opaque Unknown.
func NewServer(cfg *config.Config, log *slog.Logger, verifier *authn.Verifier) *Server {
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			authn.UnaryServerInterceptor(verifier),
			recoveryInterceptor(log),
			errorInterceptor(),
		),
	)

	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	reflection.Register(server)

	return &Server{server: server, addr: cfg.GRPC.Addr, log: log}
}

// Registrar exposes the underlying *grpc.Server so a service's
// ports/grpc.Register(registrar, app) can attach itself without this package
// needing to know about any service's proto types.
func (s *Server) Registrar() grpc.ServiceRegistrar { return s.server }

func (s *Server) Start() error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.log.Info("grpc server listening", slog.String("addr", s.addr))
	return s.server.Serve(lis)
}

func (s *Server) Shutdown(ctx context.Context) error {
	stopped := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		s.server.Stop()
		return ctx.Err()
	}
}

func recoveryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.ErrorContext(ctx, "panic recovered", slog.Any("panic", r), slog.String("method", info.FullMethod))
				err = status.Error(apperr.Internal("Something went wrong").GRPCCode(), "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

// errorInterceptor converts an *apperr.Error returned by a handler into a
// gRPC status with the matching code, so the gateway's httpx.Upstream can map
// it straight back to an HTTP status.
func errorInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		appErr := apperr.From(err)
		return resp, status.Error(appErr.GRPCCode(), appErr.Message)
	}
}

// DialTimeout is how long service startup waits for an upstream connection
// attempt before moving on — gRPC dials lazily, so this mostly bounds how
// long a first request can stall.
const DialTimeout = 5 * time.Second
