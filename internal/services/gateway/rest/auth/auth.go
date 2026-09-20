// Package auth is the gateway's REST surface for authentication: it accepts
// JSON in the client's existing shape, calls the user service over gRPC, and
// converts the response back — see dto.go for exactly which shape and why.
package auth

import (
	"github.com/gin-gonic/gin"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

var usernameTakenErr = apperr.Conflict("This username is already taken")

type Handler struct {
	User userv1.UserServiceClient
}

func (h *Handler) Register(c *gin.Context) {
	body, ok := httpx.Bind[RegisterRequestDTO](c)
	if !ok {
		return
	}

	res, err := h.User.Register(c.Request.Context(), &userv1.RegisterRequest{
		Username:    body.Username,
		Name:        body.Name,
		Phone:       body.Phone,
		Email:       body.Email,
		DateOfBirth: body.DateOfBirth,
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}

	httpx.OK(c, RegisterResponseDTO{
		Phone:     res.GetPhone(),
		ExpiredAt: res.GetExpiresAt().AsTime().Format(timeLayout),
	})
}

func (h *Handler) VerifyOtp(c *gin.Context) {
	body, ok := httpx.Bind[VerifyOtpRequestDTO](c)
	if !ok {
		return
	}

	res, err := h.User.VerifyOtp(c.Request.Context(), &userv1.VerifyOtpRequest{
		Phone:   body.Phone,
		OtpCode: body.OTPCode,
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}

	httpx.OK(c, verifyOtpResponseFromProto(res.GetSession()))
}

func (h *Handler) SuggestUsername(c *gin.Context) {
	res, err := h.User.SuggestUsername(c.Request.Context(), &userv1.SuggestUsernameRequest{
		Name:      c.Query("name"),
		Username:  c.Query("username"),
		BirthDate: c.Query("birth_date"),
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, res.GetUsernames())
}

func (h *Handler) CheckUsername(c *gin.Context) {
	res, err := h.User.CheckUsername(c.Request.Context(), &userv1.CheckUsernameRequest{
		Username: c.Query("username"),
	})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	if !res.GetAvailable() {
		httpx.Fail(c, usernameTakenErr)
		return
	}
	httpx.Done(c)
}

// Me requires the caller to be authenticated (see routes.go, where the
// Authenticate(verifier, required=true) middleware runs first) — the bearer
// token it verified is already on c.Request.Context(), and
// authn.UnaryClientInterceptor forwards it to the user service, which
// verifies it again itself.
func (h *Handler) Me(c *gin.Context) {
	res, err := h.User.GetUser(c.Request.Context(), &userv1.GetUserRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, userDTOFromProto(res.GetUser()))
}

func (h *Handler) Refresh(c *gin.Context) {
	body, ok := httpx.Bind[RefreshRequestDTO](c)
	if !ok {
		return
	}

	res, err := h.User.Refresh(c.Request.Context(), &userv1.RefreshRequest{RefreshToken: body.RefreshToken})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}

	httpx.OK(c, RefreshResponseDTO{
		AccessToken:  res.GetSession().GetAccessToken(),
		RefreshToken: res.GetSession().GetRefreshToken(),
	})
}

func (h *Handler) Logout(c *gin.Context) {
	body, ok := httpx.Bind[RefreshRequestDTO](c)
	if !ok {
		return
	}
	if _, err := h.User.Logout(c.Request.Context(), &userv1.LogoutRequest{RefreshToken: body.RefreshToken}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Done(c)
}
