// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	jsonv2 "encoding/json/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"

	"github.com/ripmav/streamcrew/internal/config"
)

// defaultServer is the local core the API commands talk to when --server is
// not set.
const defaultServer = "http://127.0.0.1:8740"

// server is the address of the core the API commands talk to.
func (e *Env) server() string {
	if e.Server != "" {
		return e.Server
	}
	return defaultServer
}

// connectClient returns the connect client for the API of the core with the
// Bearer header when a token is set.
func (e *Env) connectClient() *connect.Client {
	interceptors := []connect.ClientInterceptor{}
	if token := e.token(); token != "" {
		interceptors = append(interceptors, bearerInterceptor(token))
	}
	transport := connecthttp.NewTransport(http.DefaultClient, e.server(), connecthttp.WithReadMaxBytes(0))
	return connect.NewClient(transport, interceptors...)
}

// token returns the API token of the local core: the value of --token or
// STREAMCREW_TOKEN, else — only when the server is the local core
// (Loopback, Unix-Socket, or the address of the runtime file of the data
// directory) — the local token file of the data directory (desktop-token in
// desktop mode, api.token in daemon mode, ADR-0013 §5). A remote --server
// never gets the local admin token (Nachprüfung 2026-10-24, N1). Without a
// running core or a token file it is empty.
func (e *Env) token() string {
	if e.APIToken != "" {
		return e.APIToken
	}
	if e.Server != "" && !e.serverIsLocal() {
		return ""
	}
	dataDir := ""
	if e.Config != nil {
		dataDir = e.Config.DataDir
	}
	if dataDir == "" {
		dataDir = e.Defaults.DataDir
	}
	if dataDir == "" {
		return ""
	}
	for _, path := range []string{config.DesktopTokenPath(dataDir), config.APITokenPath(dataDir)} {
		if data, err := os.ReadFile(path); err == nil {
			if v := strings.TrimSpace(string(data)); v != "" {
				return v
			}
		}
	}
	return ""
}

// serverIsLocal reports whether the server the API commands talk to is the
// local core: a Loopback address, a Unix-Socket, or the address of the
// runtime file of the data directory.
func (e *Env) serverIsLocal() bool {
	addr := e.Server
	if addr == "" {
		return true
	}
	if strings.HasPrefix(addr, "unix:") {
		return true
	}
	u, err := url.Parse(addr)
	if err == nil && u.Host != "" {
		addr = u.Host
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	dataDir := ""
	if e.Config != nil {
		dataDir = e.Config.DataDir
	}
	if dataDir == "" {
		dataDir = e.Defaults.DataDir
	}
	if dataDir != "" {
		if data, err := os.ReadFile(config.RuntimePath(dataDir)); err == nil {
			var info struct {
				Address string `json:"address"`
			}
			if jsonv2.Unmarshal(data, &info) == nil && strings.EqualFold(info.Address, addr) {
				return true
			}
		}
	}
	return false
}

// join joins the strings with the separator.
func join(list []string, sep string) string {
	var out strings.Builder
	for i, s := range list {
		if i > 0 {
			out.WriteString(sep)
		}
		out.WriteString(s)
	}
	return out.String()
}

// streamClient returns the StreamService client of the core.
func (e *Env) streamClient() streamcrewv1alpha1connect.StreamServiceClient {
	return streamcrewv1alpha1connect.NewStreamServiceClient(e.connectClient())
}

// chatClient returns the ChatService client of the core.
func (e *Env) chatClient() streamcrewv1alpha1connect.ChatServiceClient {
	return streamcrewv1alpha1connect.NewChatServiceClient(e.connectClient())
}

// bearerInterceptor sets the Authorization header on every request.
func bearerInterceptor(token string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			// The client attaches a CallInfo to the context before the
			// interceptor chain runs.
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, spec)
		}
	}
}
