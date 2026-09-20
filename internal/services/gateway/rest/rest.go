// Package rest assembles every REST route group the gateway serves.
package rest

import (
	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
	"github.com/koobam/wuzz-backend/internal/services/gateway/clients"
	"github.com/koobam/wuzz-backend/internal/services/gateway/rest/auth"
)

func Register(engine *gin.Engine, upstreams *clients.Clients, verifier *authn.Verifier, rdb *redisx.Client, cfg *config.Config) {
	auth.Register(engine, upstreams.User, verifier, rdb)
}
