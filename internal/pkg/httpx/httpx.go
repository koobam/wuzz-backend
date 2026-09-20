// Package httpx is the gateway's HTTP layer: the gin engine, its middleware
// and the response shapes. Only the gateway imports it — other services
// speak gRPC and expose nothing but /healthz, /readyz and /metrics.
package httpx

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/status"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Envelope is the body shape for both success and failure, matching the
// mobile app's existing ApiResponse<T>/ErrorResponse<T> TypeScript types
// exactly (src/core/http/response.ts in apps/wuzz) — this is a fixed contract
// an already-shipped client parses, not a free design choice.
type Envelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	// On a validation failure this is a map of field name to error message
	// (ErrorResponse<T> in the client); otherwise the endpoint's data, or nil.
	Data any `json:"data"`
}

func OK(c *gin.Context, data any)      { respond(c, http.StatusOK, data) }
func Created(c *gin.Context, data any) { respond(c, http.StatusCreated, data) }

// Done answers an endpoint that has nothing to return.
func Done(c *gin.Context) { respond(c, http.StatusOK, nil) }

func respond(c *gin.Context, status int, data any) {
	c.JSON(status, Envelope{
		Success: true,
		Message: http.StatusText(status),
		Data:    data,
	})
}

// Fail records the error and stops the chain; ErrorHandler writes the
// response once every handler in the chain has run.
func Fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}

// Bind parses and validates a JSON body, turning a binding failure into a 400
// through Fail rather than a raw gin error.
func Bind[T any](c *gin.Context) (T, bool) {
	var body T
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, apperr.Invalid("The request body is invalid").WithCause(err))
		var zero T
		return zero, false
	}
	return body, true
}

// Upstream converts a gRPC error from a backend service into *apperr.Error,
// so a downstream NotFound reaches the client as a 404 rather than a blanket
// 500.
func Upstream(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return apperr.Internal("Something went wrong").WithCause(err)
	}
	return apperr.FromGRPCCode(st.Code(), st.Message()).WithCause(err)
}

// IntQuery reads a query parameter as an int, falling back when it is absent
// or unparseable.
func IntQuery(c *gin.Context, key string, fallback int) int {
	if v, err := strconv.Atoi(c.Query(key)); err == nil {
		return v
	}
	return fallback
}

const hstsMaxAge = 31536000

// SecurityHeaders sets the response headers that are the same on every route.
func SecurityHeaders() gin.HandlerFunc {
	value := fmt.Sprintf("max-age=%d; includeSubDomains", hstsMaxAge)
	return func(c *gin.Context) {
		if c.GetHeader("X-Forwarded-Proto") == "https" || c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", value)
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Next()
	}
}
