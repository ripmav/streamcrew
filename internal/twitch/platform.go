// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

// pollInterval is how often the platform asks for the state of the
// streamer account while it is not connected (roadmap 4.3).
const pollInterval = 5 * time.Second

// Auth supplies the data of the streamer account of the channel. The
// composition root adapts the auth service to it (roadmap 4.3, task 5).
type Auth interface {
	// Streamer reports the streamer account: ready is true when a valid
	// token is available.
	Streamer(ctx context.Context) (AccountState, error)
	// Token returns a valid access token of the streamer account.
	Token(ctx context.Context) (string, error)
	// ClientID returns the client ID of the app for the Helix headers.
	ClientID(ctx context.Context) (string, error)
}

// AccountState is the state of the streamer account as the platform
// needs it.
type AccountState struct {
	// AccountID is the user ID of the streamer account.
	AccountID string
	// Login is the login name of the streamer account.
	Login string
	// Ready reports whether a valid token is available.
	Ready bool
}

// Platform is the Twitch platform (roadmap 4.3 and 4.4): it connects the
// EventSub WebSocket when the streamer account has a token, hands the
// mapped events to the Receiver, keeps the subscriptions in line, and
// reports the state of the stream after every connect (B11). It also
// carries the chat, moderation, user lookup and the channel
// information of the channel through Helix.
type Platform struct {
	receiver   connector.Receiver
	auth       Auth
	helix      *httpclient.Client
	dialClient *http.Client
	base       string
	helixBase  string
	log        *slog.Logger
	poll       time.Duration

	mu          sync.Mutex
	connected   bool
	lastAccount AccountState
}

// PlatformOptions configures NewPlatform.
type PlatformOptions struct {
	// Receiver takes the mapped events (the event service).
	Receiver connector.Receiver
	// Auth supplies the streamer account.
	Auth Auth
	// Helix is the resilient HTTP client of the Helix API (twitch.helix).
	Helix *httpclient.Client
	// DialClient is the client for the WebSocket handshake; in server
	// mode it carries the outbound allow list (Code-ADR-0019).
	DialClient *http.Client
	// Base overrides the EventSub WebSocket base (tests use the test
	// server); empty for the official one.
	Base string
	// HelixBase overrides the Helix base (tests use the test server).
	HelixBase string
	// Logger logs the lifecycle of the platform (Code-ADR-0003); if nil,
	// the log is discarded.
	Logger *slog.Logger
	// Poll is the wait between the polls of the account state (tests
	// shorten it); empty for pollInterval.
	Poll time.Duration
}

// NewPlatform creates a Platform.
func NewPlatform(o PlatformOptions) *Platform {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	dial := o.DialClient
	if dial == nil {
		dial = &http.Client{}
	}
	return &Platform{
		receiver:   o.Receiver,
		auth:       o.Auth,
		helix:      o.Helix,
		dialClient: dial,
		base:       o.Base,
		helixBase:  o.HelixBase,
		log:        log,
		poll:       o.Poll,
	}
}

// Name implements connector.Platform.
func (p *Platform) Name() platform.Name {
	return platform.Twitch
}

// Status implements connector.Platform: the platform is connected while
// its WebSocket runs.
func (p *Platform) Status() connector.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return connector.Status{Streamer: p.connected}
}

// Chat implements connector.Platform.
func (p *Platform) Chat() connector.Chat {
	return chat{p: p}
}

// Moderation implements connector.Platform.
func (p *Platform) Moderation() connector.Moderation {
	return moderation{p: p}
}

// Users implements connector.Platform.
func (p *Platform) Users() connector.Users {
	return users{p: p}
}

// Channel implements connector.Platform: the channel information from
// Helix, live while the account has a stream.
func (p *Platform) Channel(ctx context.Context) (connector.ChannelInfo, error) {
	st, err := p.account(ctx)
	if err != nil {
		return connector.ChannelInfo{}, err
	}
	hc, err := p.ops(ctx)
	if err != nil {
		return connector.ChannelInfo{}, err
	}
	info := connector.ChannelInfo{}
	ch, err := hc.GetChannel(ctx, st.AccountID)
	if err != nil {
		return connector.ChannelInfo{}, err
	}
	if ch != nil {
		info.Title = ch.Title
		info.Game = ch.GameName
	}
	streams, err := hc.GetStreams(ctx, []string{st.AccountID}, nil)
	if err != nil {
		return connector.ChannelInfo{}, err
	}
	if len(streams) > 0 {
		info.Live = true
		info.StartedAt = streams[0].CreatedAt
		viewers := min(streams[0].ViewerCount, math.MaxInt64)
		view := int64(viewers)
		info.Viewers = &view
		if streams[0].Title != "" {
			info.Title = streams[0].Title
		}
	}
	return info, nil
}

