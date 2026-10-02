// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"fmt"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// CancelAll cancels all queued and running instances, this one included
// (B51): the command action "cancel all" uses it (actions.md B35). This
// instance ends as canceled once the running action returns.
func (r *Run) CancelAll(ctx context.Context) {
	r.engine.CancelAll(ctx)
}

// Pause pauses scope as Engine.Pause does (B40, B41), for the command
// action (actions.md B36). A pause of all holds back queued instances, not
// this running one.
func (r *Run) Pause(ctx context.Context, scope PauseScope) error {
	return r.engine.Pause(ctx, scope)
}

// Resume ends the pause of scope as Engine.Resume does (B40, B41), for the
// command action (actions.md B36).
func (r *Run) Resume(ctx context.Context, scope PauseScope) error {
	return r.engine.Resume(ctx, scope)
}

// StartCooldown starts the cooldowns of the command as if it had just been
// queued for this run (actions.md B37), through Requirements.StartCooldown.
// Without a requirement service there are no cooldowns, and it does
// nothing. A command that does not exist is an error.
func (r *Run) StartCooldown(ctx context.Context, commandID id.ID) error {
	cmd, err := r.engine.commands.Command(ctx, commandID)
	if err != nil {
		return fmt.Errorf("start cooldown of command %s: %w", commandID, err)
	}
	if r.engine.requirements == nil {
		return nil
	}
	if err := r.engine.requirements.StartCooldown(ctx, cmd, r.in.params); err != nil {
		return fmt.Errorf("start cooldown of command %q: %w", cmd.Name, err)
	}
	return nil
}
