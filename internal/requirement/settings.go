// SPDX-License-Identifier: MIT

package requirement

import (
	"context"
	"errors"
	"fmt"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
)

// errNoMessageID is wrapped by the error of deleting a triggering message
// the platform gave no ID for.
var errNoMessageID = errors.New("the platform gave no ID of the message")

// Decided implements engine.Requirements: once the decision about cmd for
// p is final, it deletes the triggering chat message if cmd has the setting
// for it (B61), on the platform of the run, also if a requirement rejected
// cmd or a threshold waits. Without a chat message it does nothing. A
// message it cannot delete is an error, which the engine logs; the run goes
// on.
func (s *Service) Decided(ctx context.Context, cmd command.Command, p engine.Params) error {
	settings, ok := find[command.SettingsRequirement](cmd)
	if !ok || !settings.DeleteTriggerMessage || p.Message == "" {
		return nil
	}
	if p.MessageID == "" {
		return fmt.Errorf("delete the triggering message of %q on %s: %w", cmd.Name, p.Platform, errNoMessageID)
	}
	target, ok := s.ports.Platforms.Platform(p.Platform)
	if !ok || !target.Status().Connected() {
		return fmt.Errorf("delete the triggering message of %q: %s: %w", cmd.Name, p.Platform, connector.ErrNotConnected)
	}
	if err := target.Chat().Delete(ctx, p.MessageID); err != nil {
		return fmt.Errorf("delete the triggering message of %q on %s: %w", cmd.Name, p.Platform, err)
	}
	return nil
}
