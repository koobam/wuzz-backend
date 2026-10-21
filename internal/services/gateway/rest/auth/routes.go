package auth

import (
	"time"

	"github.com/gin-gonic/gin"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
)

// Register wires every auth-related route onto engine. Paths match the
// mobile client's existing apiUrl() calls exactly (apps/wuzz/src/core/api/auth):
// register and verify-otp live under /app, refresh does not.
func Register(engine *gin.Engine, userClient userv1.UserServiceClient, verifier *authn.Verifier, rdb *redisx.Client, devLogin bool) {
	h := &Handler{User: userClient}

	app := engine.Group("/app")
	app.POST("/register", httpx.RateLimit(rdb, "register", 5, time.Minute), h.Register)
	app.POST("/verify-otp", httpx.RateLimit(rdb, "verify-otp", 10, time.Minute), h.VerifyOtp)
	app.GET("/suggest-username", h.SuggestUsername)
	app.GET("/check-username", h.CheckUsername)
	app.GET("/me", httpx.Authenticate(verifier, true), h.Me)

	engine.POST("/refresh", h.Refresh)
	engine.POST("/logout", h.Logout)

	// The same customer-app API under a versioned prefix. The unversioned paths
	// above are the contract of the already-shipped mobile client and stay; new
	// clients should use /app/v1.
	v1 := engine.Group("/app/v1")
	v1.POST("/register", httpx.RateLimit(rdb, "register", 5, time.Minute), h.Register)
	v1.POST("/verify-otp", httpx.RateLimit(rdb, "verify-otp", 10, time.Minute), h.VerifyOtp)
	v1.GET("/suggest-username", h.SuggestUsername)
	v1.GET("/check-username", h.CheckUsername)
	v1.GET("/me", httpx.Authenticate(verifier, true), h.Me)
	v1.GET("/me/summary", httpx.Authenticate(verifier, true), h.MemberSummary)
	// Development only: the user service refuses it too unless it runs in dev.
	if devLogin {
		v1.POST("/dev/login", h.DevLogin)
	}
	v1.POST("/refresh", h.Refresh)
	v1.POST("/logout", h.Logout)
}
