package command

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// Users is the persistence port for accounts. Adapters/postgres implements
// it; command handlers depend on this interface, never on pgx directly.
type Users interface {
	ByPhone(ctx context.Context, phone string) (domain.User, error)
	ByID(ctx context.Context, id string) (domain.User, error)
	ExistsUsername(ctx context.Context, username string) (bool, error)
	Create(ctx context.Context, u domain.User) (domain.User, error)
	SetStatus(ctx context.Context, id, status string) (domain.User, error)
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
