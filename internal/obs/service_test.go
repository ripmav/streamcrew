// SPDX-License-Identifier: MIT

package obs

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/event"
)

// fakeObs speaks obs-websocket v5 for the tests: the
// Hello/Identify/Identified negotiation, the requests of the service and
// the events of the connection.
type fakeObs struct {
	ln       net.Listener
	srv      *http.Server
	password string

	mu        sync.Mutex
	wmu       sync.Mutex // serializes the writes of the fake
	conns     int
	sessions  int // the connections that finished
	requests  []string
	scene     string
	stream    bool
	record    bool
	items     map[int]string // scene item ID -> source name
	itemID    int
	transform jsontext.Value
	ws        *websocket.Conn
}

// newFakeObs returns a fake OBS on a free port; with a password the
// authentication is checked.
func newFakeObs(t *testing.T, password string) *fakeObs {
	t.Helper()
	f := &fakeObs{
		password: password,
		scene:    "Scene",
		items:    map[int]string{},
	}
	f.srv = &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(f.handle)}
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f.ln = ln
	go func() { _ = f.srv.Serve(ln) }()
	t.Cleanup(func() { _ = f.srv.Close() })
	return f
}

// addr returns the host and the port of the fake OBS.
func (f *fakeObs) addr() (string, int) {
	h, p, _ := net.SplitHostPort(f.ln.Addr().String())
	port, _ := strconv.Atoi(p)
	return h, port
}

// connsSeen reports how many clients finished the negotiation.
func (f *fakeObs) connsSeen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns
}

// sessionsSeen reports how many connections of the fake finished.
func (f *fakeObs) sessionsSeen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions
}

// drop closes the connection of the client (B43: the reconnect).
func (f *fakeObs) drop() {
	f.mu.Lock()
	ws := f.ws
	f.mu.Unlock()
	if ws != nil {
		_ = ws.Close(websocket.StatusInternalError, "drop")
	}
}

// addSource registers the source in the current scene and returns the
// ID of its scene item.
func (f *fakeObs) addSource(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.itemID++
	f.items[f.itemID] = name
	return f.itemID
}

// streamState sets the stream state answered by GetStreamStatus.
func (f *fakeObs) streamState(active bool) {
	f.mu.Lock()
	f.stream = active
	f.mu.Unlock()
}

// lastTransform returns the requestData of the last
// SetSceneItemTransform of the connection.
func (f *fakeObs) lastTransform() jsontext.Value {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append(jsontext.Value(nil), f.transform...)
}

// sendEvent pushes the OBS event of the type with the data to the client.
func (f *fakeObs) sendEvent(typ string, data any) {
	f.mu.Lock()
	ws := f.ws
	f.mu.Unlock()
	if ws == nil {
		return
	}
	ev := map[string]any{"eventType": typ, "eventIntent": 0, "eventData": data}
	_ = f.write(context.Background(), ws, wire{Op: opEvent, D: mustMarshal(ev)})
}

// sendBadEvent pushes an OBS event whose data is not parseable into
// the event (Review 346-6).
func (f *fakeObs) sendBadEvent(typ string) {
	f.mu.Lock()
	ws := f.ws
	f.mu.Unlock()
	if ws == nil {
		return
	}
	ev := map[string]any{"eventType": typ, "eventIntent": 0, "eventData": "not an object"}
	_ = f.write(context.Background(), ws, wire{Op: opEvent, D: mustMarshal(ev)})
}

