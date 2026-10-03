// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"slices"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/settings"
)

// Container is an action that holds other actions, such as a condition or
// a repetition. Their action types count for the locks as well (B22, B23),
// and it runs them with Run.PerformChild (Code-ADR-0013). Children returns
// them always in the same order; the index is the position of a child
// action in its path (actions.md B9).
type Container = command.Parent

// WaitingCaller is an action that runs another command as part of its own
// instance and waits for it (B31), such as the command action with
// waiting. The action types of that command, and of the commands it waits
// for in turn, count for the locks (B22, B23).
type WaitingCaller interface {
	// WaitsFor returns the command the action calls with waiting; ok is
	// false if it calls none or does not wait.
	WaitsFor() (commandID id.ID, ok bool)
}

// locks returns the locks an instance of cmd needs under mode (B20 to B29);
// none is an empty list. They are fixed when the instance is queued (B28),
// with the versions of the called commands at that time.
func (e *Engine) locks(ctx context.Context, cmd command.Command, mode settings.LockMode) ([]string, error) {
	switch {
	case !mode.Valid():
		return nil, fmt.Errorf("%w: unknown lock mode %q", ErrInvalidConfig, mode)
	case cmd.Unlocked, len(cmd.Actions) == 0:
		return []string{}, nil // B27, B29
	}
	switch mode {
	case settings.LockPerCommandType:
		return []string{"kind:" + string(cmd.Kind)}, nil
	case settings.LockPerActionType:
		types := make(map[string]struct{})
		for a := range e.lockActions(ctx, cmd) {
			types["action:"+a.DocType()] = struct{}{}
		}
		return slices.Sorted(maps.Keys(types)), nil
	case settings.LockVisualAudio:
		for a := range e.lockActions(ctx, cmd) {
			if e.types.VisualAudio(a.DocType()) {
				return []string{"visual_audio"}, nil
			}
		}
		return []string{}, nil
	case settings.LockSingular:
		return []string{"singular"}, nil
	case settings.LockNone:
		return []string{}, nil
	default:
		return nil, fmt.Errorf("%w: unknown lock mode %q", ErrInvalidConfig, mode)
	}
}

// lockActions yields the actions whose types count for the locks of cmd
// (B22, B23): its own, and depth first those of the commands it calls with
// waiting, each command once, also in a cycle. A called command that cannot
// be read counts nothing, with a warning: the call reads it again when it
// runs and fails then, so its actions do not run either.
func (e *Engine) lockActions(ctx context.Context, cmd command.Command) iter.Seq[command.Action] {
	return func(yield func(command.Action) bool) {
		seen := map[id.ID]bool{cmd.ID: true}
		var walk func(c command.Command) bool
		walk = func(c command.Command) bool {
			for a := range allActions(c.Actions) {
				if !yield(a) {
					return false
				}
				caller, ok := a.(WaitingCaller)
				if !ok {
					continue
				}
				calledID, ok := caller.WaitsFor()
				if !ok || seen[calledID] {
					continue
				}
				seen[calledID] = true
				called, err := e.commands.Command(ctx, calledID)
				if err != nil {
					e.logger.WarnContext(ctx, "the locks leave out a called command that cannot be read",
						"command", cmd.Name, "called", calledID, "error", err)
					continue
				}
				if !walk(called) {
					return false
				}
			}
			return true
		}
		walk(cmd)
	}
}

// allActions yields the actions of list and, depth first, the actions they
// hold. Inactive actions and the actions they hold are left out: they do
// not run, so they need no lock (actions.md B1).
func allActions(list []command.Action) iter.Seq[command.Action] {
	return func(yield func(command.Action) bool) {
		walkActions(list, yield)
	}
}

func walkActions(list []command.Action, yield func(command.Action) bool) bool {
	for _, a := range list {
		if p, ok := a.(Performer); ok && !p.Enabled() {
			continue
		}
		if !yield(a) {
			return false
		}
		if c, ok := a.(Container); ok && !walkActions(c.Children(), yield) {
			return false
		}
	}
	return true
}
