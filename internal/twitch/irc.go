// SPDX-License-Identifier: MIT

package twitch

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"slices"

	"golang.org/x/time/rate"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	emotecat "github.com/ripmav/streamcrew/internal/emote"
)

// defaultIRCBase is the official Twitch IRC server (TLS).
const defaultIRCBase = "irc.chat.twitch.tv:6697"

// The IRC read deadlines: the login sequence is answered right away.
// The idle deadline must stay above the interval of the server PINGs:
// the first PING arrives about five minutes after the connection (Twitch
// does not document the interval), any received data resets the
// deadline, and a dead connection is dropped after it.
const (
	ircWelcomeTimeout = 30 * time.Second
	ircIdleTimeout    = 6 * time.Minute
)

// The backoff of the reconnects of the IRC session: it starts at the
// minimum and doubles with every consecutive failure, capped.
const (
	ircBackoffMin = time.Second
	ircBackoffMax = 30 * time.Second
)

// IRCOptions configures NewIRCSession.
type IRCOptions struct {
	// Receiver takes the mapped events (the event service).
	Receiver connector.Receiver
	// Login is the login of the bot account, the IRC nick.
	Login string
	// Token supplies a valid access token of the bot account; it is
	// called for every connection, so a refreshed token is picked up
	// on the next reconnect, like the EventSub client does.
	Token func(ctx context.Context) (string, error)
	// Channels are the logins of the channels the session joins; the home
	// channel must not be listed twice (spec twitch-multi-channel.md,
	// B16).
	Channels []string
	// Home is the login of the home channel in Channels (plan
	// 2026-10-22): its events carry no channel stamp (the home channel,
	// as in the EventSub path) and its mod status is not checked. Empty
	// while the session carries only additional channels.
	Home string
	// BotID is the user ID of the bot account; its messages are flagged
	// FromBot (B13). Empty for none.
	BotID string
	// Base overrides the IRC server address (tests use a plain-TCP test
	// server); empty for the official server (TLS).
	Base string
	// Emotes resolves the emote IDs of the chat messages (roadmap 4.4,
	// task 9); nil for the raw code from the text.
	Emotes *emotecat.Catalog
	// Logger logs the lifecycle of the session (Code-ADR-0003); if nil,
	// the log is discarded.
	Logger *slog.Logger
}

// IRCSession is the IRC chat adapter of the additional channels (spec
// twitch-multi-channel.md, B15–B20): one IRC connection of the bot
// account joins the channels, and the chat messages, whispers and
// message deletes ride the IRC tags onto the event model through the
// same mapping as the EventSub path. The mod status comes from the
// badges tag of the USERSTATE the server sends after every join (B17).
type IRCSession struct {
	receiver connector.Receiver
	login    string
	token    func(ctx context.Context) (string, error)
	botID    string
	base     string
	emotes   *emotecat.Catalog
	log      *slog.Logger
	// logins are the lowercase logins the session joins, in the order
	// they were given.
	logins []string
	// home is the lowercase login of the home channel while the session
	// carries it (the EventSub adapter is off, plan 2026-10-22); its
	// events carry no channel stamp and its mod status is not checked.
	home string
	// mappers is the EventSub mapper per channel (lowercase login); it
	// stamps the channel, resolves the emotes, dedups and flags the
	// bot's own messages, like the EventSub path does.
	mappers map[string]*Mapper
	// whisperDedup drops the repeats of the whispers by their message ID;
	// the whispers carry no channel (the server sends them to the account,
	// not to one of its channels), so they go to the receiver without a
	// channel stamp.
	whisperDedup *connector.Dedup
	// lims is the per-channel chat send limiter (spec twitch-
	// multi-channel.md, B9), one bucket per channel.
	lims map[string]*rate.Limiter
	// mods is the mod status per channel (lowercase login) from the
	// USERSTATE badges; seen reports which channels delivered one.
	mods map[string]bool
	seen map[string]bool
	// mu guards lims, mods, seen and conn; conn is set while a
	// connection runs and nil outside of it.
	mu   sync.Mutex
	conn net.Conn
	// writeMu serializes the writes of the connection (keepalive and
	// the sends).
	writeMu sync.Mutex
}

