// SPDX-License-Identifier: MIT

package overlay_test

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/settings"
)

func jsonV2Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonV2Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// startServer starts an overlay server on a free port; the test stops
// it again.
func startServer(t *testing.T, cfg overlay.Config) *overlay.Server {
	t.Helper()
	cfg.Listen = "127.0.0.1:0"
	srv, err := overlay.NewServer(cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	<-srv.Listening()
	_, ok := srv.Addr()
	require.True(t, ok)
	return srv
}

// base returns the HTTP base URL of the server.
func base(t *testing.T, srv *overlay.Server) string {
	t.Helper()
	addr, ok := srv.Addr()
	require.True(t, ok)
	return "http://" + addr.String()
}

// testServer returns a running server with the given section; token
// gates the endpoints when set, mediaDir is the profile media directory
// and caps the rights the core has.
func testServer(t *testing.T, sec settings.Overlay, token, mediaDir string, caps capability.Set) (*overlay.Server, *overlay.Service) {
	t.Helper()
	svc := testService(t, sec)
	srv := startServer(t, overlay.Config{
		Version:      "test",
		Token:        token,
		Service:      svc,
		MediaDir:     mediaDir,
		Capabilities: caps,
	})
	return srv, svc
}

// dial connects a runtime to the default endpoint's WebSocket.
func dial(t *testing.T, srv *overlay.Server) *websocket.Conn {
	t.Helper()
	addr, _ := srv.Addr()
	conn, _, err := websocket.Dial(context.Background(), fmt.Sprintf("ws://%s/overlay/%s/ws", addr.String(), settings.DefaultEndpointID), nil) //nolint:bodyclose // the body is consumed by Dial
	require.NoError(t, err)
	return conn
}

// get fetches the URL with the test context.
func get(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// readMsg reads one text message with a timeout and returns its type
// and the JSON of its data field.
func readMsg(t *testing.T, conn *websocket.Conn) (typ string, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := conn.Read(ctx)
	require.NoError(t, err)
	var m overlay.Message
	require.NoError(t, jsonV2Unmarshal(b, &m))
	if m.Data != nil {
		data, err = jsonV2Marshal(m.Data)
		require.NoError(t, err)
	}
	return m.Type, data
}

// writeMsg sends one text message.
func writeMsg(t *testing.T, conn *websocket.Conn, m overlay.Message) {
	t.Helper()
	data, err := jsonV2Marshal(m)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, data))
}

