// SPDX-License-Identifier: MIT

package engine

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/template"
)

// Source says why an instance exists (B1).
type Source string

// Sources of instances.
const (
	// SourceChat is a chat message that matched a trigger.
	SourceChat Source = "chat"
	// SourceEvent is an event of an event command.
	SourceEvent Source = "event"
	// SourceTimer is the timer schedule.
	SourceTimer Source = "timer"
	// SourceCall is another command that started this one without waiting
	// for it (B32).
	SourceCall Source = "call"
	// SourceManual is a start by hand, from the user interface or the API
	// (B14).
	SourceManual Source = "manual"
	// SourceReplay is a replay from the history (B54).
	SourceReplay Source = "replay"
)

// State is the state of an instance (B2).
type State string

// States of an instance.
const (
	// StatePending waits in the queue.
	StatePending State = "pending"
	// StateRunning runs its actions.
	StateRunning State = "running"
	// StateCompleted ran all actions or ended itself (B4).
	StateCompleted State = "completed"
	// StateFailed ended after an action failed under the error policy
	// "abort" (B71).
	StateFailed State = "failed"
	// StateCanceled was canceled (B50).
	StateCanceled State = "canceled"
)

// Final reports whether s is an end state, which never changes (B2).
func (s State) Final() bool {
	switch s {
	case StateCompleted, StateFailed, StateCanceled:
		return true
	default:
		return false
	}
}

// Params are the data of a run (B80). The engine copies them when it queues
// an instance; the copy must not be changed. Where a field says "none", the
// run has no such thing; no value stands for another one (Code-ADR-0017).
type Params struct {
	// Platform is the platform the command was triggered on; empty if the
	// run has none, e.g. a timer.
	Platform platform.Name
	// User is the triggering user; nil if the run has none, e.g. a timer.
	User *user.User
	// Target is the user the run is about (B81). The caller sets the one
	// it knows, e.g. the target of an event, or nil. The engine sets the
	// target of the run before queuing: without one from the caller, Start
	// and Trigger take the user the first argument names if the platform
	// knows them, and otherwise the triggering user.
	Target *user.User
	// Args are the arguments: the words after the trigger, with quoted text
	// as one argument.
	Args []string
	// ArgsText is the text after the trigger as written; empty if there is
	// none. It must not be empty if there are arguments.
	ArgsText string
	// Message is the triggering chat message, with the trigger; empty if
	// there is none.
	Message string
	// MessageID is the platform's ID of the triggering chat message, e.g.
	// for a reply (actions.md B64); empty if there is none or the platform
	// gave none. It needs Message and Platform.
	MessageID string
	// Emotes are the emote codes in Message as the platform marks them;
	// there are none without a message.
	Emotes []string
	// Values are the values of the run, e.g. the values of an event (spec
	// events.md, B7), by identifier name without "$".
	Values map[string]template.Value
}

// clone returns a copy of p that shares no slices or maps with it. Users
// are shared: the engine does not change them.
func (p Params) clone() Params {
	p.Args = slices.Clone(p.Args)
	p.Emotes = slices.Clone(p.Emotes)
	p.Values = maps.Clone(p.Values)
	return p
}

// ActionError is an action of an instance that failed (B60).
type ActionError struct {
	// Path is the place of the action in the command: the position from 1
	// at each level, e.g. [3, 2] for the second child action of the third
	// action (actions.md B9).
	Path []int `json:"path"`
	// Type is the action type.
	Type string `json:"type"`
	// Message says what went wrong.
	Message string `json:"message"`
}

// Instance is an instance as the history keeps it and the events of the
// engine carry it (B60, B61). It is a copy; it does not change with the
// instance. Lists are never nil, so JSON has empty arrays; fields that do
// not apply are left out: the user without one, the start and end times
// before the state has them.
type Instance struct {
	ID          id.ID         `json:"id"`
	CommandID   id.ID         `json:"commandId"`
	CommandName string        `json:"commandName"`
	Source      Source        `json:"source"`
	State       State         `json:"state"`
	Platform    platform.Name `json:"platform,omitempty"`
	// UserID and UserName name the triggering user; UserName is the display
	// name on Platform.
	UserID   id.ID    `json:"userId,omitzero"`
	UserName string   `json:"userName,omitempty"`
	Args     []string `json:"args"`
	// Parent is the calling instance if Source is SourceCall; it is left
	// out for the other sources (B31, B32).
	Parent    id.ID         `json:"parent,omitzero"`
	QueuedAt  time.Time     `json:"queuedAt"`
	StartedAt time.Time     `json:"startedAt,omitzero"`
	EndedAt   time.Time     `json:"endedAt,omitzero"`
	Errors    []ActionError `json:"errors"`
}

