// SPDX-License-Identifier: MIT

// The behavior test of the discord service (roadmap 7.5, spec
// overlays.md, B37, A3): a fake gateway speaks the minimum of the
// gateway protocol (hello, identify, heartbeat) and plays the voice
// states, and the service must track the occupants of the configured
// channel and publish the discord.voice events.
package discord_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/discord"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/event"
)

// wait is the patience of the waits on the events and the occupants.
const wait = 5 * time.Second

// fakeGateway is the Discord gateway the tests point discordgo at: it
// answers the protocol handshake and plays the packets the test pushes.
// Every frame goes through one writer, because the socket does not
// allow concurrent writes.
type fakeGateway struct {
	// gateway is the URL of the /gateway endpoint of the fake REST API;
	// the test points discordgo.EndpointGateway at it.
	gateway string

	mu         sync.Mutex
	handshaken chan struct{}
	onceHS     sync.Once
	writeCh    chan []byte
	seq        int
}

// newFakeGateway starts the fake REST API and the fake gateway socket.
func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	f := &fakeGateway{handshaken: make(chan struct{}), writeCh: make(chan []byte, 16)}
	up := websocket.Upgrader{}
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		require.NoError(t, err)
		// The single writer of the socket: the handshake answers of the
		// read loop and the packets the test pushes share it.
		go func() {
			for frame := range f.writeCh {
				if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
					return
				}
			}
		}()
		f.write(map[string]any{
			"op": 10,
			"d":  map[string]any{"heartbeat_interval": 41250},
		})
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var pkt struct {
				Op int `json:"op"`
			}
			if err := json.Unmarshal(data, &pkt); err != nil {
				return
			}
			switch pkt.Op {
			case 1: // heartbeat: the ack keeps the connection alive
				f.write(map[string]any{"op": 11, "s": 1, "d": nil})
			case 2: // identify: the ready packet ends the handshake
				f.write(map[string]any{
					"op": 0, "s": 1, "t": "READY",
					"d": map[string]any{"v": 10, "session_id": "ses", "user": map[string]any{"id": "me", "username": "bot"}},
				})
				f.onceHS.Do(func() { close(f.handshaken) })
			}
		}
	}))
	t.Cleanup(ws.Close)
	// The gateway answer is the socket as a ws:// URL, as Discord
	// answers it.
	wsURL := strings.Replace(ws.URL, "http://", "ws://", 1)
	f.gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"url": wsURL})
	})).URL + "/gateway"
	return f
}

// write offers one frame to the socket writer.
func (f *fakeGateway) write(frame map[string]any) {
	data, err := json.Marshal(frame)
	if err != nil {
		panic(err)
	}
	f.writeCh <- data
}

// push plays one gateway event packet to the client.
func (f *fakeGateway) push(t *testing.T, name string, d any) {
	t.Helper()
	f.mu.Lock()
	f.seq++
	seq := f.seq
	f.mu.Unlock()
	f.write(map[string]any{"op": 0, "s": seq, "t": name, "d": d})
}

// waitForHandshake waits until the client has identified and the ready
// packet has been played and consumed: the client reads the ready packet
// in its handshake, so a packet pushed before it is consumed is lost;
// the settle wait below leaves the loopback read the win.
func (f *fakeGateway) waitForHandshake(t *testing.T) {
	t.Helper()
	select {
	case <-f.handshaken:
	case <-time.After(wait):
		t.Fatal("the gateway handshake did not arrive")
	}
	time.Sleep(100 * time.Millisecond)
}

// recv takes the next event type of the channel or fails on the wait.
func recv(t *testing.T, ch <-chan event.Type) event.Type {
	t.Helper()
	select {
	case typ := <-ch:
		return typ
	case <-time.After(wait):
		t.Fatal("the event did not arrive")
		return ""
	}
}

