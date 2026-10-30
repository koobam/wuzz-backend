// Package rest assembles every REST route group the gateway serves.
//
// Routes are grouped by audience, each with its own version:
//
//	/app/v1         customers (mobile app); /app/* is the shipped, unversioned contract
//	/backoffice/v1  administrators
//	/pos/v1         outlet staff and administrators
//	/station/v1     Wuzzers (technician-drivers) and administrators
package rest

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
	"github.com/koobam/wuzz-backend/internal/services/gateway/activity"
	"github.com/koobam/wuzz-backend/internal/services/gateway/clients"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/auth"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/backoffice"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/catalogapi"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/docs"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/orders"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/presence"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/staff"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/uploads"
	"github.com/koobam/wuzz-backend/internal/services/gateway/waitlist"
)

// Register mounts every route. audit is the activity log: with it, the state-changing routes of /backoffice/v1 and /pos/v1 are logged
// and GET /backoffice/v1/activity reads the log back; nil leaves the log off (tests). leads is the landing page waitlist; nil leaves it off.
func Register(engine *gin.Engine, upstreams *clients.Clients, verifier *authn.Verifier, rdb *redisx.Client, cfg *config.Config, audit *activity.Log, leads waitlist.Repo) {
	auth.Register(engine, upstreams.User, verifier, rdb, cfg.Env == "dev")

	// Staging only. An allow-list rather than "not production": APP_ENV defaults to
	// dev, so a deploy that forgot to set it would otherwise publish the spec.
	if cfg.Env == "staging" {
		docs.Register(engine)
	}

	authed := httpx.Authenticate(verifier, true)
	catalog := &catalogapi.Handler{Catalog: upstreams.Catalog, User: upstreams.User, Tx: upstreams.Transaction}
	orderAPI := &orders.Handler{Tx: upstreams.Transaction}

	// Customer app: public catalog reads. Outlet discovery and service browsing
	// are served from MongoDB and exist only under /app/v1.
	appV1 := engine.Group("/app/v1")
	appV1.GET("/outlets", catalog.PublicOutlets)
	appV1.GET("/outlets/nearby", catalog.NearbyOutlets)
	appV1.GET("/coverage", catalog.Coverage)
	appV1.GET("/services", catalog.BrowseServices)
	appV1.GET("/services/:id", catalog.ServiceDetail)
	appV1.GET("/banners", catalog.LiveBanners)
	// A customer's wallet and claiming a code (the claim is rate limited: a code is a guessable secret).
	appV1.GET("/vouchers", authed, catalog.MyVouchers)
	appV1.POST("/vouchers/claim", authed, httpx.RateLimit(rdb, "voucher-claim", 10, time.Minute), catalog.ClaimVoucher)

	// The landing page's "tell me at launch" form. Open to anyone, so it is rate limited.
	if leads != nil {
		appV1.POST("/waitlist", httpx.RateLimit(rdb, "waitlist", 5, time.Minute), (&waitlist.Handler{Repo: leads}).Create)
	}

	bo := engine.Group("/backoffice/v1")
	if audit != nil {
		bo.Use(activity.Record(audit.Recorder, verifier))
	}
	staff.Register(bo, upstreams.User, "backoffice", httpx.IsAdmin, verifier, rdb)
	boAdmin := bo.Group("", authed, httpx.RequireIdentity(httpx.IsAdmin))
	users := &backoffice.Users{User: upstreams.User}
	boAdmin.GET("/users", users.List)
	boAdmin.PATCH("/users/:id/status", users.SetStatus)
	accounts := &backoffice.Accounts{User: upstreams.User}
	boAdmin.GET("/admins", accounts.ListAdmins())
	boAdmin.GET("/staff", accounts.ListStaff())
	// Pictures: uploaded here, stored in the media bucket (or, in dev, on disk and served by this gateway under /media).
	uploader, localMedia := uploads.New(cfg.Media, slog.Default())
	boAdmin.POST("/uploads", uploader.Create)
	if localMedia != nil {
		engine.GET("/media/*path", gin.WrapH(localMedia.Handler("/media")))
		engine.HEAD("/media/*path", gin.WrapH(localMedia.Handler("/media")))
	}
	boAdmin.GET("/outlets", catalog.BOListOutlets)
	boAdmin.POST("/outlets", catalog.BOCreateOutlet)
	boAdmin.PATCH("/outlets/:id", catalog.BOUpdateOutlet)
	boAdmin.DELETE("/outlets/:id", catalog.BODeleteOutlet)
	boAdmin.GET("/services", catalog.BOListServices)
	boAdmin.GET("/service-categories", catalog.BOListCategories)
	boAdmin.GET("/tax", catalog.TaxRates)
	boAdmin.POST("/services", catalog.BOCreateService)
	boAdmin.PATCH("/services/:id", catalog.BOUpdateService)
	boAdmin.DELETE("/services/:id", catalog.BODeleteService)
	boAdmin.GET("/variant-groups", catalog.BOListVariantGroups)
	boAdmin.POST("/variant-groups", catalog.BOCreateVariantGroup)
	boAdmin.GET("/variant-groups/:id", catalog.BOGetVariantGroup)
	boAdmin.PUT("/variant-groups/:id", catalog.BOUpdateVariantGroup)
	boAdmin.DELETE("/variant-groups/:id", catalog.BODeleteVariantGroup)
	boAdmin.GET("/modifier-groups", catalog.BOListModifierGroups)
	boAdmin.POST("/modifier-groups", catalog.BOCreateModifierGroup)
	boAdmin.GET("/modifier-groups/:id", catalog.BOGetModifierGroup)
	boAdmin.PUT("/modifier-groups/:id", catalog.BOUpdateModifierGroup)
	boAdmin.DELETE("/modifier-groups/:id", catalog.BODeleteModifierGroup)
	boAdmin.GET("/materials", catalog.BOListMaterials)
	boAdmin.POST("/materials", catalog.BOCreateMaterial)
	boAdmin.GET("/materials/:id", catalog.BOGetMaterial)
	boAdmin.PUT("/materials/:id", catalog.BOUpdateMaterial)
	boAdmin.DELETE("/materials/:id", catalog.BODeleteMaterial)
	boAdmin.GET("/banners", catalog.BOListBanners)
	boAdmin.POST("/banners", catalog.BOCreateBanner)
	boAdmin.GET("/banners/screens", catalog.BOListBannerScreens)
	boAdmin.POST("/banners/reorder", catalog.BOReorderBanners)
	boAdmin.GET("/banners/:id", catalog.BOGetBanner)
	boAdmin.PUT("/banners/:id", catalog.BOUpdateBanner)
	boAdmin.PATCH("/banners/:id", catalog.BOSetBannerStatus)
	boAdmin.DELETE("/banners/:id", catalog.BODeleteBanner)
	boAdmin.GET("/vouchers", catalog.BOListVouchers)
	boAdmin.POST("/vouchers", catalog.BOCreateVoucher)
	boAdmin.GET("/vouchers/colors", catalog.BOVoucherColors)
	boAdmin.GET("/vouchers/:id", catalog.BOGetVoucher)
	boAdmin.PUT("/vouchers/:id", catalog.BOUpdateVoucher)
	boAdmin.PATCH("/vouchers/:id", catalog.BOSetVoucherStatus)
	boAdmin.DELETE("/vouchers/:id", catalog.BODeleteVoucher)
	boAdmin.GET("/vouchers/:id/grants", catalog.BOVoucherGrants)
	boAdmin.POST("/vouchers/:id/inject", catalog.BOInjectVoucher)
	boAdmin.GET("/voucher-injections", catalog.BOListVoucherInjections)
	boAdmin.GET("/boms/:owner_type/:owner_id", catalog.BOGetBom)
	boAdmin.PUT("/boms/:owner_type/:owner_id", catalog.BOSetBom)
	boAdmin.GET("/services/:id/costing", catalog.BOGetServiceCosting)
	boAdmin.POST("/costing/calc", catalog.BOCalcCosting)
	boAdmin.GET("/stock", catalog.BOListStock())
	boAdmin.GET("/stock/movements", catalog.BOListMovements())
	boAdmin.POST("/stock/receipts", catalog.BOReceipt())
	boAdmin.POST("/stock/waste", catalog.BOWaste())
	boAdmin.POST("/stock/adjustments", catalog.BOAdjust)
	boAdmin.POST("/stock/transfers", catalog.BOTransfer)
	boAdmin.PUT("/stock/min", catalog.BOSetMin)
	if leads != nil {
		boAdmin.GET("/waitlist", (&waitlist.Handler{Repo: leads}).List)
	}
	boAdmin.GET("/orders", orderAPI.List)
	boAdmin.GET("/orders/summary", orderAPI.Summary)
	boAdmin.GET("/stats/orders", orderAPI.Stats)
	presenceAPI := &presence.Handler{Rdb: rdb}
	boAdmin.GET("/presence", presenceAPI.List)
	boAdmin.POST("/presence/ping", httpx.RateLimit(rdb, "presence-bo", 30, time.Minute), presenceAPI.Ping)
	if audit != nil {
		boAdmin.GET("/activity", (&activity.Handler{Log: audit.Reader}).List)
	}
	boAdmin.GET("/orders/:id", orderAPI.Get)
	boAdmin.PATCH("/orders/:id/status", orderAPI.SetStatus)

	pos := engine.Group("/pos/v1")
	if audit != nil {
		pos.Use(activity.Record(audit.Recorder, verifier))
	}
	staff.Register(pos, upstreams.User, "pos", httpx.IsOutletStaff, verifier, rdb)
	posStaff := pos.Group("", authed, httpx.RequireIdentity(httpx.IsOutletStaff), orders.PinOutlet())
	posStaff.GET("/catalog", catalog.POSCatalog)
	posStaff.GET("/tax", catalog.TaxRates)
	posStaff.GET("/stock", catalog.POSListStock())
	posStaff.GET("/stock/movements", catalog.POSListMovements())
	posStaff.POST("/stock/receipts", httpx.RateLimit(rdb, "pos-stock", 60, time.Minute), catalog.POSReceipt())
	posStaff.POST("/stock/waste", httpx.RateLimit(rdb, "pos-stock", 60, time.Minute), catalog.POSWaste())
	// Counts, transfers and reorder levels are head office's: the POS gets a plain 403.
	posStaff.POST("/stock/adjustments", catalogapi.HeadOfficeOnly)
	posStaff.POST("/stock/transfers", catalogapi.HeadOfficeOnly)
	posStaff.PUT("/stock/min", catalogapi.HeadOfficeOnly)
	posStaff.POST("/orders", orderAPI.Create)
	posStaff.GET("/orders", orderAPI.List)
	posStaff.POST("/presence/ping", httpx.RateLimit(rdb, "presence-pos", 30, time.Minute), presenceAPI.Ping)
	posStaff.GET("/orders/:id", orderAPI.Get)
	posStaff.PATCH("/orders/:id/status", orderAPI.SetStatus)

	// The station app: the Wuzzer who picks up and delivers. Only sign-in so far; the
	// jobs a Wuzzer works on come with the order lifecycle for pickup and delivery.
	station := engine.Group("/station/v1")
	staff.Register(station, upstreams.User, "station", httpx.IsWuzzer, verifier, rdb)
}
