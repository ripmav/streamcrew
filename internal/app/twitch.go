// SPDX-License-Identifier: MIT

package app

import (
	"context"

	"github.com/ripmav/streamcrew/internal/auth"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// twitchStreamAuth adapts the auth service to twitch.Auth: the streamer
// account of the channel (roadmap 4.3, task 5).
type twitchStreamAuth struct {
	s *auth.Service
}

// Streamer implements twitch.Auth.
func (a twitchStreamAuth) Streamer(ctx context.Context) (twitch.AccountState, error) {
	sts, err := a.s.Status(ctx)
	if err != nil {
		return twitch.AccountState{}, err
	}
	for _, st := range sts {
		if st.Platform == platform.Twitch && st.Role == connector.AccountStreamer {
			return twitch.AccountState{
				AccountID: st.UserID,
				Login:     st.Login,
				Ready:     st.State == auth.StateOK,
			}, nil
		}
	}
	return twitch.AccountState{}, nil
}

// Token implements twitch.Auth.
func (a twitchStreamAuth) Token(ctx context.Context) (string, error) {
	return a.s.Token(ctx, platform.Twitch, connector.AccountStreamer)
}

// ClientID implements twitch.Auth.
func (a twitchStreamAuth) ClientID(ctx context.Context) (string, error) {
	id, found, err := a.s.ClientID(ctx, platform.Twitch, connector.AccountStreamer)
	if err != nil {
		return "", err
	}
	if !found {
		return "", auth.ErrNoAccount
	}
	return id, nil
}
