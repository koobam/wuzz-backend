package auth

import (
	"strconv"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
)

// These DTOs match apps/wuzz/src/core/api/auth/*.types.ts field-for-field —
// that TypeScript is a shipped, unchangeable contract, not a style choice
// made here. Two different naming conventions exist side by side because the
// client already does: /app/* is snake_case, /refresh is camelCase (see
// RefreshDataDTO) — both are copied as-is, not reconciled.

// UserDTO matches user.types.ts's User (returned by GET /app/me) — note `id`
// is a JSON number there, so ID is parsed back from the domain's string form.
type UserDTO struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Name        string `json:"name"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
	Streak      int64  `json:"streak"`
	Point       int64  `json:"point"`
	XP          int64  `json:"xp"`
	DateOfBirth string `json:"date_of_birth"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func userDTOFromProto(u *userv1.User) UserDTO {
	id, _ := strconv.ParseInt(u.GetId(), 10, 64)
	dto := UserDTO{
		ID:          id,
		Username:    u.GetUsername(),
		Name:        u.GetName(),
		Phone:       u.GetPhone(),
		Email:       u.GetEmail(),
		Streak:      u.GetStreak(),
		Point:       u.GetPoints(),
		XP:          u.GetXp(),
		DateOfBirth: u.GetDateOfBirth(),
	}
	if t := u.GetCreatedAt(); t != nil {
		dto.CreatedAt = t.AsTime().Format(timeLayout)
	}
	if t := u.GetUpdatedAt(); t != nil {
		dto.UpdatedAt = t.AsTime().Format(timeLayout)
	}
	return dto
}

const timeLayout = "2006-01-02T15:04:05.000Z"

// RegisterRequestDTO matches register.types.ts's RegisterBodyParams.
type RegisterRequestDTO struct {
	Username    string `json:"username" binding:"required"`
	Name        string `json:"name" binding:"required"`
	DateOfBirth string `json:"date_of_birth"`
	Phone       string `json:"phone" binding:"required"`
	Email       string `json:"email"`
}

// RegisterResponseDTO matches register.types.ts's RegisterDataResponse.
// "expired_at", not "expires_at" — that inconsistency with VerifyOtp's field
// is already shipped in the client and not this handler's to fix.
type RegisterResponseDTO struct {
	Phone     string `json:"phone"`
	ExpiredAt string `json:"expired_at"`
}

// VerifyOtpRequestDTO matches verify-otp.types.ts's VerifyOtpBodyParams.
type VerifyOtpRequestDTO struct {
	Phone   string `json:"phone" binding:"required"`
	OTPCode string `json:"otp_code" binding:"required"`
}

// VerifyOtpResponseDTO matches verify-otp.types.ts's VerifyOtpDataResponse.
type VerifyOtpResponseDTO struct {
	User         VerifyOtpUserDTO `json:"user"`
	AccessToken  string           `json:"access_token"`
	RefreshToken string           `json:"refresh_token"`
	ExpiresAt    string           `json:"expires_at"`
}

// VerifyOtpUserDTO matches verify-otp.types.ts's VerifyOtpUser — a smaller,
// separately-declared shape from UserDTO in the same client codebase.
type VerifyOtpUserDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Username string `json:"username,omitempty"`
}

func verifyOtpResponseFromProto(s *userv1.Session) VerifyOtpResponseDTO {
	u := s.GetUser()
	return VerifyOtpResponseDTO{
		User: VerifyOtpUserDTO{
			ID:       u.GetId(),
			Name:     u.GetName(),
			Email:    u.GetEmail(),
			Phone:    u.GetPhone(),
			Username: u.GetUsername(),
		},
		AccessToken:  s.GetAccessToken(),
		RefreshToken: s.GetRefreshToken(),
		ExpiresAt:    s.GetAccessTokenExpiresAt().AsTime().Format(timeLayout),
	}
}

// RefreshRequestDTO matches the body doRefreshTokens in
// libs/api/src/request.ts actually sends: camelCase, unlike every /app/*
// endpoint.
type RefreshRequestDTO struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

// RefreshResponseDTO matches request.ts's TokenData — just the two tokens,
// camelCase, no user and no expiry (unlike VerifyOtpResponseDTO).
type RefreshResponseDTO struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}
