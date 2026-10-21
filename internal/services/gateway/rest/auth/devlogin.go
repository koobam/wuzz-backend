package auth

import (
	"github.com/gin-gonic/gin"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

type DevLoginRequestDTO struct {
	Phone string `json:"phone" binding:"required"`
}

// DevLogin: POST /app/v1/dev/login. Registered only when the gateway runs with
// APP_ENV=dev, and answered by the user service only when it does too.
func (h *Handler) DevLogin(c *gin.Context) {
	body, ok := httpx.Bind[DevLoginRequestDTO](c)
	if !ok {
		return
	}
	res, err := h.User.DevLogin(c.Request.Context(), &userv1.DevLoginRequest{Phone: body.Phone})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, verifyOtpResponseFromProto(res.GetSession()))
}
