// Package staff is the sign-in API shared by the back-office and the POS:
// phone + one-time code for an existing staff account. Mounted once per
// audience, so a customer's phone number gets a 403, not a token, when it asks
// for a staff audience.
package staff

import (
	"time"

	"github.com/gin-gonic/gin"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/platform/redisx"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

type Handler struct {
	User     userv1.UserServiceClient
	Audience string
}

type sendCodeRequest struct {
	Phone string `json:"phone" binding:"required"`
}

type verifyRequest struct {
	Phone   string `json:"phone" binding:"required"`
	OTPCode string `json:"otp_code" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type userDTO struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Phone string   `json:"phone"`
	Roles []string `json:"roles"`
}

type sessionDTO struct {
	User         userDTO `json:"user"`
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresAt    string  `json:"expires_at"`
}

func rolesOrEmpty(r []string) []string {
	if r == nil {
		return []string{}
	}
	return r
}

func sessionFromProto(s *userv1.Session) sessionDTO {
	u := s.GetUser()
	return sessionDTO{
		User:         userDTO{ID: u.GetId(), Name: u.GetName(), Phone: u.GetPhone(), Roles: rolesOrEmpty(u.GetRoles())},
		AccessToken:  s.GetAccessToken(),
		RefreshToken: s.GetRefreshToken(),
		ExpiresAt:    s.GetAccessTokenExpiresAt().AsTime().Format(timeLayout),
	}
}

// Register mounts /auth/* onto group. allow is the audience's role check, used
// for /me so a stale customer token cannot read staff identity.
func Register(group *gin.RouterGroup, userClient userv1.UserServiceClient, audience string, allow func(authn.Identity) bool, verifier *authn.Verifier, rdb *redisx.Client) {
	h := &Handler{User: userClient, Audience: audience}
	g := group.Group("/auth")
	g.POST("/send-code", httpx.RateLimit(rdb, "staff-send-code-"+audience, 5, time.Minute), h.SendCode)
	g.POST("/verify-otp", httpx.RateLimit(rdb, "staff-verify-"+audience, 10, time.Minute), h.VerifyOtp)
	g.POST("/refresh", h.Refresh)
	g.POST("/logout", h.Logout)
	g.GET("/me", httpx.Authenticate(verifier, true), httpx.RequireIdentity(allow), h.Me)
}

func (h *Handler) SendCode(c *gin.Context) {
	body, ok := httpx.Bind[sendCodeRequest](c)
	if !ok {
		return
	}
	res, err := h.User.SendLoginCode(c.Request.Context(), &userv1.SendLoginCodeRequest{Phone: body.Phone})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"expires_at": res.GetExpiresAt().AsTime().Format(timeLayout)})
}

func (h *Handler) VerifyOtp(c *gin.Context) {
	body, ok := httpx.Bind[verifyRequest](c)
	if !ok {
		return
	}
	res, err := h.User.VerifyOtp(c.Request.Context(), &userv1.VerifyOtpRequest{
		Phone: body.Phone, OtpCode: body.OTPCode, Audience: h.Audience,
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, sessionFromProto(res.GetSession()))
}

func (h *Handler) Refresh(c *gin.Context) {
	body, ok := httpx.Bind[refreshRequest](c)
	if !ok {
		return
	}
	res, err := h.User.Refresh(c.Request.Context(), &userv1.RefreshRequest{RefreshToken: body.RefreshToken})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, sessionFromProto(res.GetSession()))
}

func (h *Handler) Logout(c *gin.Context) {
	body, ok := httpx.Bind[refreshRequest](c)
	if !ok {
		return
	}
	if _, err := h.User.Logout(c.Request.Context(), &userv1.LogoutRequest{RefreshToken: body.RefreshToken}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Done(c)
}

func (h *Handler) Me(c *gin.Context) {
	res, err := h.User.GetUser(c.Request.Context(), &userv1.GetUserRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	u := res.GetUser()
	httpx.OK(c, userDTO{ID: u.GetId(), Name: u.GetName(), Phone: u.GetPhone(), Roles: rolesOrEmpty(u.GetRoles())})
}