// NewIRCSession creates an IRCSession.
func NewIRCSession(ctx context.Context, o IRCOptions) *IRCSession {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	mappers := make(map[string]*Mapper, len(o.Channels))
	logins := make([]string, 0, len(o.Channels))
	for _, login := range o.Channels {
		key := strings.ToLower(login)
		logins = append(logins, key)
		dedup, err := connector.NewDedup(connector.DefaultDedupTTL)
		if err != nil {
			log.ErrorContext(ctx, "the dedup of an irc channel is unavailable", "channel", login, "error", err)
			continue
		}
		ch := connector.Channel(login)
		if strings.EqualFold(login, o.Home) {
			ch = "" // the home channel carries no stamp
		}
		mappers[key] = NewMapper(MapperOptions{
			Receiver:     o.Receiver,
			Dedup:        dedup,
			Logger:       log,
			Emotes:       o.Emotes,
			EmoteChannel: login,
			Channel:      ch,
			BotID:        o.BotID,
		})
	}
	whisperDedup, err := connector.NewDedup(connector.DefaultDedupTTL)
	if err != nil {
		log.ErrorContext(ctx, "the dedup of the whispers is unavailable", "error", err)
		whisperDedup = nil
	}
	return &IRCSession{
		receiver:     o.Receiver,
		login:        o.Login,
		token:        o.Token,
		botID:        o.BotID,
		home:         strings.ToLower(o.Home),
		base:         o.Base,
		emotes:       o.Emotes,
		log:          log,
		logins:       logins,
		mappers:      mappers,
		whisperDedup: whisperDedup,
		lims:         make(map[string]*rate.Limiter, len(o.Channels)),
		mods:         make(map[string]bool, len(o.Channels)),
		seen:         make(map[string]bool, len(o.Channels)),
	}
}

