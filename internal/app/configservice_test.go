// SPDX-License-Identifier: MIT

package app_test

import (
	"net/http"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
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
func hotCoreServer(t *testing.T, pub *outPub) (*app.App, string, string) {
	t.Helper()
	return hotCoreArgs(t, pub, config.ModeServer)
}

// configClient returns the ConfigService client of the core.
func configClient(t *testing.T, a *app.App, token string) streamcrewv1alpha1connect.ConfigServiceClient {
	t.Helper()
	base := "http://" + mustServerAddr(t, a)
	return streamcrewv1alpha1connect.NewConfigServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0)), withAuth(token)))
}

// TestConfigServiceUpdatesTheLiveDocument runs the core in the server mode
// and changes the dry run through the ConfigService; the platforms pick up
// the change without a restart (roadmap 6.5.2).
func TestConfigServiceUpdatesTheLiveDocument(t *testing.T) {
	t.Parallel()
	pub := &outPub{}
	a, path, token := hotCoreServer(t, pub)
	base := "http://" + mustServerAddr(t, a)
	cfg := configClient(t, a, token)
	chat := chatClient(t, base, token)

	// The document is live and shows the current settings.
	g, err := cfg.Get(t.Context(), &v1alpha1.GetConfigRequest{})
	require.NoError(t, err)
	assert.True(t, g.GetLive())
	assert.Contains(t, g.GetDocument(), "log_level: debug")
	assert.Contains(t, g.GetHot(), "dry-run")
	assert.Contains(t, g.GetRestart(), "data-dir")

	// The dry run is off: the chat goes to the platform.
	_, err = chat.Send(t.Context(), &v1alpha1.SendChatRequest{Text: "real"})
	require.NoError(t, err)
	assert.Eventually(t, func() bool { return pub.sentText("real") }, tTimeout, 50*time.Millisecond)

	// The Update stores the document; the change applies at runtime.
	u, err := cfg.Update(t.Context(), &v1alpha1.UpdateConfigRequest{
		Document: "log_level: debug\ndry_run: true\n",
	})
	require.NoError(t, err)
	assert.Contains(t, u.GetApplied(), "dry-run")

	// The watcher applies the document on its next comparison (1 s).
	time.Sleep(2 * time.Second)

	// The dry run is on: the chat is only recorded, not sent.
	_, err = chat.Send(t.Context(), &v1alpha1.SendChatRequest{Text: "recorded"})
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)
	assert.False(t, pub.sentText("recorded"))
	_ = path
}

// TestConfigServiceRejectsAnExtendingDocument covers the narrowing rule
// (Code-ADR-0021, Nachtrag 2026-10-24): a document that extends the
// currently effective configuration is rejected as a whole, and the
// response names the keys.
func TestConfigServiceRejectsAnExtendingDocument(t *testing.T) {
	t.Parallel()
	a, path, token := hotCoreServer(t, &outPub{})
	cfg := configClient(t, a, token)

	// A new grant entry extends the current rights: denied.
	_, err := cfg.Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "grant: [host:fs]\n"})
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "grant")
	assert.Contains(t, err.Error(), "configuration file", "the hint where the key can be extended")

	// The file keeps the content last seen.
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.NotContains(t, string(data), "grant")

	// A narrowing document is accepted.
	_, err = cfg.Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "revoke: [net:outbound]\n"})
	assert.NoError(t, err)
}

// TestConfigServiceTTSKeysAreWriteOnlyAndHot covers the TTS provider keys
// through the running core (ADR-0019 §6, Code-ADR-0021, Nachtrag
// 2026-10-29): the key is written to the file with 0600, Get shows the
// placeholder instead of the value, a written-back document with the
// placeholder keeps the key, and an explicitly empty value clears it —
// all without a restart.
func TestConfigServiceTTSKeysAreWriteOnlyAndHot(t *testing.T) {
	t.Parallel()
	a, path, token := hotCoreServer(t, &outPub{})
	cfg := configClient(t, a, token)

	// Without a key, the document has none.
	g, err := cfg.Get(t.Context(), &v1alpha1.GetConfigRequest{})
	require.NoError(t, err)
	assert.NotContains(t, g.GetDocument(), "tts_google_key")
	assert.Contains(t, g.GetHot(), "tts-google-key", "the key is a hot setting")

	// Set the key and the region; the hot apply swaps the snapshot of
	// the TTS service on the next comparison (1 s).
	u, err := cfg.Update(t.Context(), &v1alpha1.UpdateConfigRequest{
		Document: "log_level: debug\ntts_google_key: google-secret\ntts_azure_region: westeurope\n",
	})
	require.NoError(t, err)
	assert.Contains(t, u.GetApplied(), "tts-google-key")
	assert.Contains(t, u.GetApplied(), "tts-azure-region")
	time.Sleep(2 * time.Second)

	// The file holds the real key with mode 0600.
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "google-secret")
	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// Get shows the placeholder and the region, never the secret.
	g, err = cfg.Get(t.Context(), &v1alpha1.GetConfigRequest{})
	require.NoError(t, err)
	assert.Contains(t, g.GetDocument(), "tts_google_key: "+config.SecretPlaceholder)
	assert.Contains(t, g.GetDocument(), "tts_azure_region: westeurope")
	assert.NotContains(t, g.GetDocument(), "google-secret", "the secret is write-only")

	// A written-back document with the placeholder keeps the key.
	_, err = cfg.Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: g.GetDocument()})
	require.NoError(t, err)
	data, readErr = os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "google-secret")
	assert.NotContains(t, string(data), config.SecretPlaceholder)

	// An explicitly empty value clears the key.
	_, err = cfg.Update(t.Context(), &v1alpha1.UpdateConfigRequest{
		Document: "log_level: debug\ntts_google_key: \"\"\n",
	})
	require.NoError(t, err)
	time.Sleep(2 * time.Second)
	g, err = cfg.Get(t.Context(), &v1alpha1.GetConfigRequest{})
	require.NoError(t, err)
	assert.NotContains(t, g.GetDocument(), "tts_google_key", "the cleared key is omitted")
}

// TestConfigServiceReadOnlyInDaemonMode checks that the daemon mode serves
// the document as read-only (roadmap 6.5.2).
func TestConfigServiceReadOnlyInDaemonMode(t *testing.T) {
	t.Parallel()
	a, _, token := hotCore(t, &outPub{})
	cfg := configClient(t, a, token)

	g, err := cfg.Get(t.Context(), &v1alpha1.GetConfigRequest{})
	require.NoError(t, err)
	assert.False(t, g.GetLive())

	_, err = cfg.Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "log_level: debug\n"})
	assert.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}
