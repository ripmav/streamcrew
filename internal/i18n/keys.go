// SPDX-License-Identifier: Apache-2.0

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

	// KeyRequirementRole tells that the command needs the role role or a
	// higher one (requirements.md, B12).
	KeyRequirementRole Key = "requirement.role"
	// KeyRequirementArgumentsUsage shows how to use the command, its
	// trigger and arguments as usage (requirements.md, B32).
	KeyRequirementArgumentsUsage Key = "requirement.arguments.usage"
	// KeyRequirementArgumentsType says that the argument argument needs a
	// value of the type type (requirements.md, B33).
	KeyRequirementArgumentsType Key = "requirement.arguments.type"
	// KeyRequirementArgumentsUser says that there is no user name for the
	// argument argument (requirements.md, B33).
	KeyRequirementArgumentsUser Key = "requirement.arguments.user"
	// KeyRequirementCooldownAll says that the command is blocked for
	// everyone for the duration remaining (requirements.md, B24).
	KeyRequirementCooldownAll Key = "requirement.cooldown.all"
	// KeyRequirementCooldownUser tells the user that they can use the
	// command again after the duration remaining (requirements.md, B24).
	KeyRequirementCooldownUser Key = "requirement.cooldown.user"
	// KeyRequirementFaulty says that a requirement of the command is broken
	// (requirements.md, B7, B40); the user is not told.
	KeyRequirementFaulty Key = "requirement.faulty"
	// KeyRequirementUnknown says that this version does not know a
	// requirement of the command (requirements.md, B8); the user is not
	// told.
	KeyRequirementUnknown Key = "requirement.unknown"
)