// handle runs one connection: the negotiation, then the requests and
// the responses.
func (f *fakeObs) handle(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	if !f.negotiate(r.Context(), conn) {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	f.serveConn(r.Context(), conn)
	f.mu.Lock()
	f.sessions++
	f.mu.Unlock()
}

// negotiate runs the Hello/Identify/Identified exchange of obs-websocket
// v5; a wrong authentication closes the connection with the
// AuthenticationFailed code, so that the client fails at once.
func (f *fakeObs) negotiate(ctx context.Context, conn *websocket.Conn) bool {
	salt, challenge := "test-salt", "test-challenge"
	_ = f.write(ctx, conn, wire{Op: opHello, D: mustMarshal(map[string]any{
		"obsWebSocketVersion": "v5.7",
		"rpcVersion":          1,
		"authentication":      map[string]string{"challenge": challenge, "salt": salt},
	})})
	msg, err := f.read(ctx, conn)
	if err != nil || msg.Op != opIdentify {
		return false
	}
	var identify struct {
		Authentication *string `json:"authentication"`
	}
	if err := json.Unmarshal(msg.D, &identify); err != nil {
		return false
	}
	var want, got string
	if f.password != "" {
		want = authSecret(f.password, salt, challenge)
	}
	if identify.Authentication != nil {
		got = *identify.Authentication
	}
	if got != want {
		_ = conn.Close(authFailedClose, "")
		return false
	}
	_ = f.write(ctx, conn, wire{Op: opIdentified, D: jsontext.Value(`{"negotiatedRpcVersion":1}`)})
	f.mu.Lock()
	f.conns++
	f.ws = conn
	f.mu.Unlock()
	return true
}

// serveConn answers the requests of the connection until it closes.
func (f *fakeObs) serveConn(ctx context.Context, conn *websocket.Conn) {
	for {
		msg, err := f.read(ctx, conn)
		if err != nil {
			return
		}
		if msg.Op != opRequest {
			continue
		}
		f.answer(ctx, conn, msg.D)
	}
}

// read receives one message of the connection.
func (f *fakeObs) read(ctx context.Context, conn *websocket.Conn) (*wire, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var msg wire
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// answer replies to the request with the response of the fake state.
func (f *fakeObs) answer(ctx context.Context, conn *websocket.Conn, raw jsontext.Value) {
	var req struct {
		Type string         `json:"requestType"`
		ID   string         `json:"requestId"`
		Data jsontext.Value `json:"requestData"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, req.Type)
	if req.Type == "SetSceneItemTransform" {
		f.transform = append(jsontext.Value(nil), req.Data...)
	}
	f.mu.Unlock()

	data := jsontext.Value(`{}`)
	switch req.Type {
	case "GetStreamStatus":
		data = mustMarshal(f.streamResponse())
	case "GetRecordStatus":
		data = mustMarshal(f.recordResponse())
	case "GetCurrentProgramScene":
		data = mustMarshal(map[string]any{"sceneName": f.sceneLocked()})
	case "GetSceneItemList":
		data = f.itemList()
	case "GetSceneItemSource":
		data = f.itemSource(req.Data)
	}
	_ = f.write(ctx, conn, wire{Op: opResponse, D: mustMarshal(map[string]any{
		"requestType":   req.Type,
		"requestId":     req.ID,
		"requestStatus": map[string]any{"code": statusSuccess, "result": true},
		"responseData":  data,
	})})
}

func (f *fakeObs) sceneLocked() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scene
}

func (f *fakeObs) streamResponse() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return map[string]any{"outputActive": f.stream}
}

func (f *fakeObs) recordResponse() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return map[string]any{"outputActive": f.record}
}

func (f *fakeObs) itemList() jsontext.Value {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for id, name := range f.items {
		items = append(items, map[string]any{"sceneItemId": id, "sourceName": name})
	}
	return mustMarshal(map[string]any{"sceneItems": items})
}

func (f *fakeObs) itemSource(raw jsontext.Value) jsontext.Value {
	var d struct {
		SceneItemID int `json:"sceneItemId"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return jsontext.Value(`{}`)
	}
	f.mu.Lock()
	name := f.items[d.SceneItemID]
	f.mu.Unlock()
	return mustMarshal(map[string]any{"sourceName": name})
}

// requestsSeen returns the request names of the connection, in order.
func (f *fakeObs) requestsSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// write serializes the writes of the fake to the connection.
func (f *fakeObs) write(ctx context.Context, conn *websocket.Conn, v wire) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.wmu.Lock()
	defer f.wmu.Unlock()
	return conn.Write(ctx, websocket.MessageText, data)
}

// waitUntil polls cond until it holds or the deadline runs out.
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the condition did not hold in time")
}

// collector subscribes to the bus and collects the envelopes.
type collector struct {
	mu   sync.Mutex
	envs []event.Envelope
}

