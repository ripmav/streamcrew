// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"log/slog"
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

// Platform is the Twitch platform (roadmap 4.3): it connects the
// EventSub WebSocket when the streamer account has a token, hands the
// mapped events to the Receiver, keeps the subscriptions in line, and
// reports the state of the stream after every connect (B11). The chat
// and moderation operations and the user lookup come with roadmap 4.4;
// until then they return connector.ErrNotImplemented.
type Platform struct {
	receiver   connector.Receiver
	auth       Auth
	helix      *httpclient.Client
	dialClient *http.Client
	base       string
	helixBase  string
	log        *slog.Logger
	poll       time.Duration

	mu        sync.Mutex
	connected bool
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

// Chat implements connector.Platform; the operations come with roadmap
// 4.4.
func (p *Platform) Chat() connector.Chat {
	return notYet{}
}

// Moderation implements connector.Platform; the operations come with
// roadmap 4.4.
func (p *Platform) Moderation() connector.Moderation {
	return notYet{}
}

// Users implements connector.Platform; the lookup comes with roadmap 4.4.
func (p *Platform) Users() connector.Users {
	return notYet{}
}

// Channel implements connector.Platform; the channel information comes
// with roadmap 4.4.
func (p *Platform) Channel(context.Context) (connector.ChannelInfo, error) {
	return connector.ChannelInfo{}, connector.ErrNotImplemented
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
		} else if st.Ready {
			if err := p.session(ctx, st); ctx.Err() == nil && err != nil {
				p.log.WarnContext(ctx, "the eventsub session ended", "error", err)
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

// notYet is a capability of the platform that roadmap 4.4 provides:
// every operation returns connector.ErrNotImplemented.
type notYet struct{}

var (
	_ connector.Chat       = notYet{}
	_ connector.Moderation = notYet{}
	_ connector.Users      = notYet{}
)

func (notYet) Send(context.Context, connector.Message) error {
	return connector.ErrNotImplemented
}

func (notYet) Delete(context.Context, string) error {
	return connector.ErrNotImplemented
}

func (notYet) Timeout(context.Context, user.Identity, time.Duration, string) error {
	return connector.ErrNotImplemented
}

func (notYet) Purge(context.Context, user.Identity) error {
	return connector.ErrNotImplemented
}

func (notYet) ClearChat(context.Context) error {
	return connector.ErrNotImplemented
}

func (notYet) Ban(context.Context, user.Identity, string) error {
	return connector.ErrNotImplemented
}

func (notYet) Unban(context.Context, user.Identity) error {
	return connector.ErrNotImplemented
}

func (notYet) Mod(context.Context, user.Identity) error {
	return connector.ErrNotImplemented
}

func (notYet) Unmod(context.Context, user.Identity) error {
	return connector.ErrNotImplemented
}

func (notYet) UserByLogin(context.Context, string) (user.Identity, error) {
	return user.Identity{}, connector.ErrNotImplemented
}

func (notYet) UserByID(context.Context, string) (user.Identity, error) {
	return user.Identity{}, connector.ErrNotImplemented
}
