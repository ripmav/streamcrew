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
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/ripmav/streamcrew/internal/breaker"
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
// current one afterwards. The ID changes on every new session (a
// reconnect and a drop); the subscriptions ride along only with the
// session_reconnect, a drop disables them and they are re-pointed
// (F10, EventSub Reference "Which events is my WebSocket subscribed
// to?").
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

// streamPollInterval is the wait between the polls of the stream state
// while the home channel runs without the EventSub adapter (plan
// 2026-10-22); the poll replaces the stream.online/offline events.
const streamPollInterval = 30 * time.Second

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
	// notReadyLogged announces the not-ready account once (Run polls
	// every few seconds; the log must not repeat per poll).
	notReadyLogged bool
	// ircSkipLogged announces why the IRC session is not running once
	// (connect runs every poll; the log must not repeat per poll).
	ircSkipLogged bool
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
	// eventSubOn and ircOn are the switches of the chat adapters (plan
	// 2026-10-22): "off" in the options turns the adapter off, the home
	// channel runs over the adapter that is on. At least one is on (the
	// start configuration validates it).
	eventSubOn bool
	ircOn      bool
	// ircBase is the IRC server address (tests use the test server);
	// empty for the official one.
	ircBase string
	// irc is the IRC session while it runs (nil outside of it); the
	// chat send of the additional channels routes through it (B18).
	irc *IRCSession
	// socketBase is the BetterTTV socket URL for the live emote updates
	// (plan phase 2); empty for none.
	socketBase string
	// eventSubBrk and socketBrk are the circuit breakers of the two
	// external endpoints (Code-ADR-0007).
	eventSubBrk *breaker.Breaker
	socketBrk   *breaker.Breaker
	// socketStarted guards the single start of the BetterTTV socket.
	socketStarted atomic.Bool
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
	// EventSub switches the EventSub adapter: "off" turns it off, empty
	// or "on" keep it on (the default). The home channel runs over
	// EventSub while it is on, and the additional channels while the IRC
	// adapter is off.
	EventSub string
	// IRC switches the IRC adapter: "off" turns it off, empty or "on"
	// keep it on (the default). The additional channels run over the IRC
	// session while it is on, and the home channel while the EventSub
	// adapter is off.
	IRC string
	// IRCBase overrides the IRC server address (tests use the test
	// server); empty for the official one.
	IRCBase string
	// BTTVSocketBase is the BetterTTV socket URL for the live emote
	// updates (plan phase 2); empty for none.
	BTTVSocketBase string
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
		eventSubOn: adapterOn(o.EventSub),
		ircOn:      adapterOn(o.IRC),
		ircBase:    o.IRCBase,
		socketBase: o.BTTVSocketBase,
		// The circuit breakers of the two external endpoints
		// (Code-ADR-0007); the handshakes run through them.
		eventSubBrk: breaker.New("twitch.eventsub", log, httpclient.Evaluator),
		socketBrk:   breaker.New("bttv.socket", log, httpclient.Evaluator),
		chIDs:       make(map[string]string),
		chatLim:     make(map[string]*rate.Limiter),
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
			if !st.Ready {
				p.logNotReady(ctx, st.Login)
			}
			// The BetterTTV socket (plan phase 2) joins the channels once
			// they are operated, so without a token it stays off (the
			// tests); the polling with the TTL stays the safety net.
			if st.Ready && p.emotes != nil && p.socketBase != "" && p.socketStarted.CompareAndSwap(false, true) {
				go p.runBTTVSocket(ctx)
			}
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