// Run implements supervisor.Runnable: it polls the state of the streamer
// account and connects the EventSub session while the account has a
// token; a lost token or a revoked one ends the session, and the poll
// starts it again with a fresh token.
func (p *Platform) Run(ctx context.Context) error {
	poll := p.poll
	if poll <= 0 {
		poll = pollInterval
	}
	for ctx.Err() == nil {
		st, err := p.auth.Streamer(ctx)
		if err != nil {
			p.log.WarnContext(ctx, "the state of the streamer account is unknown", "error", err)
		} else {
			p.setAccount(st)
			if st.Ready {
				if err := p.session(ctx, st); ctx.Err() == nil && err != nil {
					p.log.WarnContext(ctx, "the eventsub session ended", "error", err)
				}
			}
		}
		p.setConnected(false)
		select {
		case <-ctx.Done():
		case <-time.After(poll):
		}
	}
	return nil
}

// session runs one EventSub session: the subscription manager and the
// WebSocket, in line; it ends when the connection ends.
func (p *Platform) session(ctx context.Context, st AccountState) error {
	clientID, err := p.auth.ClientID(ctx)
	if err != nil {
		return err
	}
	token := func(ctx context.Context) (string, error) { return p.auth.Token(ctx) }
	hc := helix.New(p.helix, helix.Options{
		ClientID: clientID,
		Token:    token,
		BaseURL:  p.helixBase,
	})
	mgr := NewManager(ManagerOptions{Helix: hc, AccountID: st.AccountID, Logger: p.log})
	dedup, err := connector.NewDedup(connector.DefaultDedupTTL)
	if err != nil {
		return err
	}
	mapper := NewMapper(MapperOptions{Receiver: p.receiver, Dedup: dedup, Logger: p.log})
	// A revoked token: the subscriptions are reconciled right away with
	// the fresh token of the next connection (roadmap 4.3).
	mapper.WithRevoked(func(ctx context.Context) {
		if err := mgr.Reconcile(ctx); err != nil && ctx.Err() == nil {
			p.log.ErrorContext(ctx, "the post-revocation reconciliation failed", "error", err)
		}
	})
	client := New(Options{
		Base:       p.base,
		Token:      token,
		HTTPClient: p.dialClient,
		Logger:     p.log,
	})

	p.setConnected(true)
	p.log.InfoContext(ctx, "the twitch platform connected", "streamer", st.Login)
	defer func() {
		p.setConnected(false)
		p.log.InfoContext(ctx, "the twitch platform disconnected")
	}()
	p.streamState(ctx, hc, st.AccountID)

	mgrCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		if err := mgr.Run(mgrCtx); err != nil && ctx.Err() == nil {
			p.log.ErrorContext(ctx, "the subscription manager stopped", "error", err)
		}
	}()
	return client.Run(ctx, mapper)
}

// streamState hands over the state of the stream after the connect
// (B11): live if the account has a stream.
func (p *Platform) streamState(ctx context.Context, hc *helix.Client, accountID string) {
	streams, err := hc.GetStreams(ctx, []string{accountID}, nil)
	if err != nil {
		p.log.WarnContext(ctx, "the state of the stream is unknown", "error", err)
		return
	}
	live := len(streams) > 0
	if err := p.receiver.Stream(ctx, platform.Twitch, live); err != nil {
		p.log.ErrorContext(ctx, "handing over the state of the stream failed", "error", err)
	}
}

func (p *Platform) setConnected(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connected = v
}

func (p *Platform) setAccount(st AccountState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAccount = st
}

// Identity implements connector.Identities: the streamer from the last
// seen account state; the bot account is not connected in phase 4.
func (p *Platform) Identity(a connector.Account) (user.Identity, bool) {
	if a != connector.AccountStreamer {
		return user.Identity{}, false
	}
	p.mu.Lock()
	st := p.lastAccount
	p.mu.Unlock()
	if st.AccountID == "" {
		return user.Identity{}, false
	}
	return userIdentity(helix.User{ID: st.AccountID, Login: st.Login, DisplayName: st.Login}), true
}
