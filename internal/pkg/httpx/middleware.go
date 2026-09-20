package httpx

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
)

const (
	HeaderRequestID = "X-Request-Id"
	ctxRequestID    = "request_id"
	ctxLogger       = "logger"
)

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(HeaderRequestID)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(ctxRequestID, id)
		c.Header(HeaderRequestID, id)
		c.Next()
	}
}

func Logger(base *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		log := base.With(slog.String("request_id", c.GetString(ctxRequestID)))
		c.Set(ctxLogger, log)

		c.Next()

		attrs := []any{
			slog.String("method", c.Request.Method),
			slog.String("route", routeOf(c)),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("ip", c.ClientIP()),
		}

		switch {
		case len(c.Errors) > 0:
			log.ErrorContext(c.Request.Context(), "request failed",
				append(attrs, slog.Any("error", c.Errors.Last().Err))...)
		case c.Writer.Status() >= http.StatusInternalServerError:
			log.ErrorContext(c.Request.Context(), "request failed", attrs...)
		default:
			log.InfoContext(c.Request.Context(), "request", attrs...)
		}
	}
}

func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		observability.HTTPInFlight.Inc()
		defer observability.HTTPInFlight.Dec()

		start := time.Now()
		c.Next()

		route := routeOf(c)
		if route == "" {
			route = "unmatched"
		}
		observability.HTTPDuration.WithLabelValues(c.Request.Method, route).Observe(time.Since(start).Seconds())
		observability.HTTPRequests.WithLabelValues(c.Request.Method, route, statusClass(c.Writer.Status())).Inc()
	}
}

func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.ErrorContext(c.Request.Context(), "panic recovered",
					slog.Any("panic", r), slog.String("path", c.Request.URL.Path))
				c.AbortWithStatusJSON(http.StatusInternalServerError, Envelope{
					Success: false,
					Message: "Something went wrong",
				})
			}
		}()
		c.Next()
	}
}

// ErrorHandler writes the response for anything a handler passed to Fail.
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}
		appErr := apperr.From(c.Errors.Last().Err)
		var data any
		if len(appErr.Details) > 0 {
			data = appErr.Details
		}
		c.JSON(appErr.HTTPStatus(), Envelope{
			Success: false,
			Message: appErr.Message,
			Data:    data,
		})
	}
}

// CORS allows exactly the configured origins.
func CORS(origins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[strings.TrimSpace(o)] = true
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowed[origin] {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+HeaderRequestID)
			h.Set("Access-Control-Expose-Headers", HeaderRequestID)
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
			h.Add("Vary", "Origin")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// Authenticate verifies the bearer token at the edge and stores it for
// propagation. Downstream services verify it again themselves — that is
// where the real security boundary is.
func Authenticate(verifier *authn.Verifier, required bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, found := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		token = strings.TrimSpace(token)

		if !found || token == "" {
			if required {
				Fail(c, apperr.Unauthorized("Authentication is required"))
				return
			}
			c.Next()
			return
		}

		id, err := verifier.Parse(token)
		if err != nil {
			if required {
				Fail(c, err)
				return
			}
			c.Next()
			return
		}

		ctx := authn.WithIdentity(c.Request.Context(), id)
		ctx = authn.WithToken(ctx, token)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// RateLimit is a fixed-window limiter in Redis, shared by every gateway
// replica.
func RateLimit(rdb *redisx.Client, bucket string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		slot := time.Now().Truncate(window).Unix()
		key := rdb.Key("ratelimit", bucket, c.ClientIP(), strconv.FormatInt(slot, 10))

		count, err := rdb.Incr(c.Request.Context(), key).Result()
		if err != nil {
			LoggerFrom(c).WarnContext(c.Request.Context(), "rate limiter unavailable", slog.Any("error", err))
			c.Next()
			return
		}
		if count == 1 {
			rdb.Expire(c.Request.Context(), key, window)
		}
		if count > int64(limit) {
			Fail(c, apperr.RateLimited("Too many requests, please try again shortly"))
			return
		}
		c.Next()
	}
}

func LoggerFrom(c *gin.Context) *slog.Logger {
	if v, ok := c.Get(ctxLogger); ok {
		if log, ok := v.(*slog.Logger); ok {
			return log
		}
	}
	return slog.Default()
}

func routeOf(c *gin.Context) string { return c.FullPath() }

func statusClass(status int) string {
	switch {
	case status < 200:
		return "1xx"
	case status < 300:
		return "2xx"
	case status < 400:
		return "3xx"
	case status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