// Run connects the IRC session and runs it until the context ends: a
// dropped connection is reconnected with a backoff and a fresh token,
// and the channels are rejoined after every (re)connect.
func (s *IRCSession) Run(ctx context.Context) error {
	var backoff time.Duration
	for ctx.Err() == nil {
		token, err := s.token(ctx)
		if err != nil {
			return err
		}
		runErr := s.runConnection(ctx, token)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if backoff == 0 {
			backoff = ircBackoffMin
		} else if backoff < ircBackoffMax {
			backoff *= 2
		}
		s.log.WarnContext(ctx, "the irc connection ended, it is reconnected", "error", runErr, "backoff", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
	}
	return nil
}

// runConnection runs one connection: the login sequence, the joins,
// the keepalive and the read loop; it returns when the connection ends.
func (s *IRCSession) runConnection(ctx context.Context, token string) error {
	base := s.base
	tlsConf := true
	if base == "" {
		base = defaultIRCBase
	} else {
		tlsConf = false // the test server speaks plain TCP
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if tlsConf {
		d := tls.Dialer{NetDialer: &dialer, Config: &tls.Config{}}
		conn, err = d.DialContext(ctx, "tcp", base)
	} else {
		conn, err = dialer.Dial("tcp", base)
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.conn = nil
		s.mu.Unlock()
		_ = conn.Close()
	}()
	// A finished context closes the connection, so the read unblocks at
	// the shutdown and does not wait the read deadline.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	// The capability negotiation (dev.twitch.tv/docs/chat/irc): the
	// tags capability requires the commands capability in the same
	// request, and CAP END terminates the negotiation before the login
	// (IRCv3). The server only knows the twitch.tv/* names; the legacy
	// tmi.tv/* names are answered with a NAK (field test 2026-10-22).
	if err := s.writeLines(conn,
		"CAP REQ :twitch.tv/tags twitch.tv/commands",
		"CAP END",
		"PASS oauth:"+token,
		"NICK "+s.login,
	); err != nil {
		return err
	}
	if err := conn.SetReadDeadline(time.Now().Add(ircWelcomeTimeout)); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "the irc session connected", "channels", s.logins)
	defer s.log.InfoContext(ctx, "the irc session disconnected")

	r := textproto.NewReader(bufio.NewReader(conn))
	welcome := false
	for {
		line, err := r.ReadLine()
		if err != nil {
			return err
		}
		if err := conn.SetReadDeadline(time.Now().Add(ircIdleTimeout)); err != nil {
			return err
		}
		// Every line rides the log (truncated): the tags carry the
		// message data, the server lines the state of the connection
		// (Code-ADR-0003, debug level for the path tracing).
		s.log.DebugContext(ctx, "the irc line", "line", truncateLog(line, 300))
		if !welcome && strings.Contains(line, " 001 ") {
			welcome = true
			if lines := joinLines(s.logins); len(lines) > 0 {
				s.log.DebugContext(ctx, "the irc session joins", "channels", s.logins)
				if err := s.writeLines(conn, lines...); err != nil {
					return err
				}
			}
			continue
		}
		msg, err := parseIRCLine(line)
		if err != nil {
			continue // an unparseable line is dropped, like the EventSub path
		}
		switch msg.command {
		case "PING":
			if err := s.writeLine(conn, "PONG "+msg.trailing); err != nil {
				return err
			}
		case "USERSTATE":
			s.onUserState(ctx, msg, channelOf(msg))
		case "PRIVMSG":
			s.onPrivmsg(ctx, msg, channelOf(msg))
		case "NOTICE":
			// shortcut: a refused send (for example a followers-only
			// channel, spec B20) arrives as an asynchronous NOTICE; it
			// is logged with the channel and the reason, not returned
			// to the send call.
			s.log.WarnContext(ctx, "the irc server reports a notice", "channel", channelOf(msg), "notice", msg.trailing)
		case "RECONNECT":
			return errors.New("the irc server asked for a reconnect")
		}
	}
}

// writeLines sends the lines of the login sequence.
func (s *IRCSession) writeLines(conn net.Conn, lines ...string) error {
	for _, l := range lines {
		if err := s.writeLine(conn, l); err != nil {
			return err
		}
	}
	return nil
}

// writeLine sends one line of the connection.
func (s *IRCSession) writeLine(conn net.Conn, line string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := conn.Write([]byte(line + "\r\n"))
	return err
}

// joinLines is one JOIN per channel: the server only takes the first
// channel of a multi-channel JOIN (field test 2026-10-22), so every
// channel rides its own line.
func joinLines(logins []string) []string {
	if len(logins) == 0 {
		return nil
	}
	lines := make([]string, len(logins))
	for i, l := range logins {
		lines[i] = "JOIN #" + l
	}
	return lines
}

// channelOf is the channel of a server line (the first parameter,
// without the leading #).
func channelOf(msg ircMsg) string {
	if len(msg.params) == 0 {
		return ""
	}
	return strings.TrimPrefix(msg.params[0], "#")
}

// Send sends a chat message to the channel ch over the session; the
// per-channel bucket (spec twitch-multi-channel.md, B9) applies, a text
// longer than the platform takes is split, like the Helix path.
func (s *IRCSession) Send(ctx context.Context, ch connector.Channel, text string) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return errors.New("the irc session is not connected")
	}
	key := strings.ToLower(string(ch))
	l := s.limiter(key)
	s.log.DebugContext(ctx, "the irc send", "channel", key, "runes", len([]rune(text)))
	for _, part := range splitMessage(text) {
		if err := l.Wait(ctx); err != nil {
			return err
		}
		if err := s.writeLine(conn, "PRIVMSG #"+key+" :"+part); err != nil {
			return err
		}
	}
	return nil
}

