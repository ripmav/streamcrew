// SPDX-License-Identifier: Apache-2.0

package command

import (
	"context"
	"sync"

	"github.com/ripmav/streamcrew/internal/event"
)

// snapshot is what the service derives from all commands for chat messages
// and events: the triggers of the enabled chat commands (B16) and the event
// command of each event type (B20).
type snapshot struct {
	triggers *TriggerIndex
	events   map[event.Type]Command
}

// newSnapshot derives the snapshot of cmds.
func newSnapshot(cmds []Command) *snapshot {
	s := &snapshot{triggers: NewTriggerIndex(cmds), events: make(map[event.Type]Command)}
	for _, cmd := range cmds {
		if cmd.Kind == KindEvent {
			s.events[cmd.Event] = cmd
		}
	}
	return s
}

// derived holds the snapshot of the commands. It is built on first use
// after a change through the service, so the core reads the commands from
// the store once per change, not once per chat message.
type derived struct {
	mu sync.Mutex
	// generation counts the changes; a snapshot built while one happened
	// is not kept.
	generation uint64
	// snap is nil until the snapshot is built after a change.
	snap *snapshot
}

// changed drops the snapshot after a change of the commands, also one that
// failed, since it may have changed something.
func (s *Service) changed() {
	d := &s.derived
	d.mu.Lock()
	defer d.mu.Unlock()
	d.generation++
	d.snap = nil
}

// snapshot returns the snapshot of the commands, built if needed.
func (s *Service) snapshot(ctx context.Context) (*snapshot, error) {
	d := &s.derived
	d.mu.Lock()
	snap, generation := d.snap, d.generation
	d.mu.Unlock()
	if snap != nil {
		return snap, nil
	}
	cmds, err := s.Commands(ctx)
	if err != nil {
		return nil, err
	}
	snap = newSnapshot(cmds)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.generation == generation {
		d.snap = snap
	}
	return snap, nil
}

// Recognize finds the chat command that a chat message triggers among the
// enabled chat commands (B16, TriggerIndex.Recognize).
func (s *Service) Recognize(ctx context.Context, message string) (Recognition, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return Recognition{}, err
	}
	return snap.triggers.Recognize(message), nil
}

// EventCommand returns the event command of the event type t, enabled or
// not (B20); ok is false if there is none.
func (s *Service) EventCommand(ctx context.Context, t event.Type) (cmd Command, ok bool, err error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return Command{}, false, err
	}
	cmd, ok = snap.events[t]
	return cmd, ok, nil
}
