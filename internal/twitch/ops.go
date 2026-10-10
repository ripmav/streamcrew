// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"errors"
	"net/http"

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

// ops builds the Helix client of the platform operations (roadmap 4.4);
// the client is stateless, so one per call keeps the platform free of
// operation state.
func (p *Platform) ops(ctx context.Context) (*helix.Client, error) {
	clientID, err := p.auth.ClientID(ctx)
	if err != nil {
		return nil, err
	}
	return helix.New(p.helix, helix.Options{
		ClientID: clientID,
		Token:    p.auth.Token,
		BaseURL:  p.helixBase,
	}), nil
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
