// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"errors"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/helix"
)

// moderation is the connector.Moderation of the twitch platform
// (roadmap 4.4): the home channel runs through Helix as the streamer
// account, the additional channels as the bot account, which moderates
// them (spec twitch-multi-channel.md, B4). Purge does not exist on Twitch
// and is refused.
type moderation struct{ p *Platform }

var _ connector.Moderation = moderation{}

// the timeout range Twitch takes, in seconds (Helix timeouts).
const (
	minTimeoutSeconds = 60
	maxTimeoutSeconds = 86400
)

// Timeout implements connector.Moderation; the duration is clamped to
// the range Twitch takes (60 seconds to 24 hours). It acts on the
// channel ch, with the account that operates it.
func (m moderation) Timeout(ctx context.Context, ch connector.Channel, target user.Identity, d time.Duration, reason string) error {
	st, hc, err := m.p.accountFor(ctx, ch)
	if err != nil {
		return err
	}
	broadcaster, err := m.p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	secs := int(d.Seconds())
	switch {
	case secs < minTimeoutSeconds:
		secs = minTimeoutSeconds
	case secs > maxTimeoutSeconds:
		secs = maxTimeoutSeconds
	}
	_, err = hc.Timeout(ctx, helix.TimeoutInput{
		BroadcasterID: broadcaster,
		ModeratorID:   st.AccountID,
		FromID:        target.PlatformUserID,
		Duration:      secs,
		Reason:        reason,
	})
	return refuse(err)
}

// Purge implements connector.Moderation: Twitch has no purge, so the
// operation is refused.
func (m moderation) Purge(context.Context, connector.Channel, user.Identity) error {
	return errors.Join(connector.ErrRefused, errors.New("twitch has no purge"))
}

// ClearChat implements connector.Moderation; it clears the chat of the
// channel ch, with the account that operates it.
func (m moderation) ClearChat(ctx context.Context, ch connector.Channel) error {
	st, hc, err := m.p.accountFor(ctx, ch)
	if err != nil {
		return err
	}
	broadcaster, err := m.p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	return refuse(hc.ClearChat(ctx, broadcaster, st.AccountID))
}

// Ban implements connector.Moderation; it bans from the channel ch,
// with the account that operates it.
func (m moderation) Ban(ctx context.Context, ch connector.Channel, target user.Identity, reason string) error {
	st, hc, err := m.p.accountFor(ctx, ch)
	if err != nil {
		return err
	}
	broadcaster, err := m.p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	_, err = hc.Ban(ctx, helix.BanInput{
		BroadcasterID: broadcaster,
		ModeratorID:   st.AccountID,
		FromID:        target.PlatformUserID,
		Reason:        reason,
	})
	return refuse(err)
}

// Unban implements connector.Moderation: it lifts the ban in the channel
// ch, with the account that operates it, and if the user is not banned,
// the timeout.
func (m moderation) Unban(ctx context.Context, ch connector.Channel, target user.Identity) error {
	st, hc, err := m.p.accountFor(ctx, ch)
	if err != nil {
		return err
	}
	broadcaster, err := m.p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	err = hc.Unban(ctx, broadcaster, st.AccountID, target.PlatformUserID)
	if notFound(err) {
		err = hc.Untimeout(ctx, broadcaster, st.AccountID, target.PlatformUserID)
	}
	return refuse(err)
}

// Mod implements connector.Moderation; it adds the moderator to the
// channel ch, with the account that operates it.
func (m moderation) Mod(ctx context.Context, ch connector.Channel, target user.Identity) error {
	st, hc, err := m.p.accountFor(ctx, ch)
	if err != nil {
		return err
	}
	broadcaster, err := m.p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	return refuse(hc.AddMod(ctx, broadcaster, st.AccountID, target.PlatformUserID))
}

// Unmod implements connector.Moderation; it removes the moderator from
// the channel ch, with the account that operates it.
func (m moderation) Unmod(ctx context.Context, ch connector.Channel, target user.Identity) error {
	st, hc, err := m.p.accountFor(ctx, ch)
	if err != nil {
		return err
	}
	broadcaster, err := m.p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	return refuse(hc.RemoveMod(ctx, broadcaster, st.AccountID, target.PlatformUserID))
}
