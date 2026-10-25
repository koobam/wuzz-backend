package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
)

// NewEngine builds the gateway's gin engine.
//
// Middleware order matters: RequestID first so everything downstream logs it,
// Recovery before user code, ErrorHandler last so it sees what handlers set.
func NewEngine(cfg *config.Config, log *slog.Logger, ready observability.ReadinessFunc) *gin.Engine {
	if cfg.Env != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	if err := engine.SetTrustedProxies(cfg.HTTP.TrustedProxies); err != nil {
		log.Warn("failed to set trusted proxies", slog.Any("error", err))
	}

	engine.Use(
		// First, so the span covers everything below and Logger sees its ids.
		// Probes and scrapes would otherwise be most of the traces.
		otelgin.Middleware(cfg.ServiceName, otelgin.WithFilter(func(r *http.Request) bool {
			switch r.URL.Path {
			case "/healthz", "/readyz", "/metrics":
				return false
			}
			return true
		})),
		RequestID(),
		Logger(log),
		Metrics(),
		Recovery(log),
		CORS(cfg.HTTP.CORSOrigins),
		SecurityHeaders(),
		ErrorHandler(),
	)

	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": cfg.Version})
	})

	engine.GET("/readyz", func(c *gin.Context) {
		if ready != nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
			defer cancel()
			if err := ready(ctx); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unready", "reason": err.Error()})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	engine.GET("/metrics", gin.WrapH(promhttp.HandlerFor(
		observability.Registry, promhttp.HandlerOpts{Registry: observability.Registry})))

	return engine
}

type Server struct {
	server *http.Server
	log    *slog.Logger
}

func NewServer(cfg *config.Config, engine *gin.Engine, log *slog.Logger) *Server {
	return &Server{
		server: &http.Server{
			Addr:         cfg.HTTP.Addr,
			Handler:      engine,
			ReadTimeout:  cfg.HTTP.ReadTimeout,
			WriteTimeout: cfg.HTTP.WriteTimeout,
		},
		log: log,
	}
}

func (s *Server) Start() error {
	s.log.Info("http server listening", slog.String("addr", s.server.Addr))
	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error { return s.server.Shutdown(ctx) }
