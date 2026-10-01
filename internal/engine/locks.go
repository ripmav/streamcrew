// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"iter"
	"maps"
	"slices"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/settings"
)

// Container is an action that holds other actions, such as a condition or
// a repetition. Their action types count for the locks as well (B22, B23),
// and it runs them with Run.PerformChild (Code-ADR-0013). Children returns
// them always in the same order; the index is the position of a child
// action in its path (actions.md B9).
type Container = command.Parent

// locks returns the locks an instance of cmd needs under mode (B20 to B29);
// none is an empty list. They are fixed when the instance is queued (B28).
func (e *Engine) locks(cmd command.Command, mode settings.LockMode) ([]string, error) {
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
		for a := range allActions(cmd.Actions) {
			types["action:"+a.DocType()] = struct{}{}
		}
		return slices.Sorted(maps.Keys(types)), nil
	case settings.LockVisualAudio:
		for a := range allActions(cmd.Actions) {
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