// waitOccupants waits until the service carries n occupants.
func waitOccupants(t *testing.T, svc *discord.Service, n int) []discord.Occupant {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if occ := svc.Occupants(); len(occ) == n {
			return occ
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the occupant count %d did not arrive", n)
	return nil
}

// eventsOf subscribes to the discord voice events of the bus.
func eventsOf(t *testing.T, bus *event.Bus) <-chan event.Type {
	t.Helper()
	ch := make(chan event.Type, 8)
	sub := bus.Subscribe(t.Context(),
		event.WithName("discord_test"),
		event.WithTypes(eventtype.DiscordVoiceJoined, eventtype.DiscordVoiceLeft))
	go func() {
		for env := range sub.C() {
			ch <- env.Type
		}
	}()
	return ch
}

// voiceState is the voice state packet of the user with the name in the
// channel; an empty channel is the leave.
func voiceState(channel, userID, name string) map[string]any {
	d := map[string]any{"guild_id": "g1", "channel_id": channel, "user_id": userID}
	if name != "" {
		d["member"] = map[string]any{"user": map[string]any{"id": userID, "username": name}}
	}
	return d
}

// TestVoiceChannel covers the tracking of the configured channel: the
// initial state seeds without events, a join publishes joined, and a
// move away or a leave publishes left (B37).
func TestVoiceChannel(t *testing.T) {
	t.Parallel()
	fake := newFakeGateway(t)
	oldGateway := discordgo.EndpointGateway
	discordgo.EndpointGateway = fake.gateway
	t.Cleanup(func() { discordgo.EndpointGateway = oldGateway })

	bus := event.NewBus(nil)
	evt := eventsOf(t, bus)
	svc := discord.New(discord.Ports{
		Token:   "tok",
		Channel: "chan1",
		Bus:     bus,
		Logger:  slog.New(slog.DiscardHandler),
	})
	require.True(t, svc.Active())

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	fake.waitForHandshake(t)
	// The initial voice state of the channel: the occupant is seeded
	// without an event.
	fake.push(t, "GUILD_CREATE", map[string]any{
		"id":           "g1",
		"voice_states": []any{voiceState("chan1", "u0", "pre")},
	})
	occ := waitOccupants(t, svc, 1)
	assert.Equal(t, "pre", occ[0].Name)
	assert.Equal(t, "chan1", occ[0].Channel)

	// A join publishes discord.voice.joined with the Discord name.
	fake.push(t, "VOICE_STATE_UPDATE", voiceState("chan1", "u1", "alice"))
	assert.Equal(t, eventtype.DiscordVoiceJoined, recv(t, evt))
	occ = waitOccupants(t, svc, 2)
	names := map[string]bool{}
	for _, o := range occ {
		names[o.Name] = true
	}
	assert.True(t, names["alice"])

	// A move to another channel publishes discord.voice.left.
	fake.push(t, "VOICE_STATE_UPDATE", voiceState("chan2", "u1", ""))
	assert.Equal(t, eventtype.DiscordVoiceLeft, recv(t, evt))
	occ = waitOccupants(t, svc, 1)
	assert.Equal(t, "pre", occ[0].Name)

	// A state of another channel publishes nothing.
	fake.push(t, "VOICE_STATE_UPDATE", voiceState("chan2", "u2", "bob"))
	select {
	case typ := <-evt:
		t.Fatalf("an unexpected event %s", typ)
	case <-time.After(200 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(wait):
		t.Fatal("the service did not stop")
	}
}

// TestInactiveWithoutToken covers the inactivity without a token or a
// channel (B37): the service stays inactive and its run returns at once.
func TestInactiveWithoutToken(t *testing.T) {
	t.Parallel()
	svc := discord.New(discord.Ports{
		Channel: "chan1",
		Logger:  slog.New(slog.DiscardHandler),
	})
	assert.False(t, svc.Active())
	require.NoError(t, svc.Run(t.Context()))
	assert.Empty(t, svc.Occupants())
}
