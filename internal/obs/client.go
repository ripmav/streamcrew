// SPDX-License-Identifier: MIT

// Package obs is the OBS Studio connection of the core (roadmap 7.4,
// spec overlays.md, B42 to B45): the obs-websocket v5 client of the
// package (client.go), the events of the connection on the bus (B44)
// and the operations the streaming_software action runs (B42, B55). The
// connection starts with the core; without an OBS server at the start
// it is a clear, logged fact, not an error of the core, and it is given
// up (no retries). A dropped connection reconnects every five seconds
// (B43).
package obs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/ripmav/streamcrew/internal/netguard"
)

// The opcodes of obs-websocket v5 (protocol.md).
const (
	opHello      = 0
	opIdentify   = 1
	opIdentified = 2
	opEvent      = 5
	opRequest    = 6
	opResponse   = 7
)

// The protocol constants of obs-websocket v5 (protocol.md).
const (
	// subscriptionAll is the eventSubscriptions value that receives
	// every non-high-volume event.
	subscriptionAll = 0x7FF
	// statusSuccess is the requestStatus code of a successful request.
	statusSuccess = 100
	// authFailedClose is the close code of a refused authentication.
	authFailedClose = 4009
	// requestTimeout bounds a request without a response.
	requestTimeout = 10 * time.Second
	// dialTimeout bounds the dial and the handshake of the
	// connection: a silent peer must not block the service (Review
	// 346-4).
	dialTimeout = 10 * time.Second
)

// The output states of obs-websocket v5 (protocol.md,
// OBS_WEBSOCKET_OUTPUT_*): the values of outputState in the
// StreamStateChanged and RecordStateChanged events. Only OutputStarted
// and OutputStopped are state changes for the service; the other states
// are ignored (B44).
const (
	// OutputStarting is the output is starting.
	OutputStarting = "OBS_WEBSOCKET_OUTPUT_STARTING"
	// OutputStarted is the output is running; the "on" state.
	OutputStarted = "OBS_WEBSOCKET_OUTPUT_STARTED"
	// OutputStopping is the output is stopping.
	OutputStopping = "OBS_WEBSOCKET_OUTPUT_STOPPING"
	// OutputStopped is the output is stopped; the "off" state.
	OutputStopped = "OBS_WEBSOCKET_OUTPUT_STOPPED"
	// OutputPaused is the output is paused.
	OutputPaused = "OBS_WEBSOCKET_OUTPUT_PAUSED"
	// OutputResumed is the output is resumed.
	OutputResumed = "OBS_WEBSOCKET_OUTPUT_RESUMED"
	// OutputReconnecting is the output is reconnecting.
	OutputReconnecting = "OBS_WEBSOCKET_OUTPUT_RECONNECTING"
	// OutputReconnected is the output is reconnected.
	OutputReconnected = "OBS_WEBSOCKET_OUTPUT_RECONNECTED"
)

// wire is one obs-websocket v5 message: the opcode in op and the data
// of the opcode in d.
type wire struct {
	Op int            `json:"op"`
	D  jsontext.Value `json:"d"`
}

// helloMsg is the data of the Hello opcode (op 0), sent by the server
// to the client (protocol.md); the authentication is absent for a
// server without a password.
type helloMsg struct {
	ObsWebSocketVersion string     `json:"obsWebSocketVersion"`
	RPCVersion          int        `json:"rpcVersion"`
	Authentication      *helloAuth `json:"authentication"`
}

// helloAuth carries the salt and the challenge of the password
// authentication.
type helloAuth struct {
	Challenge string `json:"challenge"`
	Salt      string `json:"salt"`
}

// identifyMsg is the data of the Identify opcode (op 1); the
// authentication is nil for a connection without a password.
type identifyMsg struct {
	RPCVersion         int     `json:"rpcVersion"`
	Authentication     *string `json:"authentication"`
	EventSubscriptions int     `json:"eventSubscriptions"`
}

