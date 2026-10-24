// SPDX-License-Identifier: MIT

package twitch_test

import (
	"os"
	"path/filepath"
	"testing"

	jsonv2 "encoding/json/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// TestEventContract maps every fixture of testdata — the complete,
// anonymized payloads of the official EventSub documentation, one per
// subscription of the desired table — through the mapper and compares
// the result with the documented event model (plan 4.5, task 2).
func TestEventContract(t *testing.T) {
	alice := ident("alice", "Alice", "111000222")
	bob := ident("bob", "Bob", "111000333")
	carol := ident("carol", "Carol", "111000444")

	tests := []struct {
		name       string
		wantEvent  *connector.Event
		wantMsg    *connector.Incoming
		wantStream *bool
	}{
		{name: "stream.online", wantStream: new(true)},
		{name: "stream.offline", wantStream: new(false)},
		{name: "channel.follow", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelFollow,
			User:     alice,
		}},
		{name: "channel.raid", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelRaid,
			User:     bob,
			Details:  eventtype.Details{Raid: &eventtype.Raid{Viewers: 7}},
		}},
		{name: "channel.chat.message", wantMsg: &connector.Incoming{
			Platform: platform.Twitch,
			Author: user.Identity{
				Platform:       platform.Twitch,
				PlatformUserID: "111000222",
				Login:          "alice",
				DisplayName:    "Alice",
				Color:          "#FF0000",
				Roles:          role.NewSet(role.Moderator, role.Subscriber),
			},
			Message: eventtype.Message{ID: "msg-100", Text: "hi :Kappa: hey", Emotes: []string{":Kappa:"}},
		}},
		{name: "channel.chat.message_delete", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.ChatMessageDelete,
			User:     alice,
			Details:  eventtype.Details{Message: &eventtype.Message{ID: "msg-100"}},
		}},
		{name: "channel.chat.notification.subscription", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelSubscribe,
			User:     alice,
			Details:  eventtype.Details{Subscription: &eventtype.Subscription{Plan: "1000"}},
		}},
		{name: "channel.chat.notification.resubscription", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelResubscribe,
			User:     alice,
			Details: eventtype.Details{
				Subscription: &eventtype.Subscription{Plan: "1000"},
				Message:      &eventtype.Message{Text: "thanks!"},
			},
		}},
		{name: "channel.chat.notification.gift", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchSubscriptionGift,
			User:     alice,
			// The platform names the recipient by user data; the login
			// stands in for the display name (map.go).
			Target: ident("carol", "Carol", "111000444"),
			Details: eventtype.Details{
				Subscription: &eventtype.Subscription{Plan: "1000"},
				Gift:         &eventtype.Gift{Count: 1},
			},
		}},
		{name: "channel.chat.notification.mass_gift", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchSubscriptionMassGift,
			User:     bob,
			Details: eventtype.Details{
				Subscription: &eventtype.Subscription{Plan: "1000"},
				Gift:         &eventtype.Gift{Count: 3, CommunityGiftID: "cg-1"},
			},
		}},
		{name: "channel.cheer", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchBitsCheer,
			User:     alice,
			Details: eventtype.Details{
				Bits:    &eventtype.Bits{Amount: 100},
				Message: &eventtype.Message{Text: "cheers"},
			},
		}},
		{name: "channel.moderate.timeout", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.ChatUserTimeout,
			User:     carol,
			Details:  eventtype.Details{Moderation: &eventtype.Moderation{Message: "spam"}},
		}},
		{name: "channel.moderate.ban", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.ChatUserBan,
			User:     carol,
		}},
		{name: "channel.shared_chat.begin", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchSharedChatStart,
			Details:  eventtype.Details{SharedChat: &eventtype.SharedChat{SessionID: "session-1"}},
		}},
		{name: "channel.shared_chat.update", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchSharedChatUpdate,
			Details:  eventtype.Details{SharedChat: &eventtype.SharedChat{SessionID: "session-1"}},
		}},
		{name: "channel.shared_chat.end", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchSharedChatEnd,
			Details:  eventtype.Details{SharedChat: &eventtype.SharedChat{SessionID: "session-1"}},
		}},
		{name: "channel.hype_train.begin", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchHypeTrainStart,
			Details:  eventtype.Details{HypeTrain: &eventtype.HypeTrain{Level: 1, Progress: 10, Goal: 100}},
		}},
		{name: "channel.hype_train.progress", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchHypeTrainProgress,
			Details:  eventtype.Details{HypeTrain: &eventtype.HypeTrain{Level: 2, Progress: 25, Goal: 100}},
		}},
		{name: "channel.hype_train.end", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchHypeTrainEnd,
			Details:  eventtype.Details{HypeTrain: &eventtype.HypeTrain{Level: 3, Progress: 100, Goal: 100}},
		}},
		{name: "channel.ad_break.begin", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchAdStart,
			Details:  eventtype.Details{AdBreak: &eventtype.AdBreak{Duration: 300}},
		}},
		{name: "channel.shoutout.receive", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchShoutoutReceive,
			User:     bob,
			Details:  eventtype.Details{Shoutout: &eventtype.Shoutout{Viewers: 42}},
		}},
		{name: "channel.goal.begin", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchGoalStart,
			Details:  eventtype.Details{Goal: &eventtype.Goal{Target: 1000}},
		}},
		{name: "channel.goal.progress", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchGoalProgress,
			Details:  eventtype.Details{Goal: &eventtype.Goal{Current: 250, Target: 1000}},
		}},
		{name: "channel.goal.end", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchGoalEnd,
			Details:  eventtype.Details{Goal: &eventtype.Goal{Current: 1000, Target: 1000}},
		}},
		{name: "channel.charity_campaign.progress", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchCharityDonation,
			Details:  eventtype.Details{Charity: &eventtype.Charity{Current: 150, Target: 1000, Currency: "USD"}},
		}},
		{name: "channel.charity_campaign.stop", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchCharityDonation,
			Details:  eventtype.Details{Charity: &eventtype.Charity{Current: 1000, Target: 1000, Currency: "USD"}},
		}},
		{name: "channel.channel_points_automatic_reward_redemption.add", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelPointsRedeem,
			User:     alice,
			Details:  eventtype.Details{ChannelPoints: &eventtype.ChannelPoints{Amount: 100, Reward: "send_message_to_stream_chat"}},
		}},
		{name: "channel.channel_points_custom_reward_redemption.add", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchCustomPowerUpRedeem,
			User:     bob,
			Details: eventtype.Details{
				CustomPowerUp: &eventtype.CustomPowerUp{Reward: "reward-2"},
				Message:       &eventtype.Message{Text: "hello from the fixture"},
			},
		}},
		{name: "user.whisper.message", wantEvent: &connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.ChatWhisper,
			User:     carol,
			Details:  eventtype.Details{Message: &eventtype.Message{Text: "a quiet word"}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.name+".json"))
			require.NoError(t, err)
			var e twitch.Event
			require.NoError(t, jsonv2.Unmarshal(data, &e))

			r := &fakeReceiver{}
			m := mapperFor(t, r, "")
			m.OnMessage(t.Context(), e)

			switch {
			case tc.wantStream != nil:
				require.Len(t, r.streams, 1)
				assert.Equal(t, *tc.wantStream, r.streams[0])
			case tc.wantMsg != nil:
				require.Len(t, r.messages, 1)
				assert.Equal(t, *tc.wantMsg, r.messages[0])
			case tc.wantEvent != nil:
				require.Len(t, r.events, 1)
				assert.Equal(t, *tc.wantEvent, r.events[0])
			default:
				t.Fatal("no expected value")
			}
		})
	}
}

// ident is the expected identity of a fixture user.
func ident(login, display, id string) *user.Identity {
	return &user.Identity{
		Platform:       platform.Twitch,
		PlatformUserID: id,
		Login:          login,
		DisplayName:    display,
	}
}
