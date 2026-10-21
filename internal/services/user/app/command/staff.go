package command

import (
	"context"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// ---------- SendLoginCode ----------

// SendLoginCode starts sign-in for an existing staff account. It answers the
// same whether or not the number belongs to staff: a different answer would let
// anyone probe which phone numbers are back-office accounts.
type SendLoginCode struct{ Phone string }

type SendLoginCodeHandler struct {
	users         Users
	verifications Verifications
	codeTTL       time.Duration
}

func NewSendLoginCodeHandler(users Users, verifications Verifications, codeTTL time.Duration) *SendLoginCodeHandler {
	return &SendLoginCodeHandler{users: users, verifications: verifications, codeTTL: codeTTL}
}

func (h *SendLoginCodeHandler) Handle(ctx context.Context, cmd SendLoginCode) (time.Time, error) {
	phone, err := domain.NormalisePhone(cmd.Phone)
	if err != nil {
		return time.Time{}, err
	}
	expires := time.Now().Add(h.codeTTL)

	user, err := h.users.ByPhone(ctx, phone)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return expires, nil
		}
		return time.Time{}, err
	}
	if !user.IsStaff() || !user.CanSignIn() {
		return expires, nil
	}
	if err := h.verifications.Start(ctx, phone); err != nil {
		return time.Time{}, err
	}
	return expires, nil
}

// ---------- SetUserStatus ----------

type SetUserStatus struct{ UserID, Status string }

type SetUserStatusHandler struct{ users Users }

func NewSetUserStatusHandler(users Users) *SetUserStatusHandler {
	return &SetUserStatusHandler{users: users}
}

func (h *SetUserStatusHandler) Handle(ctx context.Context, cmd SetUserStatus) (domain.User, error) {
	if !domain.ValidStatus(cmd.Status) {
		return domain.User{}, apperr.Invalid("status must be active, inactive or banned")
	}
	return h.users.SetStatus(ctx, cmd.UserID, cmd.Status)
}

// ---------- ListUsers ----------

type ListUsers struct {
	Query, Status string
	Limit, Offset int
}

type ListUsersHandler struct{ users Users }

func NewListUsersHandler(users Users) *ListUsersHandler { return &ListUsersHandler{users: users} }

func (h *ListUsersHandler) Handle(ctx context.Context, q ListUsers) ([]domain.User, int64, error) {
	if q.Status != "" && !domain.ValidStatus(q.Status) {
		return nil, 0, apperr.Invalid("status must be active, inactive or banned")
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 25
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return h.users.List(ctx, ListFilter{Query: q.Query, Status: q.Status, Limit: q.Limit, Offset: q.Offset})
}