// truncateLog cuts the line to n runes for the debug log; a cut line
// is marked with an ellipsis.
func truncateLog(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// limiter returns the send limiter of the channel key, creating the
// bucket if needed (spec twitch-multi-channel.md, B9).
func (s *IRCSession) limiter(key string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.lims[key]; ok {
		return l
	}
	l := rate.NewLimiter(chatRate, chatBurst)
	s.lims[key] = l
	return l
}

// IsModerator reports whether the bot account is a moderator of the
// channel ch and whether the session has seen its USERSTATE (spec
// twitch-multi-channel.md, B17).
func (s *IRCSession) IsModerator(ch connector.Channel) (mod, seen bool) {
	key := strings.ToLower(string(ch))
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mods[key], s.seen[key]
}

// onUserState reads the mod status of the bot account in the channel
// from the badges tag (spec twitch-multi-channel.md, B17); the server
// sends one after every join, so the status is re-read on reconnects.
func (s *IRCSession) onUserState(ctx context.Context, msg ircMsg, channel string) {
	key := strings.ToLower(channel)
	if key == s.home {
		return // the home channel is operated without the mod check
	}
	if _, ok := s.mappers[key]; !ok {
		return
	}
	_, mod := parseBadgesTag(msg.tags["badges"])["moderator"]
	s.mu.Lock()
	s.mods[key] = mod
	first := !s.seen[key]
	s.seen[key] = true
	s.mu.Unlock()
	if first {
		if mod {
			s.log.InfoContext(ctx, "the bot is a moderator of the channel", "channel", channel)
		} else {
			// Without the mod status the channel is still operated;
			// the warning marks it (spec B17).
			s.log.WarnContext(ctx, "the bot is not a moderator of the channel", "channel", channel, "status", "mod_missing")
		}
	}
}

// onPrivmsg dispatches a PRIVMSG: the whispers come to the account at
// #whisper with the \01WHISPER\01 escape, the deletes as
// \01CLEARMSG\01, everything else is the chat of the channel.
func (s *IRCSession) onPrivmsg(ctx context.Context, msg ircMsg, channel string) {
	text := msg.trailing
	switch {
	case channel == "whisper" && strings.HasPrefix(text, "\x01WHISPER "):
		s.onWhisper(ctx, msg, strings.TrimSuffix(strings.TrimPrefix(text, "\x01WHISPER "), "\x01"))
	case text == "\x01CLEARMSG\x01":
		s.onClear(ctx, msg, channel)
	default:
		s.onMessage(ctx, msg, channel, text)
	}
}

// onMessage maps an IRC chat message onto the event model, with the
// same mapping as the EventSub path (emotes, badges, dedup, FromBot).
func (s *IRCSession) onMessage(ctx context.Context, msg ircMsg, channel, text string) {
	key := strings.ToLower(channel)
	mapper, ok := s.mappers[key]
	if !ok {
		s.log.WarnContext(ctx, "drop a message of an unjoined channel", "channel", channel)
		return
	}
	if id := msg.tags["id"]; id != "" && !mapper.dedup.First(id) {
		s.log.DebugContext(ctx, "drop a repeated irc message", "channel", channel, "id", id)
		return
	}
	s.log.DebugContext(ctx, "the irc chat message", "channel", channel, "user", msg.prefixName)
	p := chatMessage{
		ID:              msg.tags["id"],
		Message:         text,
		UserID:          msg.tags["user-id"],
		UserLogin:       strings.ToLower(msg.prefixName),
		UserDisplayName: msg.tags["display-name"],
		Color:           msg.tags["color"],
		Badges:          badges(msg.tags["badges"]),
		Emotes:          emotes(msg.tags["emotes"]),
	}
	if err := mapper.rec.Message(ctx, mapper.incoming(p)); err != nil {
		s.log.WarnContext(ctx, "handing over a chat message failed", "channel", channel, "error", err)
	}
}

// onWhisper maps an IRC whisper onto the whisper event, like the
// EventSub path (user.whisper.message). The whispers carry no channel
// (the server sends them to the account, not to one of its channels),
// so they go to the receiver without a channel stamp.
func (s *IRCSession) onWhisper(ctx context.Context, msg ircMsg, text string) {
	if id := msg.tags["id"]; id != "" && s.whisperDedup != nil && !s.whisperDedup.First(id) {
		return
	}
	p := whisper{
		Message:         text,
		UserID:          msg.tags["user-id"],
		UserLogin:       strings.ToLower(msg.prefixName),
		UserDisplayName: msg.tags["display-name"],
	}
	ev := connector.Event{
		Platform: platform.Twitch,
		Type:     eventtype.ChatWhisper,
		User:     new(identity(p.user())),
		Details:  eventtype.Details{Message: &eventtype.Message{Text: p.Message}},
	}
	if err := s.receiver.Event(ctx, ev); err != nil {
		s.log.WarnContext(ctx, "handing over a whisper failed", "error", err)
	}
}

// onClear maps an IRC CLEARMSG onto the message delete event, like the
// EventSub path (channel.chat.message_delete).
func (s *IRCSession) onClear(ctx context.Context, msg ircMsg, channel string) {
	key := strings.ToLower(channel)
	mapper, ok := s.mappers[key]
	if !ok {
		return
	}
	ev := connector.Event{
		Platform: platform.Twitch,
		Type:     eventtype.ChatMessageDelete,
		Details:  eventtype.Details{Message: &eventtype.Message{ID: msg.tags["target-msg-id"]}},
	}
	if err := mapper.rec.Event(ctx, ev); err != nil {
		s.log.WarnContext(ctx, "handing over a message delete failed", "channel", channel, "error", err)
	}
}

// ircMsg is a parsed line of the IRC protocol: the tags, the source
// (the nick of the sender), the command, the parameters and the
// trailing parameter.
type ircMsg struct {
	tags       map[string]string
	prefixName string
	command    string
	params     []string
	trailing   string
}

// parseIRCLine parses a line of the IRC protocol with the Twitch tags
// ("@a=b;c=d :nick!user@host COMMAND param :trailing").
func parseIRCLine(line string) (ircMsg, error) {
	var m ircMsg
	m.tags = make(map[string]string)
	rest := line
	if strings.HasPrefix(rest, "@") {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return m, errors.New("the irc line has tags without a command")
		}
		for pair := range strings.SplitSeq(rest[1:i], ";") {
			k, v, _ := strings.Cut(pair, "=")
			m.tags[k] = v
		}
		rest = rest[i+1:]
	}
	if strings.HasPrefix(rest, ":") {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return m, errors.New("the irc line has a source without a command")
		}
		// The source is "nick!user@host" or a bare host (the server
		// lines); the nick is what the mapping reads.
		src := rest[1:i]
		m.prefixName = src
		if n, _, _ := strings.Cut(src, "!"); n != "" {
			m.prefixName = n
		}
		rest = rest[i+1:]
	}
	// The trailing is everything after the first " :" parameter, with
	// its spaces; the rest are the plain parameters.
	body := rest
	if i := strings.Index(rest, " :"); i >= 0 {
		body = rest[:i]
		m.trailing = rest[i+2:]
	}
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return m, errors.New("the irc line has no command")
	}
	m.command = strings.ToUpper(fields[0])
	m.params = fields[1:]
	return m, nil
}