// newCollector subscribes to the bus and collects the envelopes.
func newCollector(t *testing.T, bus *event.Bus) *collector {
	t.Helper()
	sub := bus.Subscribe(t.Context())
	t.Cleanup(sub.Close)
	c := &collector{}
	go func() {
		for e := range sub.C() {
			c.mu.Lock()
			c.envs = append(c.envs, e)
			c.mu.Unlock()
		}
	}()
	return c
}

// has returns the index of the first envelope of the type, -1 without one.
func (c *collector) has(typ event.Type) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, e := range c.envs {
		if e.Type == typ {
			return i
		}
	}
	return -1
}

// count reports how many envelopes of the type are collected.
func (c *collector) count(typ event.Type) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.envs {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// payload returns the payload of the envelope at the index as
// eventtype.Payload.
func (c *collector) payload(i int) eventtype.Payload {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, _ := c.envs[i].Payload.(eventtype.Payload)
	return p
}

// obsOn returns a service on the fake OBS, connected.
func obsOn(t *testing.T, fake *fakeObs) *Service {
	t.Helper()
	host, port := fake.addr()
	s := New(Ports{Host: host, Port: port, Bus: event.NewBus(nil)})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool { return s.Connected() })
	return s
}

// TestRunWithoutOBSSucceeds checks that a missing OBS at the start is a
// clear, logged fact, not an error of the core, and gives up without
// retries (B43).
func TestRunWithoutOBSSucceeds(t *testing.T) {
	t.Parallel()
	s := New(Ports{Host: "127.0.0.1", Port: 1, // a closed port
		Bus: event.NewBus(nil)})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, s.Run(ctx))
	assert.False(t, s.Connected())
}

// TestRunGivesUpOnThePassword checks that a refused authentication is a
// failed first connection, and the core gives up (B43).
func TestRunGivesUpOnThePassword(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "secret")
	host, port := fake.addr()
	s := New(Ports{Host: host, Port: port, Password: "wrong", Bus: event.NewBus(nil)})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, s.Run(ctx))
	assert.False(t, s.Connected())
	assert.Zero(t, fake.connsSeen(), "the connection never finished the negotiation")
}

// TestRunFailsTheDialOnASilentPeer checks that a peer that accepts the
// connection but never speaks cannot block Run: the dial gives up with
// the timeout, and the core gives up with it (Review 346-4).
func TestRunFailsTheDialOnASilentPeer(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		<-t.Context().Done()
		_ = conn.Close()
	}()
	host, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	s := New(Ports{Host: host, Port: port, Bus: event.NewBus(nil)})
	s.dialTimeout = 200 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run blocked on the silent peer")
	}
	assert.False(t, s.Connected())
}

// TestRunConnectsAndServes checks that the service connects to the OBS,
// seeds the state and runs the operations on it (B42), and clears the
// connection at the end.
func TestRunConnectsAndServes(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "")
	host, port := fake.addr()
	s := New(Ports{Host: host, Port: port, Bus: event.NewBus(nil)})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool { return s.Connected() })
	waitUntil(t, func() bool { return len(fake.requestsSeen()) >= 3 }) // the seed
	require.NoError(t, s.SetScene(ctx, "Cut"))
	require.NoError(t, s.StartStream(ctx))
	require.NoError(t, s.StartRecording(ctx))
	require.NoError(t, s.SaveReplay(ctx))
	reqs := fake.requestsSeen()
	for _, want := range []string{
		"SetCurrentProgramScene", "StartStream", "StartRecord", "SaveReplayBuffer",
	} {
		assert.Contains(t, reqs, want)
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	assert.False(t, s.Connected())
}

// TestRunReconnectsAfterADrop checks that a dropped connection
// reconnects after the interval (B43), re-seeds the state and publishes
// nothing for the unchanged state (B44).
func TestRunReconnectsAfterADrop(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "")
	host, port := fake.addr()
	bus := event.NewBus(nil)
	s := New(Ports{Host: host, Port: port, Bus: bus})
	s.reconnect = 20 * time.Millisecond
	c := newCollector(t, bus)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool { return s.Connected() })
	require.Equal(t, 1, fake.connsSeen())
	fake.drop()
	waitUntil(t, func() bool { return fake.connsSeen() == 2 })
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	assert.Zero(t, c.count(eventtype.ObsStreamStarted), "the state did not change")
	assert.Zero(t, c.count(eventtype.ObsSceneChanged), "the scene did not change")
}