// connect runs the home channel and the channels over the chat adapters
// (plan 2026-10-22): the home channel runs over the EventSub session
// while that adapter is on, and over the IRC session while it is off
// (the session then carries the home channel besides the additional
// ones); the additional channels run over the IRC session while that
// adapter is on, and over the EventSub sessions while it is off. With
// the EventSub adapter it returns when the home session ends.
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
	var irc *IRCSession
	p.log.DebugContext(ctx, "connect the twitch platform", "streamer", st.Login, "eventsub", p.eventSubOn, "irc", p.ircOn, "additional", len(p.channels))
	if p.ircOn {
		irc = p.runIRCSessions(ctx, st, bot, &cancels, !p.eventSubOn)
	}
	if p.eventSubOn {
		if irc == nil {
			// The additional channels run over the EventSub sessions; with
			// the IRC session there are no additional-channel subscriptions
			// (no double events).
			for _, cs := range p.additionalChannels(ctx, st, bot) {
				csCtx, cancel := context.WithCancel(ctx)
				cancels = append(cancels, cancel)
				go func(cs channelSession) {
					if err := p.channelSession(csCtx, cs); err != nil && csCtx.Err() == nil {
						p.log.WarnContext(ctx, "the eventsub session of an additional channel ended", "channel", cs.login, "error", err)
					}
				}(cs)
			}
		}
		if len(cancels) > 0 {
			p.setBot(true)
			defer p.setBot(false)
			defer p.setIRC(nil)
			defer func() {
				for _, cancel := range cancels {
					cancel()
				}
			}()
		}
		return p.channelSession(ctx, home)
	}
	// The EventSub adapter is off: the IRC session carries the home
	// channel and runs inline, so the account poll sees the connection
	// like the home EventSub session.
	if irc == nil {
		// The bot account is not ready (the skip is logged); the poll
		// retries with a fresh account state.
		return nil
	}
	p.log.DebugContext(ctx, "connect the home channel over irc", "streamer", st.Login, "bot", bot.Login, "channels", irc.logins)
	p.setBot(true)
	defer p.setBot(false)
	defer p.setIRC(nil)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	p.setConnected(true)
	defer p.setConnected(false)
	p.log.InfoContext(ctx, "the twitch platform connected over irc", "streamer", st.Login)
	defer p.log.InfoContext(ctx, "the twitch platform disconnected")
	// Without the EventSub session the stream state comes from the
	// Helix poll instead of the stream.online/offline events.
	go p.streamStatePoll(ctx, st)
	return irc.Run(ctx)
}

// adapterOn is the switch of a chat adapter: "off" turns it off,
// everything else (empty, "on") keeps it on, the default.
func adapterOn(s string) bool {
	return !strings.EqualFold(strings.TrimSpace(s), "off")
}

// runIRCSessions starts the IRC session of the channels (spec
// twitch-multi-channel.md, B16, B19): it needs the bot account, and the
// channels — with the home channel left out, as in the EventSub path —
// plus, with includeHome, the home channel the EventSub adapter left
// behind (plan 2026-10-22). It returns the session while it runs, nil
// while it does not.
func (p *Platform) runIRCSessions(ctx context.Context, st, bot AccountState, cancels *[]context.CancelFunc, includeHome bool) *IRCSession {
	logins := p.ircLogins(ctx, st.Login)
	if includeHome && st.Login != "" {
		logins = append(logins, st.Login)
	}
	if !bot.Ready {
		if len(logins) > 0 && p.ircSkip() {
			if includeHome {
				p.log.InfoContext(ctx, "the channels are not operated over irc: the bot account is not ready", "login", bot.Login)
			} else {
				p.log.InfoContext(ctx, "the additional channels are not operated over irc: the bot account is not ready", "login", bot.Login)
			}
		}
		return nil
	}
	if len(logins) == 0 {
		if p.ircSkip() {
			p.log.InfoContext(ctx, "the additional channels are not operated over irc: no channel is listed")
		}
		return nil
	}
	csCtx, cancel := context.WithCancel(ctx)
	*cancels = append(*cancels, cancel)
	for _, login := range logins {
		if _, err := p.channelID(ctx, connector.Channel(login)); err != nil {
			p.log.WarnContext(ctx, "an additional channel is unknown", "channel", login, "error", err)
		}
		if p.emotes != nil {
			go p.refreshEmotes(csCtx, login)
		}
	}
	s := NewIRCSession(ctx, IRCOptions{
		Receiver: p.receiver,
		Login:    bot.Login,
		Token:    p.auth.BotToken,
		Channels: logins,
		Home:     homeLogin(includeHome, st.Login),
		BotID:    bot.AccountID,
		Base:     p.ircBase,
		Emotes:   p.emotes,
		Logger:   p.log,
	})
	p.setIRC(s)
	if includeHome {
		// The EventSub adapter is off: the caller runs the session
		// inline (connect), so the account poll sees the connection
		// and its errors like the home EventSub session. Starting it
		// here as well would run it twice, with two connections of
		// the same nick.
		return s
	}
	go func() {
		if err := s.Run(csCtx); err != nil && csCtx.Err() == nil {
			p.log.WarnContext(ctx, "the irc session ended", "error", err)
			p.setIRC(nil)
		}
	}()
	return s
}

