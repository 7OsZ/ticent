package event

const (
	DefaultMaxActiveSession  = 100
	DefaultMaxTicketsPerUser = 4
	DefaultSessionMinutes    = 30
)

const HoldMinutes = 30

const (
	minActiveSession, maxActiveSession   = 1, 1000
	minTicketsPerUser, maxTicketsPerUser = 1, 10
	minSessionMinutes, maxSessionMinutes = 5, 120
)