// TestRunSeedsTheState checks that the seed of the connection takes the
// current OBS state, so that an unchanged state publishes nothing (B44):
// the stream is on before the connection, and the started state is the
// seed, not a change.
func TestRunSeedsTheState(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "")
	fake.streamState(true)
	host, port := fake.addr()
	bus := event.NewBus(nil)
	s := New(Ports{Host: host, Port: port, Bus: bus})
	c := newCollector(t, bus)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool { return s.Connected() })
	fake.sendEvent("StreamStateChanged", map[string]any{"outputState": OutputStarted})
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, c.count(eventtype.ObsStreamStarted), "the seed is not a change")
	fake.sendEvent("StreamStateChanged", map[string]any{"outputState": OutputStopped})
	waitUntil(t, func() bool { return c.count(eventtype.ObsStreamStopped) == 1 })
	cancel()
	require.NoError(t, <-done)
}

// TestRunPublishesTheOBSEvents checks that the OBS events of the
// connection run as bus events with their details (B44), regardless of
// who caused the change.
func TestRunPublishesTheOBSEvents(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "")
	id := fake.addSource("cam")
	host, port := fake.addr()
	bus := event.NewBus(nil)
	s := New(Ports{Host: host, Port: port, Bus: bus})
	c := newCollector(t, bus)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool { return s.Connected() })

	// OutputStarting is not a change (B44): no event until OutputStarted.
	fake.sendEvent("StreamStateChanged", map[string]any{"outputState": OutputStarting})
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, c.count(eventtype.ObsStreamStarted), "starting is not a change")
	fake.sendEvent("StreamStateChanged", map[string]any{"outputState": OutputStarted})
	waitUntil(t, func() bool { return c.has(eventtype.ObsStreamStarted) >= 0 })

	fake.sendEvent("StreamStateChanged", map[string]any{"outputState": OutputStopping})
	fake.sendEvent("StreamStateChanged", map[string]any{"outputState": OutputStopped})
	waitUntil(t, func() bool { return c.has(eventtype.ObsStreamStopped) >= 0 })

	fake.sendEvent("RecordStateChanged", map[string]any{"outputState": OutputStarted})
	waitUntil(t, func() bool { return c.has(eventtype.ObsRecordingStarted) >= 0 })
	fake.sendEvent("RecordStateChanged", map[string]any{"outputState": OutputStopped, "outputPath": "/rec.mp4"})
	waitUntil(t, func() bool { return c.has(eventtype.ObsRecordingStopped) >= 0 })
	assert.Equal(t, "/rec.mp4", c.payload(c.has(eventtype.ObsRecordingStopped)).Details.ObsFile.Path)

	fake.sendEvent("ReplayBufferSaved", map[string]any{"savedReplayPath": "/replay.mp4"})
	waitUntil(t, func() bool { return c.has(eventtype.ObsReplaySaved) >= 0 })
	assert.Equal(t, "/replay.mp4", c.payload(c.has(eventtype.ObsReplaySaved)).Details.ObsFile.Path)

	fake.sendEvent("CurrentProgramSceneChanged", map[string]any{"sceneName": "Cut"})
	waitUntil(t, func() bool { return c.has(eventtype.ObsSceneChanged) >= 0 })
	sc := c.payload(c.has(eventtype.ObsSceneChanged)).Details.ObsScene
	assert.Equal(t, "Cut", sc.Scene)
	assert.Equal(t, "Scene", sc.Previous, "the previous scene of the seed")

	fake.sendEvent("SceneTransitionStarted", map[string]any{"transitionName": "Cut"})
	waitUntil(t, func() bool { return c.has(eventtype.ObsTransitionStarted) >= 0 })
	assert.Equal(t, "Cut", c.payload(c.has(eventtype.ObsTransitionStarted)).Details.ObsTransition.Name)
	fake.sendEvent("SceneTransitionEnded", map[string]any{"transitionName": "Cut"})
	waitUntil(t, func() bool { return c.has(eventtype.ObsTransitionEnded) >= 0 })

	fake.sendEvent("SceneItemEnableStateChanged", map[string]any{
		"sceneName": "Scene", "sceneItemId": id, "sceneItemEnabled": false,
	})
	waitUntil(t, func() bool { return c.has(eventtype.ObsSourceVisibilityChanged) >= 0 })
	src := c.payload(c.has(eventtype.ObsSourceVisibilityChanged)).Details.ObsSource
	assert.Equal(t, "Scene", src.Scene)
	assert.Equal(t, "cam", src.Source, "the source of the scene item")
	assert.False(t, src.Visible)

	fake.sendEvent("SourceFilterEnableStateChanged", map[string]any{
		"sourceName": "cam", "filterName": "Crop", "filterEnabled": true,
	})
	waitUntil(t, func() bool { return c.has(eventtype.ObsFilterVisibilityChanged) >= 0 })
	flt := c.payload(c.has(eventtype.ObsFilterVisibilityChanged)).Details.ObsFilter
	assert.Equal(t, "cam", flt.Source)
	assert.Equal(t, "Crop", flt.Filter)
	assert.True(t, flt.Visible)

	cancel()
	require.NoError(t, <-done)
}

