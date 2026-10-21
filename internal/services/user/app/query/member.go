package query

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// Members is the read port behind the member card.
type Members interface {
	Summary(ctx context.Context, userID string) (domain.MemberSummary, error)
}

type GetMemberSummary struct {
	UserID string
}

type GetMemberSummaryHandler struct{ members Members }

func NewGetMemberSummaryHandler(members Members) *GetMemberSummaryHandler {
	return &GetMemberSummaryHandler{members: members}
}

func (h *GetMemberSummaryHandler) Handle(ctx context.Context, q GetMemberSummary) (domain.MemberSummary, error) {
	if q.UserID == "" {
		return domain.MemberSummary{}, apperr.Unauthorized("Authentication is required")
	}
	return h.members.Summary(ctx, q.UserID)
}
