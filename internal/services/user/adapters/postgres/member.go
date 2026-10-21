package postgres

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

type Members struct{ pool *postgres.Pool }

func NewMembers(pool *postgres.Pool) *Members { return &Members{pool: pool} }

// Summary reads the whole member card in one round trip. A customer with no
// wallet row has a balance of zero; the level is the highest whose threshold
// the customer's xp has reached, and the next one is the lowest still above it.
func (m *Members) Summary(ctx context.Context, id string) (domain.MemberSummary, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return domain.MemberSummary{}, apperr.NotFound("User not found")
	}

	var (
		s         domain.MemberSummary
		level     *string
		nextLevel *string
		nextXP    *int64
	)
	err = m.pool.QueryRow(ctx, `
		SELECT u.name, u.points, u.xp,
		       COALESCE(w.balance, 0),
		       (SELECT count(*) FROM vouchers v
		         WHERE v.user_id = u.id AND v.used_at IS NULL AND v.expires_at > now()),
		       (SELECT count(*) FROM referrals r WHERE r.referrer_id = u.id),
		       cur.name, nxt.name, nxt.min_xp
		FROM users u
		LEFT JOIN wallets w ON w.user_id = u.id
		LEFT JOIN LATERAL (
		    SELECT name FROM member_levels WHERE min_xp <= u.xp ORDER BY min_xp DESC LIMIT 1
		) cur ON true
		LEFT JOIN LATERAL (
		    SELECT name, min_xp FROM member_levels WHERE min_xp > u.xp ORDER BY min_xp ASC LIMIT 1
		) nxt ON true
		WHERE u.id = $1`, n).
		Scan(&s.Name, &s.Points, &s.XP, &s.WalletBalance, &s.VoucherCount, &s.FriendCount, &level, &nextLevel, &nextXP)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MemberSummary{}, apperr.NotFound("User not found")
	}
	if err != nil {
		return domain.MemberSummary{}, apperr.Internal("Could not load the member summary").WithCause(err)
	}

	if level != nil {
		l := &domain.MemberLevel{Name: *level}
		if nextLevel != nil && nextXP != nil {
			l.NextLevel, l.NextLevelXP = *nextLevel, *nextXP
		}
		s.Level = l
	}
	return s, nil
}
