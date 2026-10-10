// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"errors"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/helix"
)

// maxChatMessage is the limit Twitch takes for one chat message, in
// runes (roadmap 4.4, actions.md B65).
const maxChatMessage = 500

// chat is the connector.Chat of the twitch platform (roadmap 4.4): it
// sends and deletes messages of the channel through Helix. The bot
// account is not connected in phase 4, so a message from it is
// refused.
type chat struct{ p *Platform }

var _ connector.Chat = chat{}

// Send implements connector.Chat: it splits a text that is longer than
// Twitch allows and keeps to the rate limits of the platform through
// the resilient HTTP client (actions.md B65).
func (c chat) Send(ctx context.Context, m connector.Message) error {
	if m.From == connector.AccountBot {
		return errors.Join(connector.ErrNotConnected, errors.New("the bot account is not connected"))
	}
	st, err := c.p.account(ctx)
	if err != nil {
		return err
	}
	hc, err := c.p.ops(ctx)
	if err != nil {
		return err
	}
	for _, part := range splitMessage(m.Text) {
		_, err := hc.SendChatMessage(ctx, helix.SendChatMessageInput{
			BroadcasterID: st.AccountID,
			SenderID:      st.AccountID,
			Message:       part,
		})
		if err != nil {
			return refuse(err)
		}
	}
	return nil
}

// Delete implements connector.Chat.
func (c chat) Delete(ctx context.Context, messageID string) error {
	st, err := c.p.account(ctx)
	if err != nil {
		return err
	}
	hc, err := c.p.ops(ctx)
	if err != nil {
		return err
	}
	return refuse(hc.DeleteChatMessage(ctx, st.AccountID, st.AccountID, messageID))
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
