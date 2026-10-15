// SPDX-License-Identifier: MIT

package user

// Stat is a statistic of the users that can be ranked
// (roadmap 5.2.5). DonatedCents (phase 9) and Strikes (5.5) have no
// ranking yet.
type Stat string

// The statistics with a ranking.
const (
	// StatWatchMinutes ranks the watch time in minutes (B9).
	StatWatchMinutes Stat = "watch_minutes"
	// StatMessages ranks the chat messages sent (B9).
	StatMessages Stat = "messages"
	// StatCommandsRun ranks the completed commands (B9).
	StatCommandsRun Stat = "commands_run"
	// StatMentions ranks the times the user was mentioned (B9).
	StatMentions Stat = "mentions"
	// StatStreamsWatched ranks the streams the user watched (B9).
	StatStreamsWatched Stat = "streams_watched"
)

// Valid reports whether s is a known statistic.
func (s Stat) Valid() bool {
	switch s {
	case StatWatchMinutes, StatMessages, StatCommandsRun, StatMentions, StatStreamsWatched:
		return true
	}
	return false
}

// Ranked is a user in a ranking with the value of the statistic
// (roadmap 5.2.5).
type Ranked struct {
	// User is the ranked user with its identities and statistics.
	User User
	// Value is the user's value of the statistic.
	Value int64
}
