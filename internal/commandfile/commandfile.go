// SPDX-License-Identifier: Apache-2.0

// Package commandfile is the file format of commands as code (spec
// commands-as-code.md): commands, command groups and cooldown groups as
// documents with apiVersion, kind, metadata and spec, in YAML or JSON.
//
// So far it builds the JSON Schema of the files (B30); reading, checking,
// importing and exporting them follow (roadmap 3.5).
package commandfile

import "github.com/ripmav/streamcrew/internal/domain/command"

// APIVersion is the only version of the format (B1).
const APIVersion = "streamcrew/v1alpha1"

// Kind says what a document describes (B2).
type Kind string

// The kinds (B2).
const (
	KindChatCommand   Kind = "ChatCommand"
	KindEventCommand  Kind = "EventCommand"
	KindTimerCommand  Kind = "TimerCommand"
	KindActionGroup   Kind = "ActionGroup"
	KindCommandGroup  Kind = "CommandGroup"
	KindCooldownGroup Kind = "CooldownGroup"
)

// Kinds returns the kinds: those of commands in the order of commands.md,
// B2, then the groups.
func Kinds() []Kind {
	return []Kind{KindChatCommand, KindEventCommand, KindTimerCommand, KindActionGroup, KindCommandGroup, KindCooldownGroup}
}

// CommandKind returns the kind of command that documents of kind k
// describe; ok is false for the groups.
func (k Kind) CommandKind() (kind command.Kind, ok bool) {
	switch k {
	case KindChatCommand:
		return command.KindChat, true
	case KindEventCommand:
		return command.KindEvent, true
	case KindTimerCommand:
		return command.KindTimer, true
	case KindActionGroup:
		return command.KindActionGroup, true
	default: // KindCommandGroup, KindCooldownGroup
		return "", false
	}
}

// The values of the members of a command that a document leaves out (B10,
// B11).
const (
	// DefaultEnabled: a command is active.
	DefaultEnabled = true
	// DefaultUnlocked: a command waits for the other commands of its lock
	// group.
	DefaultUnlocked = false
	// DefaultErrorPolicy: the next action runs after one failed.
	DefaultErrorPolicy = command.ErrorContinue
	// DefaultTriggerMode: chat messages name the triggers with "!".
	DefaultTriggerMode = command.TriggerExclamation
)
