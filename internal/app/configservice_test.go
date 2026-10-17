// SPDX-License-Identifier: MIT

package app_test

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
)

// tTimeout is the wait for asynchronous effects in the tests.
const tTimeout = 5 * time.Second

// hotCoreServer returns a running core in the server mode with the mock
// platform, a configuration file and the HTTP API.
func hotCoreServer(t *testing.T, pub *outPub) (*app.App, string) {
	t.Helper()
	return hotCoreArgs(t, pub, config.ModeServer)
}

// configClient returns the ConfigService client of the core.
func configClient(t *testing.T, a *app.App) streamcrewv1alpha1connect.ConfigServiceClient {
	t.Helper()
	base := "http://" + a.HTTPServer().Addr().String()
	return streamcrewv1alpha1connect.NewConfigServiceClient(http.DefaultClient, base)
}

// TestConfigServiceUpdatesTheLiveDocument runs the core in the server mode
// and changes the dry run through the ConfigService; the platforms pick up
// the change without a restart (roadmap 6.6.2).
func TestConfigServiceUpdatesTheLiveDocument(t *testing.T) {
	t.Parallel()
	pub := &outPub{}
	a, path := hotCoreServer(t, pub)
	base := "http://" + a.HTTPServer().Addr().String()
	cfg := configClient(t, a)
	chat := chatClient(t, base)

	// The document is live and shows the current settings.
	g, err := cfg.Get(t.Context(), connect.NewRequest(&v1alpha1.GetConfigRequest{}))
	require.NoError(t, err)
	assert.True(t, g.Msg.GetLive())
	assert.Contains(t, g.Msg.GetDocument(), "log_level: debug")
	assert.Contains(t, g.Msg.GetHot(), "dry-run")
	assert.Contains(t, g.Msg.GetRestart(), "data-dir")

	// The dry run is off: the chat goes to the platform.
	_, err = chat.Send(t.Context(), connect.NewRequest(&v1alpha1.SendChatRequest{Text: "real"}))
	require.NoError(t, err)
	assert.Eventually(t, func() bool { return pub.sentText("real") }, tTimeout, 50*time.Millisecond)

	// The Update stores the document; the change applies at runtime.
	u, err := cfg.Update(t.Context(), connect.NewRequest(&v1alpha1.UpdateConfigRequest{
		Document: "log_level: debug\ndry_run: true\n",
	}))
	require.NoError(t, err)
	assert.Contains(t, u.Msg.GetApplied(), "dry-run")

	// The watcher applies the document on its next comparison (1 s).
	time.Sleep(2 * time.Second)

	// The dry run is on: the chat is only recorded, not sent.
	_, err = chat.Send(t.Context(), connect.NewRequest(&v1alpha1.SendChatRequest{Text: "recorded"}))
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)
	assert.False(t, pub.sentText("recorded"))
	_ = path
}

// TestConfigServiceReadOnlyInDaemonMode checks that the daemon mode serves
// the document as read-only (roadmap 6.6.2).
func TestConfigServiceReadOnlyInDaemonMode(t *testing.T) {
	t.Parallel()
	a, _ := hotCore(t, &outPub{})
	cfg := configClient(t, a)

	g, err := cfg.Get(t.Context(), connect.NewRequest(&v1alpha1.GetConfigRequest{}))
	require.NoError(t, err)
	assert.False(t, g.Msg.GetLive())

	_, err = cfg.Update(t.Context(), connect.NewRequest(&v1alpha1.UpdateConfigRequest{Document: "log_level: debug\n"}))
	assert.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}
