package observability

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ReadinessFunc reports whether the service can currently serve traffic.
type ReadinessFunc func(ctx context.Context) error

// AdminServer is the small HTTP surface a gRPC-only service still exposes:
// /healthz, /readyz and /metrics. The gateway does not use this — its full
// gin engine already serves the same three routes.
type AdminServer struct {
	server *http.Server
	log    *slog.Logger
}

func NewAdminServer(addr, version string, ready ReadinessFunc, log *slog.Logger) *AdminServer {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","version":"` + version + `"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			if err := ready(ctx); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"unready"}`))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	mux.Handle("/metrics", promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry}))

	return &AdminServer{
		server: &http.Server{Addr: addr, Handler: mux},
		log:    log,
	}
}

func (s *AdminServer) Start() error {
	s.log.Info("admin http server listening", slog.String("addr", s.server.Addr))
	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *AdminServer) Shutdown(ctx context.Context) error { return s.server.Shutdown(ctx) }

// Setup would wire OpenTelemetry tracing/metrics export. Left a no-op for now
// — the Prometheus registry above already covers metrics, and there is no
// collector deployed yet to send traces to. The signature is kept so cmd/*
// does not need to change when this grows a real implementation.
func Setup(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}
