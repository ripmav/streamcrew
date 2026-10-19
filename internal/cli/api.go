// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
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
	if e.APIToken != "" {
		interceptors = append(interceptors, bearerInterceptor(e.APIToken))
	}
	transport := connecthttp.NewTransport(http.DefaultClient, e.server(), connecthttp.WithReadMaxBytes(0))
	return connect.NewClient(transport, interceptors...)
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