// eventMsg is the data of the Event opcode (op 5).
type eventMsg struct {
	Type   string         `json:"eventType"`
	Intent int            `json:"eventIntent"`
	Data   jsontext.Value `json:"eventData"`
}

// requestMsg is the data of the Request opcode (op 6).
type requestMsg struct {
	Type string         `json:"requestType"`
	ID   string         `json:"requestId"`
	Data jsontext.Value `json:"requestData,omitempty"`
}

// responseMsg is the data of the Response opcode (op 7).
type responseMsg struct {
	Type   string         `json:"requestType"`
	ID     string         `json:"requestId"`
	Status requestStatus  `json:"requestStatus"`
	Data   jsontext.Value `json:"responseData"`
}

// requestStatus is the requestStatus of the Response opcode.
type requestStatus struct {
	Code    int    `json:"code"`
	Result  bool   `json:"result"`
	Comment string `json:"comment,omitempty"`
}

// The event data of the OBS events the service publishes (B44).
type (
	streamStateEvent struct {
		OutputState string `json:"outputState"`
	}
	recordStateEvent struct {
		OutputState string `json:"outputState"`
		OutputPath  string `json:"outputPath"`
	}
	replaySavedEvent struct {
		SavedReplayPath string `json:"savedReplayPath"`
	}
	sceneChangedEvent struct {
		SceneName string `json:"sceneName"`
	}
	transitionEvent struct {
		TransitionName string `json:"transitionName"`
	}
	itemEnabledEvent struct {
		SceneName        string `json:"sceneName"`
		SceneItemID      int    `json:"sceneItemId"`
		SceneItemEnabled bool   `json:"sceneItemEnabled"`
	}
	filterEnabledEvent struct {
		SourceName    string `json:"sourceName"`
		FilterName    string `json:"filterName"`
		FilterEnabled bool   `json:"filterEnabled"`
	}
)

// client is one established connection to the OBS server: the reader of
// the connection, the events for the consumer of the session, and the
// requests on it.
type client struct {
	conn     *websocket.Conn
	logger   *slog.Logger
	password string

	writeMu sync.Mutex

	mu      sync.Mutex
	waiters map[string]chan *responseMsg

	events chan *eventMsg
	done   chan struct{}

	closeOnce sync.Once
}

// dial connects to the OBS server at host:port, runs the handshake, and
// starts the reader (B43); a refused authentication is a close with the
// code 4009, so the handshake fails with it. The dial and the handshake
// run with the timeout, so that a silent peer cannot block the
// connection (Review 346-4); the reader keeps the context it was
// given.
func dial(ctx context.Context, host string, port int, password string, guard netguard.Dialer, logger *slog.Logger, timeout time.Duration) (*client, error) {
	endpoint := net.JoinHostPort(host, strconv.Itoa(port))
	opts := &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				DialContext: guard.DialContext,
			},
		},
	}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, _, err := websocket.Dial(hctx, "ws://"+endpoint, opts) //nolint:bodyclose // the body is consumed by Dial
	if err != nil {
		return nil, fmt.Errorf("obs %s: %w", endpoint, err)
	}
	c := &client{
		conn:     conn,
		logger:   logger,
		password: password,
		waiters:  map[string]chan *responseMsg{},
		events:   make(chan *eventMsg, 100),
		done:     make(chan struct{}),
	}
	if err := c.handshake(hctx); err != nil {
		_ = conn.CloseNow()
		return nil, fmt.Errorf("obs %s: %w", endpoint, err)
	}
	go c.readLoop(ctx)
	return c, nil
}

