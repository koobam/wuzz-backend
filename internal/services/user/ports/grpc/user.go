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

		Audience: req.GetAudience(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.VerifyOtpResponse{Session: toProtoSession(session)}, nil
}

func (s *Server) DevLogin(ctx context.Context, req *userv1.DevLoginRequest) (*userv1.DevLoginResponse, error) {
	if s.app.Commands.DevLogin == nil {
		// Indistinguishable from a method that does not exist.
		return nil, apperr.NotFound("Not found")
	}
	session, err := s.app.Commands.DevLogin.Handle(ctx, command.DevLogin{Phone: req.GetPhone()})
	if err != nil {
		return nil, err
	}
	return &userv1.DevLoginResponse{Session: toProtoSession(session)}, nil
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
	profile, err := s.app.Queries.GetUser.Handle(ctx, query.GetUser{UserID: identity.UserID})
	if err != nil {
		return nil, err
	}
	if profile.Account != nil {
		return &userv1.GetUserResponse{User: toProtoAccount(*profile.Account)}, nil
	}
	return &userv1.GetUserResponse{User: toProtoUser(profile.User)}, nil
}

func (s *Server) GetMemberSummary(ctx context.Context, _ *userv1.GetMemberSummaryRequest) (*userv1.GetMemberSummaryResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.app.Queries.MemberSummary.Handle(ctx, query.GetMemberSummary{UserID: identity.UserID})
	if err != nil {
		return nil, err
	}
	out := &userv1.MemberSummary{
		Name: m.Name, Points: m.Points, Xp: m.XP, VoucherCount: m.VoucherCount, WalletBalance: m.WalletBalance, FriendCount: m.FriendCount,
	}
	if m.Level != nil {
		out.Level = &userv1.MemberLevel{Name: m.Level.Name, NextLevel: m.Level.NextLevel, NextLevelXp: m.Level.NextLevelXP}
	}
	return &userv1.GetMemberSummaryResponse{Summary: out}, nil
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

// toProtoAccount shows an admin or staff member as a User, with the roles their token carries.
func toProtoAccount(a domain.Account) *userv1.User {
	return &userv1.User{
		Id:        a.ID,
		Name:      a.Name,
		Phone:     a.Phone,
		Status:    a.Status,
		Roles:     a.Roles(),
		CreatedAt: timestamppb.New(a.CreatedAt),
		UpdatedAt: timestamppb.New(a.UpdatedAt),
	}
}

func toProtoSession(s command.Session) *userv1.Session {
	user := toProtoUser(s.User)
	if s.Account != nil {
		user = toProtoAccount(*s.Account)
	}
	return &userv1.Session{
		AccessToken:          s.AccessToken,
		RefreshToken:         s.RefreshToken,
		User:                 user,
		AccessTokenExpiresAt: timestamppb.New(s.AccessTokenExpiresAt),
	}
}

func (s *Server) SendLoginCode(ctx context.Context, req *userv1.SendLoginCodeRequest) (*userv1.SendLoginCodeResponse, error) {
	expires, err := s.app.Commands.SendLoginCode.Handle(ctx, command.SendLoginCode{Phone: req.GetPhone()})
	if err != nil {
		return nil, err
	}
	return &userv1.SendLoginCodeResponse{ExpiresAt: timestamppb.New(expires)}, nil
}

// requireAdmin gates the account-management RPCs on the `admin` role in the
// verified token — the role is the credential, not anything on the request.
func requireAdmin(ctx context.Context) error {
	identity, err := authn.Require(ctx)
	if err != nil {
		return err
	}
	if !identity.HasRole("admin") {
		return apperr.Forbidden("Administrator access is required")
	}
	return nil
}

func (s *Server) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.ListUsersResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListUsers.Handle(ctx, query.ListUsers{
		Query: req.GetQuery(), Status: req.GetStatus(), SortBy: req.GetSortBy(), SortDir: req.GetSortDir(),
		Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*userv1.User, 0, len(res.Users))
	for _, u := range res.Users {
		out = append(out, toProtoUser(u))
	}
	return &userv1.ListUsersResponse{Users: out, Total: res.Total}, nil
}

// ResolveCustomers finds customers by id or phone number for the back office. It is admin-only: it answers with names and numbers.
func (s *Server) ResolveCustomers(ctx context.Context, req *userv1.ResolveCustomersRequest) (*userv1.ResolveCustomersResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ResolveCustomers.Handle(ctx, query.ResolveCustomers{IDs: req.GetIds(), Phones: req.GetPhones()})
	if err != nil {
		return nil, err
	}
	out := make([]*userv1.User, 0, len(res.Users))
	for _, u := range res.Users {
		out = append(out, toProtoUser(u))
	}
	return &userv1.ResolveCustomersResponse{Users: out, Unmatched: res.Unmatched}, nil
}

func (s *Server) ListAccounts(ctx context.Context, req *userv1.ListAccountsRequest) (*userv1.ListAccountsResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.app.Queries.ListAccounts.Handle(ctx, query.ListAccounts{
		Kind: req.GetKind(), Query: req.GetQuery(), Status: req.GetStatus(), OutletID: req.GetOutletId(),
		SortBy: req.GetSortBy(), SortDir: req.GetSortDir(),
		Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*userv1.Account, 0, len(res.Accounts))
	for _, a := range res.Accounts {
		out = append(out, &userv1.Account{
			Id: a.ID, Kind: a.Kind, Name: a.Name, Phone: a.Phone, Role: a.Role, OutletId: a.OutletID, Status: a.Status,
			CreatedAt: timestamppb.New(a.CreatedAt), UpdatedAt: timestamppb.New(a.UpdatedAt),
		})
	}
	return &userv1.ListAccountsResponse{Accounts: out, Total: res.Total}, nil
}

func (s *Server) SetUserStatus(ctx context.Context, req *userv1.SetUserStatusRequest) (*userv1.SetUserStatusResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	u, err := s.app.Commands.SetUserStatus.Handle(ctx, command.SetUserStatus{UserID: req.GetUserId(), Status: req.GetStatus()})
	if err != nil {
		return nil, err
	}
	return &userv1.SetUserStatusResponse{User: toProtoUser(u)}, nil
}