// badges is the badges tag ("a/1,b/2") as the EventSub badge list, in
// the stable sorted order of the badge sets.
func badges(raw string) []badge {
	b := parseBadgesTag(raw)
	if len(b) == 0 {
		return nil
	}
	out := make([]badge, 0, len(b))
	for set, version := range b {
		out = append(out, badge{SetID: fmt.Sprintf("%s/%d", set, version)})
	}
	slices.SortFunc(out, func(a, b badge) int { return strings.Compare(a.SetID, b.SetID) })
	return out
}

// parseBadgesTag parses the badges tag into the badge sets with their
// versions.
func parseBadgesTag(raw string) map[string]int {
	out := map[string]int{}
	for pair := range strings.SplitSeq(raw, ",") {
		if pair == "" {
			continue
		}
		set, v, _ := strings.Cut(pair, "/")
		n, _ := strconv.Atoi(v)
		out[set] = n
	}
	return out
}

// emotes is the emotes tag ("id:start-end,...") as the EventSub emote
// list.
func emotes(raw string) []emote {
	if raw == "" {
		return nil
	}
	var out []emote
	for e := range strings.SplitSeq(raw, ",") {
		id, rangeStr, _ := strings.Cut(e, ":")
		startStr, endStr, _ := strings.Cut(rangeStr, "-")
		start, _ := strconv.Atoi(startStr)
		end, _ := strconv.Atoi(endStr)
		out = append(out, emote{ID: id, Start: start, End: end})
	}
	return out
}
