package auth

import (
	"github.com/gin-gonic/gin"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

type memberLevelDTO struct {
	Name        string `json:"name"`
	NextLevel   string `json:"next_level,omitempty"`
	NextLevelXP int64  `json:"next_level_xp,omitempty"`
}

// MemberSummaryDTO is the member card: level, points, usable vouchers, wallet.
type MemberSummaryDTO struct {
	Name          string          `json:"name"`
	Level         *memberLevelDTO `json:"level"`
	Points        int64           `json:"points"`
	XP            int64           `json:"xp"`
	VoucherCount  int64           `json:"voucher_count"`
	WalletBalance int64           `json:"wallet_balance"`
	FriendCount   int64           `json:"friend_count"`
}

// MemberSummary: GET /app/v1/me/summary (authenticated).
func (h *Handler) MemberSummary(c *gin.Context) {
	res, err := h.User.GetMemberSummary(c.Request.Context(), &userv1.GetMemberSummaryRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	s := res.GetSummary()
	out := MemberSummaryDTO{
		Name: s.GetName(), Points: s.GetPoints(), XP: s.GetXp(),
		VoucherCount: s.GetVoucherCount(), WalletBalance: s.GetWalletBalance(), FriendCount: s.GetFriendCount(),
	}
	if l := s.GetLevel(); l != nil {
		out.Level = &memberLevelDTO{Name: l.GetName(), NextLevel: l.GetNextLevel(), NextLevelXP: l.GetNextLevelXp()}
	}
	httpx.OK(c, out)
}
