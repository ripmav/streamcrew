// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

// account returns the streamer account of the channel; the error
// wraps connector.ErrNotConnected when the account has no valid token
// (roadmap 4.4).
func (p *Platform) account(ctx context.Context) (AccountState, error) {
	st, err := p.auth.Streamer(ctx)
	if err != nil {
		return AccountState{}, err
	}
	if !st.Ready {
		return AccountState{}, errors.Join(connector.ErrNotConnected, errors.New("the streamer account has no valid token"))
	}
	return st, nil
}

// ops builds the Helix client of the platform operations (roadmap 4.4)
// with the token of the streamer account; the client is stateless, so one
// per call keeps the platform free of operation state.
func (p *Platform) ops(ctx context.Context) (*helix.Client, error) {
	return p.opsToken(ctx, p.auth.Token)
}

// opsToken builds a Helix client that signs its requests with the given
// token (the streamer's or the bot's, spec twitch-multi-channel.md B4).
func (p *Platform) opsToken(ctx context.Context, token func(ctx context.Context) (string, error)) (*helix.Client, error) {
	clientID, err := p.auth.ClientID(ctx)
	if err != nil {
		return nil, err
	}
	return helix.New(p.helix, helix.Options{
		ClientID: clientID,
		Token:    token,
		BaseURL:  p.helixBase,
	}), nil
}

// accountFor resolves the account that operates the channel ch (spec
// twitch-multi-channel.md, B4): the streamer's for the home channel, the
// bot's for an additional channel, together with a Helix client for its
// token. With an additional channel it wraps ErrNotConnected when the bot
// account has no valid token.
func (p *Platform) accountFor(ctx context.Context, ch connector.Channel) (AccountState, *helix.Client, error) {
	if ch == "" {
		st, err := p.account(ctx)
		if err != nil {
			return AccountState{}, nil, err
		}
		hc, err := p.ops(ctx)
		return st, hc, err
	}
	bot, err := p.auth.Bot(ctx)
	if err != nil {
		return AccountState{}, nil, err
	}
	if !bot.Ready {
		return AccountState{}, nil, errors.Join(connector.ErrNotConnected, errors.New("the bot account has no valid token"))
	}
	hc, err := p.opsToken(ctx, p.auth.BotToken)
	return bot, hc, err
}

// refuse wraps a 403 answer of Helix with connector.ErrRefused: the
// platform refused the operation, e.g. for lack of rights (actions.md
// B86).
func refuse(err error) error {
	var se *httpclient.StatusError
	if errors.As(err, &se) && se.StatusCode == http.StatusForbidden {
		return errors.Join(connector.ErrRefused, err)
	}
	return err
}

// notFound reports whether Helix answered 404.
func notFound(err error) bool {
	var se *httpclient.StatusError
	return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
}

// channelID resolves the broadcaster ID of the channel ch (spec twitch-multi-channel.md, B4).
// The home channel is the streamer's own account; an additional channel is looked up by
// its login over Helix and cached, so the Helix call happens once per channel.
func (p *Platform) channelID(ctx context.Context, ch connector.Channel) (string, error) {
	if ch == "" {
		st, err := p.account(ctx)
		if err != nil {
			return "", err
		}
		return st.AccountID, nil
	}
	login := strings.ToLower(string(ch))
	p.chMu.Lock()
	if id, ok := p.chIDs[login]; ok {
		p.chMu.Unlock()
		return id, nil
	}
	p.chMu.Unlock()
	if _, err := p.account(ctx); err != nil {
		return "", err
	}
	hc, err := p.ops(ctx)
	if err != nil {
		return "", err
	}
	users, err := hc.GetUsers(ctx, nil, []string{string(ch)})
	if err != nil {
		return "", fmt.Errorf("channel %s: %w", ch, err)
	}
	if len(users) == 0 {
		return "", fmt.Errorf("channel %q: %w", ch, connector.ErrUnknownUser)
	}
	id := users[0].ID
	p.chMu.Lock()
	p.chIDs[login] = id
	p.chMu.Unlock()
	return id, nil
}
