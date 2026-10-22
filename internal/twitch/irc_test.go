// SPDX-License-Identifier: MIT

package twitch_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// fakeIRCServer is a minimal Twitch IRC server for the IRC session tests
// (spec twitch-multi-channel.md, B16–B18): it answers the login
// sequence, sends the USERSTATE with the badges of the bot account after
// every join, answers the pings, records the PRIVMSGs of the client, and
// pushes the chat lines of the test to the client.
type fakeIRCServer struct {
	ln   net.Listener
	addr string

	mu           sync.Mutex
	conn         net.Conn
	conns        int // the number of accepted connections so far
	token        string
	nick         string
	joinedCount  int
	sentPrivmsgs []string
	// modBadge is the badges tag of the USERSTATE per channel (lowercase
	// login); a channel without an entry gets no badges.
	modBadge map[string]string
}

func newFakeIRCServer(t *testing.T, modBadge map[string]string) *fakeIRCServer {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	s := &fakeIRCServer{ln: ln, addr: ln.Addr().String(), modBadge: modBadge}
	go s.accept()
	return s
}

// accept takes the client connections in a row; the client reconnects
// on its own, so a closed connection is followed by a new one.
func (s *fakeIRCServer) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conn = conn
		s.conns++
		s.mu.Unlock()
		go s.serve(conn)
	}
}

// connectionCount is the number of accepted connections so far; a
// session that runs once holds exactly one.
func (s *fakeIRCServer) connectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// serve speaks the protocol the client needs: the capability
// acknowledgement, the welcome, the USERSTATE after the join, and the
// pong of the ping.
func (s *fakeIRCServer) serve(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
		_ = conn.Close()
	}()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "CAP REQ"):
			s.write(conn, ":tmi.twitch.tv CAP * ACK :tmi.tv/tags tmi.tv/commands tmi.tv/membership")
		case strings.HasPrefix(line, "PASS "):
			s.mu.Lock()
			s.token = strings.TrimPrefix(line, "PASS ")
			s.mu.Unlock()
		case strings.HasPrefix(line, "NICK "):
			s.mu.Lock()
			s.nick = strings.TrimPrefix(line, "NICK ")
			s.mu.Unlock()
			s.write(conn, ":tmi.twitch.tv 001 "+s.nick+" :Welcome, GLHF!")
			s.write(conn, ":tmi.twitch.tv 002 "+s.nick+" :Your host is tmi.twitch.tv")
			s.write(conn, ":tmi.twitch.tv 004 "+s.nick+" :tmi.twitch.tv")
			s.write(conn, ":tmi.twitch.tv 376 "+s.nick+" :End of /MOTD command.")
		case strings.HasPrefix(line, "JOIN "):
			s.mu.Lock()
			s.joinedCount++
			s.mu.Unlock()
			// the client multi-joins in the comma syntax: "JOIN #a,#b".
			for group := range strings.FieldsSeq(strings.TrimPrefix(line, "JOIN ")) {
				for ch := range strings.SplitSeq(group, ",") {
					ch = strings.TrimPrefix(ch, "#")
					if strings.EqualFold(ch, s.nick) {
						continue // the server never joins the own nick
					}
					s.write(conn, s.userState(ch))
				}
			}
		case strings.HasPrefix(line, "PRIVMSG "):
			s.mu.Lock()
			s.sentPrivmsgs = append(s.sentPrivmsgs, line)
			s.mu.Unlock()
		case strings.HasPrefix(line, "PING"):
			s.write(conn, ":tmi.twitch.tv PONG"+strings.TrimPrefix(line, "PING"))
		}
	}
}

// write sends a line to the connection.
func (s *fakeIRCServer) write(conn net.Conn, line string) {
	_, _ = conn.Write([]byte(line + "\r\n"))
}

// userState is the USERSTATE of the bot account in the channel ch: the
// badges tag carries the mod status from the test's table.
func (s *fakeIRCServer) userState(ch string) string {
	tags := "id=ustate-" + strings.ToLower(ch) + ";tmi-sent-ts=1"
	s.mu.Lock()
	badges := s.modBadge[strings.ToLower(ch)]
	s.mu.Unlock()
	if badges != "" {
		tags = "badges=" + badges + ";" + tags
	}
	return "@" + tags + " :tmi.twitch.tv USERSTATE #" + ch
}

// push sends a chat line to the connected client.
func (s *fakeIRCServer) push(t *testing.T, line string) {
	t.Helper()
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	require.NotNil(t, conn, "the client is not connected")
	_, err := conn.Write([]byte(line + "\r\n"))
	require.NoError(t, err)
}

// privmsgs is a copy of the PRIVMSG lines the client sent.
func (s *fakeIRCServer) privmsgs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sentPrivmsgs...)
}

// joined counts the joins of the client, over all connections.
func (s *fakeIRCServer) joined() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.joinedCount
}

