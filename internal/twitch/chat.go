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

// chat is the connector.Chat of the twitch platform (roadmap 4.4): it
// sends and deletes messages of the channel through Helix. The bot
// account is not connected in phase 4, so a message from it is
// refused.
type chat struct{ p *Platform }

var _ connector.Chat = chat{}

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
		return errors.Join(connector.ErrNotConnected, errors.New("the bot account is not connected"))
	}
	st, err := c.p.account(ctx)
	if err != nil {
		return err
	}
	broadcaster, err := c.p.channelID(ctx, m.Channel)
	if err != nil {
		return err
	}
	hc, err := c.p.ops(ctx)
	if err != nil {
		return err
	}
	limiter := c.p.chatLimiter(m.Channel)
	for _, part := range splitMessage(m.Text) {
		if err := limiter.Wait(ctx); err != nil {
			return err
		}
		_, err := hc.SendChatMessage(ctx, helix.SendChatMessageInput{
			BroadcasterID: broadcaster,
			SenderID:      st.AccountID,
			Message:       part,
		})
		if err != nil {
			return refuse(err)
		}
	}
	return nil
}

// Delete implements connector.Chat; it deletes from the channel ch.
func (c chat) Delete(ctx context.Context, ch connector.Channel, messageID string) error {
	st, err := c.p.account(ctx)
	if err != nil {
		return err
	}
	broadcaster, err := c.p.channelID(ctx, ch)
	if err != nil {
		return err
	}
	hc, err := c.p.ops(ctx)
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
