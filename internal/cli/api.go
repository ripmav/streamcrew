// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

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

// connectOptions are the client options for the API of the core: the Bearer
// header when a token is set.
func (e *Env) connectOptions() []connect.ClientOption {
	if e.APIToken == "" {
		return nil
	}
	b := &bearer{token: e.APIToken}
	return []connect.ClientOption{connect.WithInterceptors(b)}
}

// streamClient returns the StreamService client of the core.
func (e *Env) streamClient() streamcrewv1alpha1connect.StreamServiceClient {
	return streamcrewv1alpha1connect.NewStreamServiceClient(http.DefaultClient, e.server(), e.connectOptions()...)
}

// chatClient returns the ChatService client of the core.
func (e *Env) chatClient() streamcrewv1alpha1connect.ChatServiceClient {
	return streamcrewv1alpha1connect.NewChatServiceClient(http.DefaultClient, e.server(), e.connectOptions()...)
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