// waitJoin blocks until the client has joined at least once.
func (s *fakeIRCServer) waitJoin(t *testing.T) {
	t.Helper()
	waitFor(t, 5*time.Second, func() int64 {
		if s.joined() >= 1 {
			return 1
		}
		return 0
	})
}

// ircSession builds a session against the fake server with one channel
// ("testchan") and the receiver r.
func ircSession(t *testing.T, r *fakeReceiver, srv *fakeIRCServer, channels ...string) *twitch.IRCSession {
	t.Helper()
	if len(channels) == 0 {
		channels = []string{"testchan"}
	}
	return twitch.NewIRCSession(t.Context(), twitch.IRCOptions{
		Receiver: r,
		Login:    "bot",
		Token:    func(context.Context) (string, error) { return "tok", nil },
		Channels: channels,
		BotID:    "99",
		Base:     srv.addr,
	})
}

// runSession runs the session for the test; the join is waited for
// before the test body runs.
func runSession(t *testing.T, s *twitch.IRCSession, srv *fakeIRCServer) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	srv.waitJoin(t)
}

// TestIRCSessionMessage maps an IRC chat message onto the event model
// (spec twitch-multi-channel.md, B16): the channel is stamped, the
// author carries the roles from the badges, the emote is resolved from
// the position, and the message ID rides along for the dedup.
func TestIRCSessionMessage(t *testing.T) {
	t.Parallel()
	r := &fakeReceiver{}
	srv := newFakeIRCServer(t, map[string]string{"testchan": "moderator/1"})
	runSession(t, ircSession(t, r, srv), srv)

	srv.push(t, "@badges=moderator/1;color=#ff0000;display-name=Alice;emotes=42:0-5;id=msg-1;user-id=42;tmi-sent-ts=1 :alice!alice@alice.tmi.twitch.tv PRIVMSG #testchan :Kappa hi")
	waitFor(t, 5*time.Second, func() int64 {
		if len(r.received()) == 1 {
			return 1
		}
		return 0
	})
	m := r.lastMessage(t)
	assert.Equal(t, "testchan", string(m.Channel), "the channel of the session is stamped")
	assert.Equal(t, "42", m.Author.PlatformUserID)
	assert.Equal(t, "alice", m.Author.Login)
	assert.Equal(t, "Alice", m.Author.DisplayName)
	assert.True(t, m.Author.Roles.Has(role.Moderator), "the badges carry the roles")
	assert.False(t, m.FromBot)
	assert.Equal(t, "msg-1", m.Message.ID)
	assert.Equal(t, "Kappa hi", m.Message.Text)
	assert.Equal(t, []string{"Kappa"}, m.Message.Emotes, "the emote code is read from the position")
}

// TestIRCSessionMessageFromBot flags the messages of the bot account
// (spec twitch-multi-channel.md, B13).
func TestIRCSessionMessageFromBot(t *testing.T) {
	t.Parallel()
	r := &fakeReceiver{}
	srv := newFakeIRCServer(t, map[string]string{"testchan": "moderator/1"})
	runSession(t, ircSession(t, r, srv), srv)

	srv.push(t, "@display-name=bot;id=msg-2;tmi-sent-ts=1;user-id=99 :bot!bot@bot.tmi.twitch.tv PRIVMSG #testchan :hello")
	waitFor(t, 5*time.Second, func() int64 {
		if len(r.received()) == 1 {
			return 1
		}
		return 0
	})
	assert.True(t, r.lastMessage(t).FromBot)
}

// TestIRCSessionDedup drops the repeats of the messages by their ID
// (spec twitch-multi-channel.md, B22).
func TestIRCSessionDedup(t *testing.T) {
	t.Parallel()
	r := &fakeReceiver{}
	srv := newFakeIRCServer(t, map[string]string{"testchan": "moderator/1"})
	runSession(t, ircSession(t, r, srv), srv)

	srv.push(t, "@display-name=Alice;id=msg-3;tmi-sent-ts=1 :alice!alice@alice.tmi.twitch.tv PRIVMSG #testchan :one")
	srv.push(t, "@display-name=Alice;id=msg-3;tmi-sent-ts=1 :alice!alice@alice.tmi.twitch.tv PRIVMSG #testchan :one")
	time.Sleep(100 * time.Millisecond)
	assert.Len(t, r.received(), 1, "the repeat by the ID is dropped")
}