// TestRunPublishesEachChangeOnce checks that a repeated state of the
// connection publishes nothing, so that each change runs once (B44).
func TestRunPublishesEachChangeOnce(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "")
	host, port := fake.addr()
	bus := event.NewBus(nil)
	s := New(Ports{Host: host, Port: port, Bus: bus})
	c := newCollector(t, bus)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool { return s.Connected() })
	fake.sendEvent("StreamStateChanged", map[string]any{"outputState": OutputStarted})
	fake.sendEvent("StreamStateChanged", map[string]any{"outputState": OutputStarted})
	waitUntil(t, func() bool { return c.count(eventtype.ObsStreamStarted) == 1 })
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, c.count(eventtype.ObsStreamStarted))
	cancel()
	require.NoError(t, <-done)
}

// TestRunReportsNotConnectedDuringAReconnect checks that a dropped
// connection clears the active connection, so that an operation in the
// window of the reconnect gets ErrNotConnected, not the dead client
// (B55, Review 346-3); after the reconnect the operation runs again.
func TestRunReportsNotConnectedDuringAReconnect(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "")
	host, port := fake.addr()
	s := New(Ports{Host: host, Port: port, Bus: event.NewBus(nil)})
	s.reconnect = 500 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool { return s.Connected() })
	fake.drop()
	waitUntil(t, func() bool { return !s.Connected() })
	assert.ErrorIs(t, s.SetScene(ctx, "Cut"), ErrNotConnected, "the window of the reconnect")
	// The service publishes the new client after the seed of the
	// reconnect, so the condition is Connected again, not the
	// negotiation seen by the fake.
	waitUntil(t, func() bool { return s.Connected() })
	require.Equal(t, 2, fake.connsSeen())
	require.NoError(t, s.SetScene(ctx, "Cut"), "after the reconnect")
	cancel()
	require.NoError(t, <-done)
}

// TestRunClosesTheConnectionOnShutdown checks that the shutdown of the
// core ends the OBS session: the client closes the connection, so that
// the fake sees the session finish, and Run stops (Review 346-7).
func TestRunClosesTheConnectionOnShutdown(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "")
	host, port := fake.addr()
	s := New(Ports{Host: host, Port: port, Bus: event.NewBus(nil)})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool { return s.Connected() })
	cancel()
	require.NoError(t, <-done)
	waitUntil(t, func() bool { return fake.sessionsSeen() == 1 })
}

// TestRunSkipsAnUnparseableEvent checks that an OBS event whose data
// does not parse is dropped with a debug log, and the connection keeps
// running (Review 346-6): a broken scene event publishes no scene
// change with an empty name, and the next valid event still runs.
func TestRunSkipsAnUnparseableEvent(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "")
	host, port := fake.addr()
	bus := event.NewBus(nil)
	s := New(Ports{Host: host, Port: port, Bus: bus})
	c := newCollector(t, bus)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool { return s.Connected() })
	fake.sendBadEvent("CurrentProgramSceneChanged")
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, c.count(eventtype.ObsSceneChanged), "the broken event publishes nothing")
	fake.sendEvent("CurrentProgramSceneChanged", map[string]any{"sceneName": "Cut"})
	waitUntil(t, func() bool { return c.count(eventtype.ObsSceneChanged) == 1 })
	assert.Equal(t, "Cut", c.payload(c.has(eventtype.ObsSceneChanged)).Details.ObsScene.Scene)
	cancel()
	require.NoError(t, <-done)
}

