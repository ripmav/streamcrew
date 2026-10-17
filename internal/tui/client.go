// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"

	"connectrpc.com/connect"
)

// defaultServer is the local core the TUI connects to when no --server is
// set and no runtime file exists.
const defaultServer = "http://127.0.0.1:8740"

// server resolves the address of the core: --server, the runtime file of
// the data directory, or the local default.
func server(dataDir, given string) string {
	if given != "" {
		return given
	}
	if dataDir != "" {
		if data, err := os.ReadFile(runtimePath(dataDir)); err == nil {
			var info struct {
				Address string `json:"address"`
			}
			if err := json.Unmarshal(data, &info); err == nil && info.Address != "" {
				return baseFromAddress(info.Address)
			}
		}
	}
	return defaultServer
}

// baseFromAddress converts the listen address of the core to a base URL:
// host:port becomes http://host:port, unix:/path stays for the unix dialer.
func baseFromAddress(addr string) string {
	if strings.HasPrefix(addr, "unix:") {
		return addr
	}
	return "http://" + addr
}

// httpClientFor returns the HTTP client and the base URL for the core at
// base: a unix socket gets a unix dialer, everything else the default client.
func httpClientFor(base string) (*http.Client, string) {
	if after, ok := strings.CutPrefix(base, "unix:"); ok {
		return &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", after)
			},
		}}, "http://core"
	}
	return http.DefaultClient, base
}

// bearerOption returns the client option that sends the token with every
// request; nil for an empty token.
func bearerOption(token string) []connect.ClientOption {
	if token == "" {
		return nil
	}
	b := &bearer{token: token}
	return []connect.ClientOption{connect.WithInterceptors(b)}
}

// bearer sets the Authorization header on every unary request.
type bearer struct{ token string }

func (b *bearer) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		req.Header().Set("Authorization", "Bearer "+b.token)
		return next(ctx, req)
	}
}

func (b *bearer) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (b *bearer) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// runtimePath is the runtime file in the data directory.
func runtimePath(dataDir string) string { return dataDir + "/runtime.json" }
