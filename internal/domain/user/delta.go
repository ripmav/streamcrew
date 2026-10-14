// SPDX-License-Identifier: MIT

package user

import (
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// StatsDelta adds to the statistics of one user (B9): the counters are
// incremented by its values, FirstSeen is set when it is unset and Seen is
// set, and LastSeen becomes Seen when it is set. Zero counters change
// nothing; a zero Seen leaves the seen times alone.
type StatsDelta struct {
	UserID id.ID
	// Seen is the contact that caused the delta; zero for contacts
	// without a time, e.g. the watch time of a whole minute.
	Seen           time.Time
	WatchMinutes   int64
	Messages       int64
	CommandsRun    int64
	Mentions       int64
	StreamsWatched int64
}
