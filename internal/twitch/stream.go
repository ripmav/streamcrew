// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"fmt"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/helix"
)

// SetTitle sets the title of the stream of the channel ch (roadmap 5.6,
// SetTitle) through Helix.
func (p *Platform) SetTitle(ctx context.Context, ch connector.Channel, title string) error {
	if _, err := p.account(ctx); err != nil {
		return err
	}
	broadcaster, err := p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	hc, err := p.ops(ctx)
	if err != nil {
		return err
	}
	return refuse(hc.UpdateChannel(ctx, helix.UpdateChannelInput{
		BroadcasterID: broadcaster,
		Title:         title,
	}))
}

// SetGame sets the category of the stream of the channel ch (roadmap 5.6,
// SetGame). The category is named; its ID is looked up, then set.
func (p *Platform) SetGame(ctx context.Context, ch connector.Channel, game string) error {
	if _, err := p.account(ctx); err != nil {
		return err
	}
	broadcaster, err := p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	hc, err := p.ops(ctx)
	if err != nil {
		return err
	}
	games, err := hc.GetGames(ctx, nil, "", game)
	if err != nil {
		return refuse(err)
	}
	if len(games) == 0 {
		return fmt.Errorf("no category named %q", game)
	}
	return refuse(hc.UpdateChannel(ctx, helix.UpdateChannelInput{
		BroadcasterID: broadcaster,
		GameID:        games[0].ID,
	}))
}

var _ connector.StreamControl = (*Platform)(nil)
