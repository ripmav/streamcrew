// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	emotecat "github.com/ripmav/streamcrew/internal/emote"
	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

// sessionState hands the EventSub session ID from the WebSocket
// client to the subscription manager: the client reports the ID of
// every connection, the manager waits for the first and reads the
// current one afterwards (the ID changes on every session
// reconnect, the subscriptions ride along with the session).
type sessionState struct {
	mu     sync.Mutex
	id     string
	notify chan struct{}
}

func newSessionState() *sessionState {
	return &sessionState{notify: make(chan struct{})}
}

// report is the Session callback of the WebSocket client.
func (s *sessionState) report(id string) {
	s.mu.Lock()
	s.id = id
	n := s.notify
	s.notify = nil
	s.mu.Unlock()
	if n != nil {
		close(n)
	}
}

// current blocks until the first session ID is reported and returns
// the current ID after that.
func (s *sessionState) current(ctx context.Context) (string, error) {
	s.mu.Lock()
	id, n := s.id, s.notify
	s.mu.Unlock()
	if id != "" {
		return id, nil
	}
	if n == nil {
		return "", errors.New("eventsub: the session is gone")
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-n:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id, nil
}

// peek is the currently reported ID, "" before the first report.
func (s *sessionState) peek() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// pollInterval is how often the platform asks for the state of the
// streamer account while it is not connected (roadmap 4.3).
const pollInterval = 5 * time.Second

// emoteRefresh is the wait between the refreshes of the emote catalog
// (roadmap 4.4, task 9); it matches the catalog's TTL.
const emoteRefresh = time.Hour

// Auth supplies the data of the accounts of the channel (roadmap 4.3,
// task 5, 4.6). The composition root adapts the auth service to it.
type Auth interface {
	// Streamer reports the streamer account: ready is true when a valid
	// token is available.
	Streamer(ctx context.Context) (AccountState, error)
	// Token returns a valid access token of the streamer account.
	Token(ctx context.Context) (string, error)
	// ClientID returns the client ID of the app for the Helix headers.
	ClientID(ctx context.Context) (string, error)
	// Bot reports the bot account (spec twitch-multi-channel.md, B2):
	// ready is true when a valid token is available; without a bot
	// account, an empty AccountState.
	Bot(ctx context.Context) (AccountState, error)
	// BotToken returns a valid access token of the bot account; the
	// additional channels run with it (B4).
	BotToken(ctx context.Context) (string, error)
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
	// emotes resolves the emote IDs of the chat messages (roadmap 4.4,
	// task 9); nil without a catalog.
	emotes *emotecat.Catalog

	mu           sync.Mutex
	connected    bool
	botConnected bool
	lastAccount  AccountState
	// sessions is the session state of the current session (nil
	// outside of a session).
	sessions *sessionState
	// chMu guards chIDs and chatLim.
	chMu sync.Mutex
	// chIDs is the resolved broadcaster IDs of the additional channels,
	// keyed by their lowercase login (spec twitch-multi-channel.md, B4).
	chIDs map[string]string
	// chatLim is the per-channel chat send limiter, keyed by the channel's
	// lowercase login (empty for the home channel); each channel has its
	// own bucket (spec twitch-multi-channel.md, B9).
	chatLim map[string]*rate.Limiter
	// channels are the logins of the additional channels (spec
	// twitch-multi-channel.md, B1); empty for the home channel only.
	channels []string
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
	// Emotes is the emote catalog the mapper resolves emote IDs with
	// (roadmap 4.4, task 9); nil for the raw code from the text.
	Emotes *emotecat.Catalog
	// Channels are the logins of the additional channels the bot operates
	// in besides the home channel (spec twitch-multi-channel.md, B1); the
	// home channel must not be listed (B3).
	Channels []string
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
		emotes:     o.Emotes,
		channels:   o.Channels,
		chIDs:      make(map[string]string),
		chatLim:    make(map[string]*rate.Limiter),
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
	return connector.Status{Streamer: p.connected, Bot: p.botConnected}
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
				if err := p.connect(ctx, st); ctx.Err() == nil && err != nil {
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

// channelSession is the EventSub session of one channel (spec
// twitch-multi-channel.md, B4): the home channel runs with the streamer
// token and the full breadth of functions, the additional channels with
// the bot token and the moderator rights. The sessions are one per
// channel, so every event of a session is the event of its channel (B5).
type channelSession struct {
	// channel is the channel of the session; empty for the home channel.
	channel connector.Channel
	// login is the login of the channel (emote catalog, the log).
	login string
	// accountID is the user ID of the channel (the broadcaster
	// condition of the subscriptions).
	accountID string
	// whisper is the user ID that receives the whisper messages; empty
	// for none.
	whisper string
	// botID is the user ID of the bot account; its messages are flagged
	// FromBot (B13); empty if the bot is not connected.
	botID string
	// token is the access token of the session (streamer or bot).
	token func(ctx context.Context) (string, error)
	// home is the session of the home channel: it reports the stream
	// state and keeps the platform's status up.
	home bool
	// ready reports whether the channels are operated (B3); nil for the
	// home channel.
	ready func(ctx context.Context) (bool, error)
}

// connect runs the EventSub session of the home channel and the sessions
// of the additional channels; it returns when the home session ends.
func (p *Platform) connect(ctx context.Context, st AccountState) error {
	token := func(ctx context.Context) (string, error) { return p.auth.Token(ctx) }
	bot, botErr := p.auth.Bot(ctx)
	if botErr != nil {
		p.log.WarnContext(ctx, "the state of the bot account is unknown", "error", botErr)
		bot = AccountState{}
	}
	home := channelSession{
		login:     st.Login,
		accountID: st.AccountID,
		whisper:   st.AccountID,
		token:     token,
		home:      true,
	}
	if bot.Ready {
		home.botID = bot.AccountID
	}
	var cancels []context.CancelFunc
	for _, cs := range p.additionalChannels(ctx, st, bot) {
		csCtx, cancel := context.WithCancel(ctx)
		cancels = append(cancels, cancel)
		go func(cs channelSession) {
			if err := p.channelSession(csCtx, cs); err != nil && csCtx.Err() == nil {
				p.log.WarnContext(ctx, "the eventsub session of an additional channel ended", "channel", cs.login, "error", err)
			}
		}(cs)
	}
	if len(cancels) > 0 {
		p.setBot(true)
		defer p.setBot(false)
		defer func() {
			for _, cancel := range cancels {
				cancel()
			}
		}()
	}
	return p.channelSession(ctx, home)
}

// additionalChannels selects the sessions of the additional channels
// (spec twitch-multi-channel.md, B2, B3): the channel must exist, and the
// bot must be a moderator of it; an entry that names the home channel is
// not an additional channel, so it is left out with a warning (B3).
func (p *Platform) additionalChannels(ctx context.Context, st, bot AccountState) []channelSession {
	if len(p.channels) == 0 {
		return nil
	}
	clientID, err := p.auth.ClientID(ctx)
	if err != nil {
		p.log.WarnContext(ctx, "the additional channels are not operated", "error", err)
		return nil
	}
	botToken := func(ctx context.Context) (string, error) { return p.auth.BotToken(ctx) }
	hc := helix.New(p.helix, helix.Options{ClientID: clientID, Token: botToken, BaseURL: p.helixBase})
	var out []channelSession
	for _, login := range p.channels {
		if strings.EqualFold(login, st.Login) {
			p.log.WarnContext(ctx, "the channels section lists the home channel, it is not additional", "channel", login)
			continue
		}
		id, err := p.channelID(ctx, connector.Channel(login))
		if err != nil {
			p.log.WarnContext(ctx, "an additional channel is not operated", "channel", login, "error", err)
			continue
		}
		mod, err := p.isModerator(ctx, hc, id, bot.AccountID)
		if err != nil {
			p.log.WarnContext(ctx, "the moderator status of the bot is unknown", "channel", login, "error", err)
			continue
		}
		if !mod {
			p.log.WarnContext(ctx, "the bot is not a moderator of the channel, it is not operated", "channel", login, "status", "mod_missing")
			continue
		}
		out = append(out, channelSession{
			channel:   connector.Channel(login),
			login:     login,
			accountID: id,
			whisper:   bot.AccountID,
			botID:     bot.AccountID,
			token:     botToken,
			ready:     func(ctx context.Context) (bool, error) { return p.isModerator(ctx, hc, id, bot.AccountID) },
		})
	}
	return out
}

// isModerator reports whether the user with the userID is a moderator of
// the channel with the channelID (spec twitch-multi-channel.md, B3); the
// check is repeated at every reconciliation by the ready callback.
func (p *Platform) isModerator(ctx context.Context, hc *helix.Client, channelID, userID string) (bool, error) {
	mods, err := hc.GetModerators(ctx, channelID)
	if err != nil {
		return false, err
	}
	for _, m := range mods {
		if m.ID == userID {
			return true, nil
		}
	}
	return false, nil
}

// channelSession runs the EventSub session of the channel: the
// subscription manager and the WebSocket, in line; it ends when the
// connection ends.
func (p *Platform) channelSession(ctx context.Context, cs channelSession) error {
	clientID, err := p.auth.ClientID(ctx)
	if err != nil {
		return err
	}
	hc := helix.New(p.helix, helix.Options{
		ClientID: clientID,
		Token:    cs.token,
		BaseURL:  p.helixBase,
	})
	sessions := newSessionState()
	if cs.home {
		p.mu.Lock()
		p.sessions = sessions
		p.mu.Unlock()
	}
	specs := desired
	if !cs.home {
		specs = ChannelSpecs()
	}
	mgr := NewManager(ManagerOptions{
		Helix:     hc,
		Channels:  []string{cs.accountID},
		Whisper:   cs.whisper,
		Specs:     specs,
		Logger:    p.log,
		SessionID: sessions.current,
		Ready:     cs.ready,
	})
	dedup, err := connector.NewDedup(connector.DefaultDedupTTL)
	if err != nil {
		return err
	}
	mapper := NewMapper(MapperOptions{
		Receiver:     p.receiver,
		Dedup:        dedup,
		Logger:       p.log,
		Emotes:       p.emotes,
		EmoteChannel: cs.login,
		Channel:      cs.channel,
		BotID:        cs.botID,
	})
	// A revoked token: the subscriptions are reconciled right away with
	// the fresh token of the next connection (roadmap 4.3).
	mapper.WithRevoked(func(ctx context.Context) {
		if err := mgr.Reconcile(ctx); err != nil && ctx.Err() == nil {
			p.log.ErrorContext(ctx, "the post-revocation reconciliation failed", "error", err)
		}
	})
	client := New(Options{
		Base:       p.base,
		Token:      cs.token,
		HTTPClient: p.dialClient,
		Logger:     p.log,
		Session:    sessions.report,
	})

	if cs.home {
		p.log.InfoContext(ctx, "the twitch platform connected", "streamer", cs.login)
		defer func() {
			p.setConnected(false)
			p.log.InfoContext(ctx, "the twitch platform disconnected")
		}()
		p.streamState(ctx, hc, cs.accountID)
		// The status goes up only after the initial stream state was
		// handed over, so a connected status never precedes the state
		// event.
		p.setConnected(true)
	} else {
		p.log.InfoContext(ctx, "the additional channel connected", "channel", cs.login)
		defer p.log.InfoContext(ctx, "the additional channel disconnected", "channel", cs.login)
	}

	mgrCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		if err := mgr.Run(mgrCtx); err != nil && ctx.Err() == nil {
			p.log.ErrorContext(ctx, "the subscription manager stopped", "error", err)
		}
	}()
	if p.emotes != nil {
		go p.refreshEmotes(mgrCtx, cs.login)
	}
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

// refreshEmotes keeps the emote catalog of the channel fresh (roadmap
// 4.4, task 9): it refreshes at once and on every tick until the context
// ends.
func (p *Platform) refreshEmotes(ctx context.Context, channel string) {
	p.emotes.Refresh(ctx, channel)
	tick := time.NewTicker(emoteRefresh)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.emotes.Refresh(ctx, channel)
		}
	}
}

// SessionID is the ID of the currently connected EventSub session
// (from the session_welcome); empty outside of a session. The chaos
// tests use it to address the client of the mock server.
func (p *Platform) SessionID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions == nil {
		return ""
	}
	return p.sessions.peek()
}

func (p *Platform) setConnected(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connected = v
}

func (p *Platform) setBot(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.botConnected = v
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