// instance is an instance while the engine knows it. The fields after the
// blank line are guarded by Engine.mu.
type instance struct {
	id     id.ID
	cmd    command.Command
	source Source
	params Params
	scope  *template.Scope
	locks  []string
	// parent is the calling instance; zero for none.
	parent id.ID
	// chain has the commands from the first caller to this one (B73).
	chain []id.ID
	// greeting is the instance of the greeting this one belongs to: itself,
	// or the greeting that called it and waits for it; zero if it is no
	// greeting (B41, B43).
	greeting id.ID
	// mediaGap is the gap after the pictures and sounds of a greeting
	// (B43).
	mediaGap time.Duration
	// start is closed when the instance gets its locks.
	start chan struct{}
	// cancel cancels the context of the instance.
	cancel context.CancelFunc

	state     State
	queuedAt  time.Time
	startedAt time.Time
	endedAt   time.Time
	errors    []ActionError
}

// origin says which instance called an instance, if any.
type origin struct {
	// parent is the calling instance.
	parent id.ID
	// chain has the commands from the first caller to the calling one.
	chain []id.ID
}

// newInstance returns a pending instance of cmd, the version of the command
// at this moment (B3).
func newInstance(cmd command.Command, src Source, p Params, cfg Config, locks []string, org origin) *instance {
	cmd.Actions = slices.Clone(cmd.Actions)
	cmd.Requirements = slices.Clone(cmd.Requirements)
	p = p.clone()
	return &instance{
		id:       id.New(),
		cmd:      cmd,
		source:   src,
		params:   p,
		scope:    newScope(cmd, p, cfg),
		locks:    locks,
		parent:   org.parent,
		chain:    append(slices.Clone(org.chain), cmd.ID),
		mediaGap: cfg.Commands.EntranceMediaGap.Std(),
		start:    make(chan struct{}),
		state:    StatePending,
		queuedAt: time.Now(),
	}
}

// newScope returns the scope of the templates of a run (B80).
func newScope(cmd command.Command, p Params, cfg Config) *template.Scope {
	s := &template.Scope{
		Platform:     p.Platform,
		User:         p.User,
		Target:       p.Target,
		CommandName:  cmd.Name,
		Message:      p.Message,
		Emotes:       p.Emotes,
		Args:         p.Args,
		ArgsText:     p.ArgsText,
		ArgDelimiter: cfg.Commands.ArgDelimiter,
		Location:     cfg.Location,
	}
	for name, v := range p.Values {
		s.SetValue(name, v)
	}
	return s
}

// snapshot returns a copy of in; Engine.mu is held.
func (in *instance) snapshot() Instance {
	s := Instance{
		ID:          in.id,
		CommandID:   in.cmd.ID,
		CommandName: in.cmd.Name,
		Source:      in.source,
		State:       in.state,
		Platform:    in.params.Platform,
		Args:        append([]string{}, in.params.Args...),
		Parent:      in.parent,
		QueuedAt:    in.queuedAt,
		StartedAt:   in.startedAt,
		EndedAt:     in.endedAt,
		Errors:      append([]ActionError{}, in.errors...),
	}
	if u := in.params.User; u != nil {
		s.UserID = u.ID
		s.UserName = displayName(*u, in.params.Platform)
	}
	return s
}

// displayName returns the name of u on platform p, or on its first platform
// if it has no identity on p.
func displayName(u user.User, p platform.Name) string {
	i, ok := u.Identity(p)
	if !ok {
		if len(u.Identities) == 0 {
			return ""
		}
		i = u.Identities[0]
	}
	return cmp.Or(i.DisplayName, i.Login)
}
