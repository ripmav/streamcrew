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
// (roadmap 4.4): the operations go through Helix as the streamer
// account. Purge does not exist on Twitch and is refused.
type moderation struct{ p *Platform }

var _ connector.Moderation = moderation{}

// the timeout range Twitch takes, in seconds (Helix timeouts).
const (
	minTimeoutSeconds = 60
	maxTimeoutSeconds = 86400
)

// Timeout implements connector.Moderation; the duration is clamped to
// the range Twitch takes (60 seconds to 24 hours).
func (m moderation) Timeout(ctx context.Context, target user.Identity, d time.Duration, reason string) error {
	st, err := m.p.account(ctx)
	if err != nil {
		return err
	}
	hc, err := m.p.ops(ctx)
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
		BroadcasterID: st.AccountID,
		ModeratorID:   st.AccountID,
		FromID:        target.PlatformUserID,
		Duration:      secs,
		Reason:        reason,
	})
	return refuse(err)
}

// Purge implements connector.Moderation: Twitch has no purge, so the
// operation is refused.
func (m moderation) Purge(context.Context, user.Identity) error {
	return errors.Join(connector.ErrRefused, errors.New("twitch has no purge"))
}

// ClearChat implements connector.Moderation.
func (m moderation) ClearChat(ctx context.Context) error {
	st, err := m.p.account(ctx)
	if err != nil {
		return err
	}
	hc, err := m.p.ops(ctx)
	if err != nil {
		return err
	}
	return refuse(hc.ClearChat(ctx, st.AccountID, st.AccountID))
}

// Ban implements connector.Moderation.
func (m moderation) Ban(ctx context.Context, target user.Identity, reason string) error {
	st, err := m.p.account(ctx)
	if err != nil {
		return err
	}
	hc, err := m.p.ops(ctx)
	if err != nil {
		return err
	}
	_, err = hc.Ban(ctx, helix.BanInput{
		BroadcasterID: st.AccountID,
		ModeratorID:   st.AccountID,
		FromID:        target.PlatformUserID,
		Reason:        reason,
	})
	return refuse(err)
}

// Unban implements connector.Moderation: it lifts the ban, and if the
// user is not banned, the timeout.
func (m moderation) Unban(ctx context.Context, target user.Identity) error {
	st, err := m.p.account(ctx)
	if err != nil {
		return err
	}
	hc, err := m.p.ops(ctx)
	if err != nil {
		return err
	}
	err = hc.Unban(ctx, st.AccountID, st.AccountID, target.PlatformUserID)
	if notFound(err) {
		err = hc.Untimeout(ctx, st.AccountID, st.AccountID, target.PlatformUserID)
	}
	return refuse(err)
}

// Mod implements connector.Moderation.
func (m moderation) Mod(ctx context.Context, target user.Identity) error {
	st, err := m.p.account(ctx)
	if err != nil {
		return err
	}
	hc, err := m.p.ops(ctx)
	if err != nil {
		return err
	}
	return refuse(hc.AddMod(ctx, st.AccountID, st.AccountID, target.PlatformUserID))
}

// Unmod implements connector.Moderation.
func (m moderation) Unmod(ctx context.Context, target user.Identity) error {
	st, err := m.p.account(ctx)
	if err != nil {
		return err
	}
	hc, err := m.p.ops(ctx)
	if err != nil {
		return err
	}
	return refuse(hc.RemoveMod(ctx, st.AccountID, st.AccountID, target.PlatformUserID))
}
