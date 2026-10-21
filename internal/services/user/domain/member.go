package domain

// MemberLevel is where a customer stands on the level ladder.
type MemberLevel struct {
	Name        string
	NextLevel   string // "" at the top
	NextLevelXP int64  // 0 at the top
}

// MemberSummary is what the app shows on a customer's member card.
type MemberSummary struct {
	Name          string
	Level         *MemberLevel // nil when no levels are configured
	Points        int64
	XP            int64
	VoucherCount  int64
	WalletBalance int64
	FriendCount   int64
}
