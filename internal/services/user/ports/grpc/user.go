package grpc

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/user/app/command"
	"github.com/koobam/wuzz-backend/internal/services/user/app/query"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

func (s *Server) Register(ctx context.Context, req *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	var birthdate *time.Time
	if raw := req.GetDateOfBirth(); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return nil, apperr.Invalid("date_of_birth must be YYYY-MM-DD")
		}
		birthdate = &parsed
	}

	result, err := s.app.Commands.Register.Handle(ctx, command.Register{
		Username:  req.GetUsername(),
		Name:      req.GetName(),
		Phone:     req.GetPhone(),
		Email:     req.GetEmail(),
		Birthdate: birthdate,
	})
	if err != nil {
		return nil, err
	}
	return &userv1.RegisterResponse{
		Phone:     result.Phone,
		ExpiresAt: timestamppb.New(result.ExpiresAt),
	}, nil
}

func (s *Server) VerifyOtp(ctx context.Context, req *userv1.VerifyOtpRequest) (*userv1.VerifyOtpResponse, error) {
	session, err := s.app.Commands.VerifyOtp.Handle(ctx, command.VerifyOtp{
		Phone: req.GetPhone(),
		Code:  req.GetOtpCode(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.VerifyOtpResponse{Session: toProtoSession(session)}, nil
}

func (s *Server) SuggestUsername(ctx context.Context, req *userv1.SuggestUsernameRequest) (*userv1.SuggestUsernameResponse, error) {
	usernames, err := s.app.Queries.SuggestUsername.Handle(ctx, query.SuggestUsername{
		Name:     req.GetName(),
		Username: req.GetUsername(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.SuggestUsernameResponse{Usernames: usernames}, nil
}

func (s *Server) CheckUsername(ctx context.Context, req *userv1.CheckUsernameRequest) (*userv1.CheckUsernameResponse, error) {
	available, err := s.app.Queries.CheckUsername.Handle(ctx, query.CheckUsername{Username: req.GetUsername()})
	if err != nil {
		return nil, err
	}
	return &userv1.CheckUsernameResponse{Available: available}, nil
}

func (s *Server) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	// The caller is whoever the access token names, not a field on the
	// request — see the interceptor chain in internal/pkg/authn/grpc.go.
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.app.Queries.GetUser.Handle(ctx, query.GetUser{UserID: identity.UserID})
	if err != nil {
		return nil, err
	}
	return &userv1.GetUserResponse{User: toProtoUser(user)}, nil
}

func (s *Server) Refresh(ctx context.Context, req *userv1.RefreshRequest) (*userv1.RefreshResponse, error) {
	session, err := s.app.Commands.Refresh.Handle(ctx, command.Refresh{RefreshToken: req.GetRefreshToken()})
	if err != nil {
		return nil, err
	}
	return &userv1.RefreshResponse{Session: toProtoSession(session)}, nil
}

func (s *Server) Logout(ctx context.Context, req *userv1.LogoutRequest) (*userv1.LogoutResponse, error) {
	if _, err := s.app.Commands.Logout.Handle(ctx, command.Logout{RefreshToken: req.GetRefreshToken()}); err != nil {
		return nil, err
	}
	return &userv1.LogoutResponse{}, nil
}

func toProtoUser(u domain.User) *userv1.User {
	out := &userv1.User{
		Id:        u.ID,
		Username:  u.Username,
		Name:      u.Name,
		Phone:     u.Phone,
		Email:     u.Email,
		Points:    u.Points,
		Streak:    u.Streak,
		Xp:        u.XP,
		Status:    u.Status,
		CreatedAt: timestamppb.New(u.CreatedAt),
		UpdatedAt: timestamppb.New(u.UpdatedAt),
	}
	if u.Birthdate != nil {
		out.DateOfBirth = u.Birthdate.Format("2006-01-02")
	}
	return out
}

func toProtoSession(s command.Session) *userv1.Session {
	return &userv1.Session{
		AccessToken:          s.AccessToken,
		RefreshToken:         s.RefreshToken,
		User:                 toProtoUser(s.User),
		AccessTokenExpiresAt: timestamppb.New(s.AccessTokenExpiresAt),
	}
}
