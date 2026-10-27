// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/time/rate"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/helix"
)

// maxChatMessage is the limit Twitch takes for one chat message, in
// runes (roadmap 4.4, actions.md B65).
const maxChatMessage = 500

// The Twitch chat send limit, per channel: 20 messages in 30 seconds
// (spec twitch-multi-channel.md, B9).
const (
	chatRate  = rate.Limit(20.0 / 30.0) // messages per second
	chatBurst = 20                      // messages that may go at once
)

// The second send limit of the IRC adapter, per channel: one message
// in 3 seconds (spec twitch-multi-channel.md, B18).
const (
	chatRate3  = rate.Limit(1.0 / 3.0)
	chatBurst3 = 1
)

// chat is the connector.Chat of the twitch platform (roadmap 4.4): it
// sends and deletes messages of the channel through Helix. The home
// channel is operated with the streamer account, the additional channels
// with the bot account (spec twitch-multi-channel.md, B4).
type chat struct{ p *Platform }

var (
	_ connector.Chat    = chat{}
	_ connector.Replier = chat{}
)

// Send implements connector.Chat: it splits a text that is longer than
// Twitch allows and keeps to the rate limits of the platform through
// the resilient HTTP client (actions.md B65). The channels that run over
// the IRC adapter run through the IRC session of the bot account
// (spec twitch-multi-channel.md, B18); there the bot account is the
// sender, so a message from it is the normal case.
func (c chat) Send(ctx context.Context, m connector.Message) error {
	if m.Channel != "" && c.p.ircOn {
		return c.p.ircSend(ctx, m.Channel, m.Text)
	}
	if m.From == connector.AccountBot {
		return c.p.botSend(ctx, m)
	}
	return c.p.streamerSend(ctx, m)
}

// Reply implements connector.Replier (code review 2026-10-24, T5): a
// reply of an additional channel runs over the IRC session with the
// reply-parent-msg-id tag; the home channel and the Helix path have no
// reply parameter, so the reply degrades to a plain message there
// (shortcut: no reply tag on the Helix path; add it to SendChatMessage
// when the platform takes one).
func (c chat) Reply(ctx context.Context, messageID string, m connector.Message) error {
	if m.Channel != "" && c.p.ircOn {
		return c.p.ircSendReply(ctx, m.Channel, messageID, m.Text)
	}
	if m.From == connector.AccountBot {
		return c.p.botSend(ctx, m)
	}
	return c.p.streamerSend(ctx, m)
}

// botSend sends the message with the bot account (spec
// twitch-multi-channel.md, B4): to an additional channel, or to the home
// channel while a bot session runs. Without a connected bot account it
// is refused.
func (p *Platform) botSend(ctx context.Context, m connector.Message) error {
	if m.Channel == "" && !p.botState() {
		return errors.Join(connector.ErrNotConnected, errors.New("the bot account is not connected"))
	}
	bot, err := p.auth.Bot(ctx)
	if err != nil {
		return err
	}
	if !bot.Ready {
		return errors.Join(connector.ErrNotConnected, errors.New("the bot account has no valid token"))
	}
	broadcaster, err := p.channelID(ctx, m.Channel)
	if err != nil {
		return err
	}
	hc, err := p.opsToken(ctx, p.auth.BotToken)
	if err != nil {
		return err
	}
	return p.sendParts(ctx, hc, broadcaster, bot.AccountID, m)
}

// streamerSend sends the message with the streamer account.
func (p *Platform) streamerSend(ctx context.Context, m connector.Message) error {
	st, err := p.account(ctx)
	if err != nil {
		return err
	}
	broadcaster, err := p.channelID(ctx, m.Channel)
	if err != nil {
		return err
	}
	hc, err := p.ops(ctx)
	if err != nil {
		return err
	}
	return p.sendParts(ctx, hc, broadcaster, st.AccountID, m)
}

// sendParts splits the text to the Twitch limit and sends the parts as
// the sender, through the rate limit of the channel.
func (p *Platform) sendParts(ctx context.Context, hc *helix.Client, broadcaster, sender string, m connector.Message) error {
	limiter := p.chatLimiter(m.Channel)
	for _, part := range splitMessage(m.Text) {
		if err := limiter.Wait(ctx); err != nil {
			return err
		}
		_, err := hc.SendChatMessage(ctx, helix.SendChatMessageInput{
			BroadcasterID: broadcaster,
			SenderID:      sender,
			Message:       part,
		})
		if err != nil {
			return refuse(err)
		}
	}
	return nil
}

// Delete implements connector.Chat; it deletes from the channel ch, with
// the account that operates it (streamer for the home channel, bot for an
// additional one, spec twitch-multi-channel.md B4).
func (c chat) Delete(ctx context.Context, ch connector.Channel, messageID string) error {
	st, hc, err := c.p.accountFor(ctx, ch)
	if err != nil {
		return err
	}
	broadcaster, err := c.p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	return refuse(hc.DeleteChatMessage(ctx, broadcaster, st.AccountID, messageID))
}

// ircSend hands the message to the IRC session of the additional
// channels (spec twitch-multi-channel.md, B18); the session applies
// the per-channel bucket and the splitting.
func (p *Platform) ircSend(ctx context.Context, ch connector.Channel, text string) error {
	p.mu.Lock()
	s := p.irc
	p.mu.Unlock()
	if s == nil {
		return errors.New("the irc session is not connected")
	}
	return s.Send(ctx, ch, text)
}

// ircSendReply hands the reply to the IRC session of the additional
// channels (code review 2026-10-24, T5); the session applies the
// per-channel buckets and the splitting.
func (p *Platform) ircSendReply(ctx context.Context, ch connector.Channel, replyID, text string) error {
	p.mu.Lock()
	s := p.irc
	p.mu.Unlock()
	if s == nil {
		return errors.New("the irc session is not connected")
	}
	return s.SendReply(ctx, ch, replyID, text)
}

// chatLimiter returns the chat send limiter of the channel ch; each
// channel has its own bucket, so one channel is not limited by another
// (spec twitch-multi-channel.md, B9).
func (p *Platform) chatLimiter(ch connector.Channel) *rate.Limiter {
	key := strings.ToLower(string(ch))
	p.chMu.Lock()
	defer p.chMu.Unlock()
	if l, ok := p.chatLim[key]; ok {
		return l
	}
	l := rate.NewLimiter(chatRate, chatBurst)
	p.chatLim[key] = l
	return l
}

// splitMessage splits text into parts of at most maxChatMessage runes,
// breaking at the last space before the limit; a word that is longer
// than the limit is cut hard.
func splitMessage(text string) []string {
	runes := []rune(text)
	var parts []string
	for len(runes) > 0 {
		cut := len(runes)
		if cut > maxChatMessage {
			cut = maxChatMessage
			// break at the last space before the limit, if any
			for i := cut - 1; i > 0; i-- {
				if runes[i] == ' ' {
					cut = i
					break
				}
			}
		}
		parts = append(parts, string(runes[:cut]))
		runes = runes[cut:]
		// drop the space the part broke at
		if len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	return parts
}