// homeLogin is the home login for the IRC session while it carries the
// home channel (the EventSub adapter is off), else empty.
func homeLogin(includeHome bool, login string) string {
	if includeHome {
		return login
	}
	return ""
}

// ircLogins is the additional channels the IRC session joins (spec
// twitch-multi-channel.md, B16): the listed channels, the home channel
// left out with a warning, as in the EventSub path.
func (p *Platform) ircLogins(ctx context.Context, homeLogin string) []string {
	var out []string
	for _, login := range p.channels {
		if strings.EqualFold(login, homeLogin) {
			p.log.WarnContext(ctx, "the channels section lists the home channel, it is not additional", "channel", login)
			continue
		}
		out = append(out, login)
	}
	return out
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
	// The moderator_user_id condition of channel.follow and
	// channel.moderate v2 and the user_id condition of the chat events
	// name the token owner: the broadcaster for the home channel, the
	// bot for an additional channel (EventSub Reference "Channel
	// Follow Condition", plan 2026-10-22-eventsub-reference-fix.md).
	moderator := cs.accountID
	if !cs.home {
		moderator = cs.botID
	}
	mgr := NewManager(ManagerOptions{
		Helix:     hc,
		Channels:  []string{cs.accountID},
		Whisper:   cs.whisper,
		Moderator: moderator,
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
	// A single subscription is revoked (user_removed, version_removed):
	// the session stays, the manager replaces the subscription with
	// the current table (F11). The details are already logged by the
	// client at the revocation frame.
	mapper.WithSubscriptionRevoked(func(ctx context.Context, _ RevokedSubscription) {
		if err := mgr.Reconcile(ctx); err != nil && ctx.Err() == nil {
			p.log.ErrorContext(ctx, "the post-subscription-revocation reconciliation failed", "error", err)
		}
	})
	// welcomes counts the session welcomes of this connection; the
	// first one is covered by the initial stream state below, the
	// re-welcomes of a session_reconnect report it again (code review
	// 2026-10-24, T6).
	welcomes := 0
	client := New(Options{
		Base:       p.base,
		Token:      cs.token,
		HTTPClient: p.dialClient,
		Logger:     p.log,
		Session: func(id string) {
			sessions.report(id)
			welcomes++
			// The new session's 10 s window (F12, EventSub Reference
			// "Welcome message"): a drop disabled the old session's
			// subscriptions, so they are re-pointed right after every
			// new session, not at the next 10 min interval. The
			// session_reconnect keeps its subscriptions (the status
			// stays enabled) and the reconciliation is a no-op.
			go func() {
				if err := mgr.Reconcile(ctx); err != nil && ctx.Err() == nil {
					p.log.ErrorContext(ctx, "the post-session reconciliation failed", "error", err)
				}
			}()
			// The session_reconnect replaces the session, and the stream
			// state of the old session is gone with it (code review
			// 2026-10-24, T6): the home session reports the state again
			// after a re-welcome; the first welcome is covered by the
			// initial report below.
			if cs.home && welcomes > 1 {
				go p.streamState(ctx, hc, cs.accountID)
			}
		},
		Breaker: p.eventSubBrk,
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

// streamStatePoll keeps the stream state fresh while the home channel
// runs without the EventSub adapter (plan 2026-10-22): the
// stream.online/offline events are gone, so it polls the stream over
// Helix and reports every state change, the first included.
func (p *Platform) streamStatePoll(ctx context.Context, st AccountState) {
	hc, err := p.ops(ctx)
	if err != nil {
		p.log.WarnContext(ctx, "the helix client of the stream poll is unavailable", "error", err)
		return
	}
	last, got := false, false // the first answer is reported, not only a change
	for ctx.Err() == nil {
		streams, err := hc.GetStreams(ctx, []string{st.AccountID}, nil)
		if err != nil {
			if ctx.Err() == nil {
				p.log.WarnContext(ctx, "the state of the stream is unknown", "error", err)
			}
		} else if live := len(streams) > 0; !got || live != last {
			last, got = live, true
			p.log.DebugContext(ctx, "the state of the stream changed over the poll", "live", live)
			if err := p.receiver.Stream(ctx, platform.Twitch, live); err != nil {
				p.log.ErrorContext(ctx, "handing over the state of the stream failed", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(streamPollInterval):
		}
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

// runBTTVSocket runs the BetterTTV socket (plan phase 2) for the lifetime
// of the platform: it joins the operated channels and applies the emote
// events to the catalog.
func (p *Platform) runBTTVSocket(ctx context.Context) {
	s := emotecat.NewBTTVSocket(emotecat.SocketOptions{
		Base:     p.socketBase,
		Logger:   p.log,
		Channels: p.socketChannels,
		Catalog:  p.emotes,
		Breaker:  p.socketBrk,
	})
	if err := s.Run(ctx); err != nil && ctx.Err() == nil {
		p.log.ErrorContext(ctx, "the bttv socket stopped", "error", err)
	}
}

// socketChannels is the channels the socket joins: the home channel while
// the streamer account is ready and the additional channels while their
// IDs are resolved; the socket picks up new channels on the running
// connection.
func (p *Platform) socketChannels(_ context.Context) []emotecat.SocketChannel {
	var out []emotecat.SocketChannel
	p.mu.Lock()
	st := p.lastAccount
	p.mu.Unlock()
	if st.Ready && st.Login != "" && st.AccountID != "" {
		out = append(out, emotecat.SocketChannel{Login: st.Login, ID: st.AccountID})
	}
	p.chMu.Lock()
	for _, login := range p.channels {
		if id, ok := p.chIDs[strings.ToLower(login)]; ok {
			out = append(out, emotecat.SocketChannel{Login: login, ID: id})
		}
	}
	p.chMu.Unlock()
	return out
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

// setIRC sets the IRC session while it runs (nil on the end);
// guarded by the platform's mutex like the status.
func (p *Platform) setIRC(s *IRCSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.irc = s
	if s != nil {
		p.ircSkipLogged = false
	}
}

// setConnected sets the status of the home channel session.
func (p *Platform) setConnected(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connected = v
}

// botState reports whether a bot session runs (an additional-channel or
// IRC session); while it does, the bot account is the sender for chat
// (actions.md B61).
func (p *Platform) botState() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.botConnected
}

func (p *Platform) setBot(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.botConnected = v
}

func (p *Platform) setAccount(st AccountState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := p.lastAccount.Ready != st.Ready || p.lastAccount.Login != st.Login
	p.lastAccount = st
	if st.Ready {
		p.notReadyLogged = false
	}
	if changed {
		p.log.Debug("the state of the streamer account", "login", st.Login, "ready", st.Ready)
	}
}

// ircSkip reports whether the skip of the IRC session may be logged now:
// once per transition, not per poll (connect runs every poll).
func (p *Platform) ircSkip() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ircSkipLogged {
		return false
	}
	p.ircSkipLogged = true
	return true
}

// logNotReady announces that the streamer account is not ready — for
// example login_required after a new required scope until the user logs
// in again (ADR-0014) — once per transition, not per poll.
func (p *Platform) logNotReady(ctx context.Context, login string) {
	p.mu.Lock()
	if p.notReadyLogged {
		p.mu.Unlock()
		return
	}
	p.notReadyLogged = true
	p.mu.Unlock()
	p.log.WarnContext(ctx, "the streamer account is not ready, log in again", "login", login)
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
