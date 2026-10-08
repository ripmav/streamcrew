// SPDX-License-Identifier: MIT

package i18n

// The keys of the messages (ADR-0022, point 8). All keys of the core are
// here, each with a message in every catalog; a test checks that the
// constants and the catalogs agree.
const (
	// KeyDurationDays and the other duration keys write one unit of a
	// duration with the number n (ADR-0022, point 6).
	KeyDurationDays    Key = "duration.days"
	KeyDurationHours   Key = "duration.hours"
	KeyDurationMinutes Key = "duration.minutes"
	KeyDurationSeconds Key = "duration.seconds"
)
