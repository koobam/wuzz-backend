// Package rest assembles every REST route group the gateway serves.
//
// Routes are grouped by audience, each with its own version:
//
//	/app/v1         customers (mobile app); /app/* is the shipped, unversioned contract
//	/backoffice/v1  administrators
//	/pos/v1         outlet staff and administrators
package rest

import (
	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
	"github.com/koobam/wuzz-backend/internal/services/gateway/clients"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/auth"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/backoffice"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/catalogapi"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/docs"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/orders"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/staff"
)

func Register(engine *gin.Engine, upstreams *clients.Clients, verifier *authn.Verifier, rdb *redisx.Client, cfg *config.Config) {
	auth.Register(engine, upstreams.User, verifier, rdb, cfg.Env == "dev")

	// Staging only. An allow-list rather than "not production": APP_ENV defaults to
	// dev, so a deploy that forgot to set it would otherwise publish the spec.
	if cfg.Env == "staging" {
		docs.Register(engine)
	}

	authed := httpx.Authenticate(verifier, true)
	catalog := &catalogapi.Handler{Catalog: upstreams.Catalog}
	orderAPI := &orders.Handler{Tx: upstreams.Transaction}

	// Customer app: public catalog reads. Outlet discovery and service browsing
	// are served from MongoDB and exist only under /app/v1.
	appV1 := engine.Group("/app/v1")
	appV1.GET("/outlets", catalog.PublicOutlets)
	appV1.GET("/outlets/nearby", catalog.NearbyOutlets)
	appV1.GET("/coverage", catalog.Coverage)
	appV1.GET("/services", catalog.BrowseServices)
	appV1.GET("/services/:id", catalog.ServiceDetail)

	bo := engine.Group("/backoffice/v1")
	staff.Register(bo, upstreams.User, "backoffice", httpx.IsAdmin, verifier, rdb)
	boAdmin := bo.Group("", authed, httpx.RequireIdentity(httpx.IsAdmin))
	users := &backoffice.Users{User: upstreams.User}
	boAdmin.GET("/users", users.List)
	boAdmin.PATCH("/users/:id/status", users.SetStatus)
	boAdmin.GET("/outlets", catalog.BOListOutlets)
	boAdmin.POST("/outlets", catalog.BOCreateOutlet)
	boAdmin.PATCH("/outlets/:id", catalog.BOUpdateOutlet)
	boAdmin.GET("/services", catalog.BOListServices)
	boAdmin.POST("/services", catalog.BOCreateService)
	boAdmin.PATCH("/services/:id", catalog.BOUpdateService)
	boAdmin.GET("/orders", orderAPI.List)
	boAdmin.GET("/orders/:id", orderAPI.Get)
	boAdmin.PATCH("/orders/:id/status", orderAPI.SetStatus)

	pos := engine.Group("/pos/v1")
	staff.Register(pos, upstreams.User, "pos", httpx.IsOutletStaff, verifier, rdb)
	posStaff := pos.Group("", authed, httpx.RequireIdentity(httpx.IsOutletStaff))
	posStaff.GET("/catalog", catalog.POSCatalog)
	posStaff.POST("/orders", orderAPI.Create)
	posStaff.GET("/orders", orderAPI.List)
	posStaff.GET("/orders/:id", orderAPI.Get)
	posStaff.PATCH("/orders/:id/status", orderAPI.SetStatus)
}