// TestIRCSessionWhisper maps an IRC whisper onto the whisper event,
// without a channel stamp: the server sends the whisper to the account,
// not to one of its channels (spec twitch-multi-channel.md, B16).
func TestIRCSessionWhisper(t *testing.T) {
	t.Parallel()
	r := &fakeReceiver{}
	srv := newFakeIRCServer(t, map[string]string{"testchan": "moderator/1"})
	runSession(t, ircSession(t, r, srv), srv)

	srv.push(t, "@display-name=Bob;id=whisper-1;tmi-sent-ts=1;user-id=7 :bob!bob@bob.tmi.twitch.tv PRIVMSG #whisper :\x01WHISPER hello bot\x01")
	waitFor(t, 5*time.Second, func() int64 {
		if len(r.eventList()) == 1 {
			return 1
		}
		return 0
	})
	e := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatWhisper, e.Type)
	assert.Empty(t, e.Channel, "the whisper carries no channel")
	require.NotNil(t, e.User)
	assert.Equal(t, "bob", e.User.Login)
	assert.Equal(t, "hello bot", e.Details.Message.Text)
}

// TestIRCSessionClear maps an IRC CLEARMSG onto the message delete
// event (spec twitch-multi-channel.md, B16).
func TestIRCSessionClear(t *testing.T) {
	t.Parallel()
	r := &fakeReceiver{}
	srv := newFakeIRCServer(t, map[string]string{"testchan": "moderator/1"})
	runSession(t, ircSession(t, r, srv), srv)

	srv.push(t, "@target-msg-id=gone-1;tmi-sent-ts=1 :alice!alice@alice.tmi.twitch.tv PRIVMSG #testchan :\x01CLEARMSG\x01")
	waitFor(t, 5*time.Second, func() int64 {
		if len(r.eventList()) == 1 {
			return 1
		}
		return 0
	})
	e := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatMessageDelete, e.Type)
	assert.Equal(t, "testchan", string(e.Channel), "the channel of the session is stamped")
	assert.Equal(t, "gone-1", e.Details.Message.ID)
}

// TestIRCSessionModStatus reads the mod status from the USERSTATE
// badges (spec twitch-multi-channel.md, B17): the moderator channel is
// known, the channel without the badge is known and no moderator.
func TestIRCSessionModStatus(t *testing.T) {
	t.Parallel()
	r := &fakeReceiver{}
	srv := newFakeIRCServer(t, map[string]string{"modchan": "moderator/1"})
	s := ircSession(t, r, srv, "modchan", "plainchan")
	runSession(t, s, srv)

	mod, seen := s.IsModerator(connector.Channel("modchan"))
	require.True(t, seen)
	assert.True(t, mod, "the badge marks the moderator")
	mod, seen = s.IsModerator(connector.Channel("plainchan"))
	assert.False(t, mod, "without the badge the bot is no moderator")
	assert.True(t, seen, "both channels delivered their USERSTATE")
}

// TestIRCSessionSend sends through the session (spec twitch-multi-
// channel.md, B18): the message rides the PRIVMSG of the channel, a
// long text is split, and the send without a connection is an error.
func TestIRCSessionSend(t *testing.T) {
	t.Parallel()
	r := &fakeReceiver{}
	srv := newFakeIRCServer(t, map[string]string{"testchan": "moderator/1"})
	s := ircSession(t, r, srv)

	err := s.Send(t.Context(), "testchan", "no connection")
	assert.ErrorContains(t, err, "not connected")

	runSession(t, s, srv)
	require.NoError(t, s.Send(t.Context(), "testchan", "hello"))
	long := strings.Repeat("word ", 200) // 1000 runes: two parts
	require.NoError(t, s.Send(t.Context(), "testchan", long))

	var got []string
	waitFor(t, 5*time.Second, func() int64 {
		got = srv.privmsgs()
		if len(got) == 3 {
			return 1
		}
		return 0
	})
	require.Len(t, got, 3)
	assert.Contains(t, got, "PRIVMSG #testchan :hello")
	// the parts are in order and cover the text without the break space
	var joined strings.Builder
	for _, line := range got[1:] {
		joined.WriteString(strings.TrimPrefix(line, "PRIVMSG #testchan :") + " ")
	}
	assert.Equal(t, strings.TrimSuffix(long, " ")+" ", strings.TrimSuffix(joined.String(), " "), "the split keeps the text")
}

// TestIRCSessionReconnect rejoins the channels after the server drops
// the connection: the client reconnects on its own and the server gets
// the joins again.
func TestIRCSessionReconnect(t *testing.T) {
	t.Parallel()
	r := &fakeReceiver{}
	srv := newFakeIRCServer(t, map[string]string{"testchan": "moderator/1"})
	runSession(t, ircSession(t, r, srv), srv)

	srv.mu.Lock()
	if srv.conn != nil {
		_ = srv.conn.Close()
	}
	srv.mu.Unlock()
	require.Equal(t, 2, srv.joinedAfter(t, 2), "the client rejoins after the reconnect")
}

// joinedAfter waits until the joins count the given number and returns
// the count.
func (s *fakeIRCServer) joinedAfter(t *testing.T, want int) int {
	t.Helper()
	waitFor(t, 10*time.Second, func() int64 {
		if s.joined() >= want {
			return 1
		}
		return 0
	})
	return s.joined()
}
