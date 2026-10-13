// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

var _ command.Channels = scopeChannels{}

// scopeChannels is the command.Checks.Channels port (commands.md, B9):
// the channels the profile operates, in the order home channel, then the
// additional channels of the settings section. The home channel is the
// login of the streamer account; without one the profile operates only
// the additional channels.
type scopeChannels struct {
	store    *store.Store
	settings *settings.Service
}

// Operated implements command.Channels.
func (c scopeChannels) Operated(ctx context.Context) ([]string, error) {
	var out []string
	acc, found, err := c.store.Account(ctx, string(platform.Twitch), string(connector.AccountStreamer))
	if err != nil {
		return nil, fmt.Errorf("the streamer account: %w", err)
	}
	if found && acc.Login != "" {
		out = append(out, acc.Login)
	}
	chs, err := settings.Load(ctx, c.settings, settings.DefaultChannels())
	if err != nil {
		return nil, fmt.Errorf("the channels settings: %w", err)
	}
	for _, login := range chs.Additional {
		if slices.ContainsFunc(out, func(home string) bool { return strings.EqualFold(home, login) }) {
			continue
		}
		out = append(out, login)
	}
	return out, nil
}
