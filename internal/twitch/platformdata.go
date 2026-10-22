// SPDX-License-Identifier: MIT

package twitch

import (
	"context"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

var _ connector.PlatformData = (*Platform)(nil)

// UserPlatformData implements connector.PlatformData (roadmap 5.2.2,
// users-and-roles.md, B10): it asks Helix for the follow and the
// subscription of the account on the home channel. The Helix endpoint of
// the subscriptions has no start date, so SubscribedAt stays zero
// (decision, plan phase 5.2.2).
func (p *Platform) UserPlatformData(ctx context.Context, platformUserID string) (user.PlatformData, error) {
	channelID, err := p.channelID(ctx, "")
	if err != nil {
		return user.PlatformData{}, err
	}
	hc, err := p.ops(ctx)
	if err != nil {
		return user.PlatformData{}, err
	}
	var data user.PlatformData
	followers, err := hc.GetFollowers(ctx, channelID, platformUserID)
	if err != nil {
		return user.PlatformData{}, refuse(err)
	}
	if len(followers) > 0 {
		data.FollowedAt = followers[0].FollowedAt
	}
	subs, err := hc.GetChannelSubscriptions(ctx, channelID)
	if err != nil {
		return user.PlatformData{}, refuse(err)
	}
	for _, s := range subs {
		if s.SubscriberID == platformUserID {
			data.SubTier = subTier(s.Tier)
			break
		}
	}
	return data, nil
}

// subTier maps the Helix tier ("1000", "2000", "3000") to the tier number
// (users-and-roles.md, B10: 1 to 3 on Twitch); 0 for an unknown tier.
func subTier(tier string) int {
	switch tier {
	case "1000":
		return 1
	case "2000":
		return 2
	case "3000":
		return 3
	default:
		return 0
	}
}
