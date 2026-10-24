// SPDX-License-Identifier: MIT

package twitchmock

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/rpc"
	"strconv"
)

// The mock EventSub WebSocket server runs a JSON-RPC 2.0 control
// interface (net/rpc/jsonrpc over HTTP) on the fixed port 44747; the
// twitch-cli commands "event websocket reconnect|close|keepalive" and
// "event trigger … --transport=websocket" speak this protocol. The
// methods below speak it directly, which also allows event types the
// CLI does not know (its topic list predates the 2025 EventSub
// names): the server forwards the raw body without a topic check.
//
// One short-lived connection per call: the chaos tests restart the
// server, which invalidates the connection. DialHTTP fails fast when
// the server is down (connection refused); while the server is up it
// always answers (the handlers return, also the ones that refuse
// during a reconnect).

// rpcArgs is the argument of RPCHandler.ExecuteGenericRPC; the field
// names match the twitch-cli struct rpc_handler.RPCArgs (net/rpc
// matches by name).
type rpcArgs struct {
	RPCName   string            `json:"RPCName"`
	Body      string            `json:"Body"`
	Variables map[string]string `json:"Variables"`
}

// rpcReply is the reply of RPCHandler.ExecuteGenericRPC; response
// code 0 is success.
type rpcReply struct {
	ResponseCode int    `json:"ResponseCode"`
	DetailedInfo string `json:"DetailedInfo"`
}

// call runs one RPC against the mock server.
func (c *Client) call(rpcName, body string, vars map[string]string) error {
	target := fmt.Sprintf("127.0.0.1:%d", rpcPort)
	client, err := rpc.DialHTTP("tcp", target)
	if err != nil {
		return fmt.Errorf("twitchmock: the mock server rpc is not reachable: %w", err)
	}
	defer client.Close()
	var reply rpcReply
	if err := client.Call("RPCHandler.ExecuteGenericRPC",
		rpcArgs{RPCName: rpcName, Body: body, Variables: vars}, &reply); err != nil {
		return fmt.Errorf("twitchmock: rpc %s failed: %w", rpcName, err)
	}
	if reply.ResponseCode != 0 {
		return fmt.Errorf("twitchmock: rpc %s failed: code %d: %s", rpcName, reply.ResponseCode, reply.DetailedInfo)
	}
	return nil
}

// Fire forwards an event body (a complete EventsubResponse JSON:
// {"subscription": {…}, "event": {…}}) to all connected clients — or
// to one alone when session (the session ID of the session_welcome
// message) is non-empty.
func (c *Client) Fire(body, session string) error {
	vars := map[string]string{}
	if session != "" {
		vars["ClientName"] = session
	}
	return c.call("EventSubWebSocketForwardEvent", body, vars)
}

// FireEvent wraps an event payload into the EventsubResponse
// structure and forwards it to all clients, with the partner user as
// broadcaster. The server fills the session ID and the created-at
// timestamp per client, so the subscription shell here is enough.
func (c *Client) FireEvent(subType, version string, event jsontext.Value) error {
	body := fmt.Sprintf(
		`{"subscription":{"id":"twitchmock","status":"enabled","type":%q,"version":%q,`+
			`"condition":{"broadcaster_user_id":%q},"transport":{"method":"websocket"},`+
			`"created_at":"2026-01-01T00:00:00Z"},"event":%s}`,
		subType, version, c.BroadcasterID, string(event))
	return c.Fire(body, "")
}

// Reconnect simulates the production load balancer restart: the
// server sends session_reconnect to all clients and closes the old
// connections after a grace period of about 30 seconds; the clients
// reconnect with the session ID and carry the subscriptions over.
// While it runs, the other RPCs (close, keepalive, subscription)
// refuse with a server error.
func (c *Client) Reconnect() error {
	return c.call("EventSubWebSocketReconnect", "", nil)
}

// CloseClient closes the client of the given session with the
// documented close code (4003–4008, see the EventSub WebSocket
// reference of the Twitch developer documentation).
func (c *Client) CloseClient(session string, reason int) error {
	vars := map[string]string{
		"ClientName":  session,
		"CloseReason": strconv.Itoa(reason),
	}
	return c.call("EventSubWebSocketCloseClient", "", vars)
}

// SetKeepalive toggles the session_keepalive messages of the given
// session (disabled means: the server stops the keepalives, so a
// conforming client must notice the silence and reconnect).
func (c *Client) SetKeepalive(session string, enabled bool) error {
	vars := map[string]string{
		"ClientName":     session,
		"FeatureEnabled": strconv.FormatBool(enabled),
	}
	return c.call("EventSubWebSocketKeepalive", "", vars)
}

// jsonDecode decodes a JSON body (also used in twitchmock.go).
func jsonDecode(r io.Reader, v any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