// TestOverlayPageServesTheRuntime covers B2 and B3: the page of the
// named endpoint carries the injected endpoint and the embedded bundle;
// an unknown endpoint answers 404.
func TestOverlayPageServesTheRuntime(t *testing.T) {
	srv, _ := testServer(t, settings.Overlay{}, "", "", capability.Set{})

	resp := get(t, base(t, srv)+"/overlay/default")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"id":"default"`)
	assert.Contains(t, string(body), "window.__OVERLAY__")
	assert.Contains(t, string(body), "state.widgets")

	resp = get(t, base(t, srv)+"/overlay/nope")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestOverlayTokenProtectsTheEndpoints covers B5 and B48: with a set
// token the page, the WebSocket and the media URLs require the value as
// the query parameter token, otherwise they answer 401.
func TestOverlayTokenProtectsTheEndpoints(t *testing.T) {
	media := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(media, "a.txt"), []byte("hello"), 0o600))
	srv, _ := testServer(t, settings.Overlay{}, "secret", media, capability.Set{})

	// The page: without and with the wrong token is 401.
	for _, path := range []string{"/overlay/default", "/overlay/default?token=nope"} {
		resp := get(t, base(t, srv)+path)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, path)
	}
	// The page: with the right token is open.
	resp := get(t, base(t, srv)+"/overlay/default?token=secret")
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// The media.
	resp = get(t, base(t, srv)+"/media/media/a.txt")
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// The WebSocket: the upgrade is refused without the token, and the
	// runtime's URL with the token upgrades (B5).
	_, resp, dialErr := websocket.Dial(context.Background(), base(t, srv)+"/overlay/default/ws", nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, dialErr)
	// On a failed dial the response body is the error page and is
	// closed here; on success it is the connection itself and is
	// closed with the conn below.
	conn, resp, err := websocket.Dial(context.Background(), base(t, srv)+"/overlay/default/ws?token=secret", nil)
	if resp != nil && err != nil {
		_ = resp.Body.Close()
	}
	require.NoError(t, err)
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// TestOverlayWSHandshakeAndStateSync covers B3, B4, B7 and B49: the
// client gets hello and state.widgets, answers ready and gets the state
// again; two clients see the same state.
func TestOverlayWSHandshakeAndStateSync(t *testing.T) {
	srv, _ := testServer(t, settings.Overlay{
		Widgets: []settings.OverlayWidget{
			{ID: "goal1", Kind: "goal", Visible: true, Position: settings.Position{Model: settings.PositionSimple, Point: "top"}},
		},
	}, "", "", capability.Set{})

	c1 := dial(t, srv)
	t.Cleanup(func() { _ = c1.Close(websocket.StatusNormalClosure, "") })
	c2 := dial(t, srv)
	t.Cleanup(func() { _ = c2.Close(websocket.StatusNormalClosure, "") })

	// Both clients see the same state (B49).
	for _, c := range []*websocket.Conn{c1, c2} {
		typ, data := readMsg(t, c)
		require.Equal(t, overlay.TypeHello, typ)
		var hello overlay.HelloData
		require.NoError(t, jsonV2Unmarshal(data, &hello))
		assert.Equal(t, settings.DefaultEndpointID, hello.Endpoint)
		typ, data = readMsg(t, c)
		require.Equal(t, overlay.TypeStateWidgets, typ)
		var state overlay.StateWidgetsData
		require.NoError(t, jsonV2Unmarshal(data, &state))
		require.Len(t, state.Widgets, 1)
		assert.Equal(t, "goal1", state.Widgets[0].ID)
	}

	// ready repeats the full state (B4, B7).
	writeMsg(t, c1, overlay.Message{Type: overlay.TypeReady})
	typ, _ := readMsg(t, c1)
	assert.Equal(t, overlay.TypeHello, typ)
}

// TestItemShowAndRemoveOverTheWire covers B4 and B11 on the wire: the
// client gets item.show and item.remove for the active items.
func TestItemShowAndRemoveOverTheWire(t *testing.T) {
	srv, svc := testServer(t, settings.Overlay{}, "", "", capability.Set{})
	c := dial(t, srv)
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	readMsg(t, c) // hello
	readMsg(t, c) // state.widgets

	id, err := svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{
		Kind: "text", Content: map[string]any{"text": "hi"},
		Position: settings.Position{Model: settings.PositionSimple, Point: "center"},
		Layer:    3,
	})
	require.NoError(t, err)
	typ, data := readMsg(t, c)
	require.Equal(t, overlay.TypeItemShow, typ)
	var item overlay.ItemData
	require.NoError(t, jsonV2Unmarshal(data, &item))
	assert.Equal(t, id, item.ID)
	assert.Equal(t, "text", item.Kind)
	assert.Equal(t, 3, item.Layer)

	require.True(t, svc.RemoveItem(context.Background(), settings.DefaultEndpointID, id))
	typ, data = readMsg(t, c)
	require.Equal(t, overlay.TypeItemRemove, typ)
	var rm overlay.RemoveData
	require.NoError(t, jsonV2Unmarshal(data, &rm))
	assert.Equal(t, id, rm.ID)
}

// TestItemEndedOverTheWire covers B14: the item.ended of the runtime
// removes the item from the active set and the other runtimes get the
// remove (B49).
func TestItemEndedOverTheWire(t *testing.T) {
	srv, svc := testServer(t, settings.Overlay{}, "", "", capability.Set{})
	c := dial(t, srv)
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	readMsg(t, c)
	readMsg(t, c)

	id, err := svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{
		Kind: "audio", Position: settings.Position{Model: settings.PositionSimple, Point: "center"},
	})
	require.NoError(t, err)
	readMsg(t, c) // item.show

	writeMsg(t, c, overlay.Message{Type: overlay.TypeItemEnded, Data: overlay.EndedData{ID: id}})
	typ, data := readMsg(t, c) // the remove proves the server processed it
	require.Equal(t, overlay.TypeItemRemove, typ)
	var rm overlay.RemoveData
	require.NoError(t, jsonV2Unmarshal(data, &rm))
	assert.Equal(t, id, rm.ID)
	assert.False(t, svc.RemoveItem(context.Background(), settings.DefaultEndpointID, id)) // already gone
	assert.Empty(t, svc.ActiveItems(settings.DefaultEndpointID))
}

// TestMediaServesFilesWithRanges covers B6 and B47: the media route
// serves the media directory and the released roots with
// http.ServeContent (range requests), and answers 404 for unknown
// roots and for paths that escape a root.
func TestMediaServesFilesWithRanges(t *testing.T) {
	media := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(media, "note.txt"), []byte("0123456789"), 0o600))
	released := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(released, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(released, "sub", "img.png"), []byte("PNGDATA"), 0o600))

	srv := startServer(t, overlay.Config{
		Version:      "test",
		Service:      testService(t, settings.Overlay{}),
		MediaDir:     media,
		Capabilities: hostFSCapability(t),
		Root: func(name string) (string, bool) {
			if name == "assets" {
				return released, true
			}
			return "", false
		},
	})

	// The media directory under its root name.
	resp := get(t, base(t, srv)+"/media/media/note.txt")
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "0123456789", string(body))
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")

	// A range request (B6).
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base(t, srv)+"/media/media/note.txt", nil)
	require.NoError(t, err)
	req.Header.Set("Range", "bytes=2-5")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Equal(t, "2345", string(body))

	// The released file root.
	resp = get(t, base(t, srv)+"/media/assets/sub/img.png")
	body, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "PNGDATA", string(body))

	// Unknown root, missing file, directory and traversal: 404 (B47).
	for _, path := range []string{
		"/media/nope/sub/img.png",
		"/media/media/missing.txt",
		"/media/media/sub",
		"/media/media/..%2F..%2Fetc%2Fpasswd",
		"/media/media/../../../etc/passwd",
	} {
		resp := get(t, base(t, srv)+path)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, path)
	}
}

// TestMediaRequiresHostFSCoversB6: without the right the media route
// serves nothing, even for an existing file.
func TestMediaRequiresHostFSCoversB6(t *testing.T) {
	media := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(media, "a.txt"), []byte("hello"), 0o600))
	s, err := capability.NewSet(capability.NetOutbound) // host:fs is off
	require.NoError(t, err)
	srv, _ := testServer(t, settings.Overlay{}, "", media, s)
	resp := get(t, base(t, srv)+"/media/media/a.txt")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestMediaServesRegisteredMIMETypes covers B6: a known extension gets
// its registered content type.
func TestMediaServesRegisteredMIMETypes(t *testing.T) {
	media := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(media, "video.mp4"), []byte("MP4"), 0o600))
	srv, _ := testServer(t, settings.Overlay{}, "", media, hostFSCapability(t))
	resp := get(t, base(t, srv)+"/media/media/video.mp4")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "video/mp4")
}

// TestOverlayServerServesUntilTheContextEnds covers B1: Run returns
// cleanly when the context ends.
func TestOverlayServerServesUntilTheContextEnds(t *testing.T) {
	svc := testService(t, settings.Overlay{})
	ctx, cancel := context.WithCancel(context.Background())
	srv, err := overlay.NewServer(overlay.Config{Listen: "127.0.0.1:0", Version: "test", Service: svc})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	<-srv.Listening()
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("the server did not stop")
	}
}

// TestOverlayServerFailsOnABrokenListenAddress: a port that is taken
// already is a permanent error, like the API server.
func TestOverlayServerFailsOnABrokenListenAddress(t *testing.T) {
	// Occupy the port for the whole test.
	var lc net.ListenConfig
	take, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, take.Close()) })

	svc := testService(t, settings.Overlay{})
	srv, err := overlay.NewServer(overlay.Config{Listen: take.Addr().String(), Version: "test", Service: svc})
	require.NoError(t, err)
	err = srv.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listen")
}

// hostFSCapability returns a capability set with host:fs.
func hostFSCapability(t *testing.T) capability.Set {
	t.Helper()
	s, err := capability.NewSet(capability.HostFS)
	require.NoError(t, err)
	return s
}