// handshake runs the Hello/Identify/Identified exchange (protocol.md);
// the hello comes from the server.
func (c *client) handshake(ctx context.Context) error {
	msg, err := c.read(ctx)
	if err != nil {
		return fmt.Errorf("the hello: %w", err)
	}
	if msg.Op != opHello {
		return fmt.Errorf("the hello: expected the opcode %d, got %d", opHello, msg.Op)
	}
	var hello helloMsg
	_ = json.Unmarshal(msg.D, &hello)
	var auth *string
	if c.password != "" && hello.Authentication != nil {
		secret := authSecret(c.password, hello.Authentication.Salt, hello.Authentication.Challenge)
		auth = &secret
	}
	if err := c.write(ctx, wire{Op: opIdentify, D: mustMarshal(identifyMsg{
		RPCVersion:         1,
		Authentication:     auth,
		EventSubscriptions: subscriptionAll,
	})}); err != nil {
		return fmt.Errorf("the identify: %w", err)
	}
	msg, err = c.read(ctx)
	if err != nil {
		return fmt.Errorf("the identify: %w", err)
	}
	if msg.Op != opIdentified {
		return fmt.Errorf("the identify: expected the opcode %d, got %d", opIdentified, msg.Op)
	}
	return nil
}

// readLoop reads the connection until it ends (a drop, a close, or the
// context); the events go to the consumer of the session, the responses
// to their waiters.
func (c *client) readLoop(ctx context.Context) {
	defer close(c.done)
	defer close(c.events)
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		var msg wire
		if err := json.Unmarshal(data, &msg); err != nil {
			c.logger.DebugContext(ctx, "the OBS message is not parseable", "error", err)
			continue
		}
		switch msg.Op {
		case opEvent:
			var ev eventMsg
			_ = json.Unmarshal(msg.D, &ev)
			select {
			case c.events <- &ev:
			default:
				// the consumer is behind; the event is dropped, the
				// reader must not stall on it
				c.logger.DebugContext(ctx, "the OBS event was dropped, the consumer is behind")
			}
		case opResponse:
			var resp responseMsg
			_ = json.Unmarshal(msg.D, &resp)
			c.mu.Lock()
			ch := c.waiters[resp.ID]
			delete(c.waiters, resp.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- &resp
			}
		}
	}
}

// request sends the OBS request of the type with the params (nil without
// one) and reads the response into out (nil without one); a failed
// request is an error with the status comment of the response.
func (c *client) request(ctx context.Context, typ string, params any, out any) error {
	var data jsontext.Value
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("the request %s: %w", typ, err)
		}
		data = jsontext.Value(b)
	}
	id := uuid.NewString()
	ch := make(chan *responseMsg, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return errors.New("the connection is gone")
	default:
	}
	c.waiters[id] = ch
	c.mu.Unlock()
	if err := c.write(ctx, wire{Op: opRequest, D: mustMarshal(requestMsg{Type: typ, ID: id, Data: data})}); err != nil {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
		return fmt.Errorf("the request %s: %w", typ, err)
	}
	timer := time.NewTimer(requestTimeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if !resp.Status.Result || resp.Status.Code != statusSuccess {
			return fmt.Errorf("the request %s failed: %s (code %d)", typ, resp.Status.Comment, resp.Status.Code)
		}
		if out == nil || len(resp.Data) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Data, out); err != nil {
			return fmt.Errorf("the response of the request %s: %w", typ, err)
		}
		return nil
	case <-c.done:
		return errors.New("the connection dropped while the request " + typ + " was running")
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-timer.C:
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
		return fmt.Errorf("the request %s: no response within %s", typ, requestTimeout)
	}
}

// write sends one message of the connection; the writes of the
// connection are serialized.
func (c *client) write(ctx context.Context, msg wire) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("the message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, data)
}

// read receives one message of the connection.
func (c *client) read(ctx context.Context) (*wire, error) {
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var msg wire
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("the message is not parseable: %w", err)
	}
	return &msg, nil
}

// close ends the connection gracefully and waits for the reader to end;
// it is idempotent.
func (c *client) close() {
	c.closeOnce.Do(func() {
		_ = c.conn.Close(websocket.StatusNormalClosure, "bye")
		<-c.done
	})
}

// authSecret is the authentication string of obs-websocket v5 for the
// password with the salt and the challenge of the hello (protocol.md,
// Identify).
func authSecret(password, salt, challenge string) string {
	inner := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(inner[:])
	outer := sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(outer[:])
}

func mustMarshal(v any) jsontext.Value {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return jsontext.Value(data)
}
