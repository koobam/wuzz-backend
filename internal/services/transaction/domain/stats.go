package domain

// StatsRange is the stretch of days a Stats call is about, in Asia/Jakarta's calendar. From and To (YYYY-MM-DD, both
// included) are given together; with neither, it is the last Days days, today included.
type StatsRange struct {
	From, To string
	Days     int
}

// MaxStatsDays is the longest range the dashboard can draw.
const MaxStatsDays = 92

// Stats is what the dashboard shows: orders by status, revenue and a row per day.
type Stats struct {
	ByStatus map[string]int64
	Revenue  int64
	Days     []DayStats
}

// DayStats is one day (YYYY-MM-DD, Asia/Jakarta) of orders.
type DayStats struct {
	Date                         string
	Orders, Fulfilled, Cancelled int64
}