// TestActionsWithoutAConnection checks that an operation without an
// active connection is a clear error, not a wait (B55).
func TestActionsWithoutAConnection(t *testing.T) {
	t.Parallel()
	s := New(Ports{Host: "127.0.0.1", Port: 1, Bus: event.NewBus(nil)})
	ctx := t.Context()
	ops := map[string]func() error{
		"set_scene":    func() error { return s.SetScene(ctx, "Scene") },
		"start_stream": func() error { return s.StartStream(ctx) },
		"stop_stream":  func() error { return s.StopStream(ctx) },
		"start_record": func() error { return s.StartRecording(ctx) },
		"stop_record":  func() error { return s.StopRecording(ctx) },
		"save_replay":  func() error { return s.SaveReplay(ctx) },
		"set_source":   func() error { return s.SetSource(ctx, Source{Source: "cam", Visible: new(true)}) },
		"set_filter":   func() error { return s.SetFilter(ctx, "cam", "Crop", true) },
	}
	for name, op := range ops {
		assert.ErrorIs(t, op(), ErrNotConnected, name)
	}
}

// TestSetSource checks the kind set_source of the service: the
// visibility, the position, and a source that is not in the scene (B42).
func TestSetSource(t *testing.T) {
	t.Parallel()
	t.Run("the visibility", func(t *testing.T) {
		fake := newFakeObs(t, "")
		fake.addSource("cam")
		s := obsOn(t, fake)
		require.NoError(t, s.SetSource(t.Context(), Source{Source: "cam", Visible: new(false)}))
		assert.Contains(t, fake.requestsSeen(), "SetSceneItemEnabled")
	})
	t.Run("the position", func(t *testing.T) {
		fake := newFakeObs(t, "")
		id := fake.addSource("cam")
		s := obsOn(t, fake)
		x := 10.0
		require.NoError(t, s.SetSource(t.Context(), Source{Source: "cam", X: &x}))
		// The position is a partial transform: the request carries the
		// changed coordinate only, and no Get request goes out (Review
		// 346-2).
		var got struct {
			SceneName          string             `json:"sceneName"`
			SceneItemID        int                `json:"sceneItemId"`
			SceneItemTransform map[string]float64 `json:"sceneItemTransform"`
		}
		require.NoError(t, json.Unmarshal(fake.lastTransform(), &got))
		assert.Equal(t, "Scene", got.SceneName)
		assert.Equal(t, id, got.SceneItemID)
		assert.Equal(t, map[string]float64{"positionX": 10.0}, got.SceneItemTransform, "only the position fields")
		x, y := 10.0, 20.0
		require.NoError(t, s.SetSource(t.Context(), Source{Source: "cam", X: &x, Y: &y}))
		require.NoError(t, json.Unmarshal(fake.lastTransform(), &got))
		assert.Equal(t, map[string]float64{"positionX": 10.0, "positionY": 20.0}, got.SceneItemTransform)
		assert.NotContains(t, fake.requestsSeen(), "GetSceneItemTransform")
	})
	t.Run("a source without the scene is an error", func(t *testing.T) {
		fake := newFakeObs(t, "")
		s := obsOn(t, fake)
		err := s.SetSource(t.Context(), Source{Source: "ghost", Visible: new(true)})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ghost")
	})
}

// TestSetFilter checks the kind set_filter of the service (B42).
func TestSetFilter(t *testing.T) {
	t.Parallel()
	fake := newFakeObs(t, "")
	s := obsOn(t, fake)
	require.NoError(t, s.SetFilter(t.Context(), "cam", "Crop", false))
	assert.Contains(t, fake.requestsSeen(), "SetSourceFilterEnabled")
}
