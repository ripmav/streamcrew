// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// TestSettingsViewShowsTheDocument checks that the settings view shows the
// document with the annotation per setting and the live status
// (roadmap 6.5.3).
func TestSettingsViewShowsTheDocument(t *testing.T) {
	t.Parallel()
	base := startMockCoreServer(t)
	client, base := httpClientFor(base)
	cfgClient := streamcrewv1alpha1connect.NewConfigServiceClient(connect.NewClient(connecthttp.NewTransport(client, base, connecthttp.WithReadMaxBytes(0)), bearerInterceptor(testToken)))

	resp, err := cfgClient.Get(t.Context(), &v1alpha1.GetConfigRequest{})
	require.NoError(t, err)
	assert.True(t, resp.GetLive())

	m := model{
		settings: settingsView{
			loaded:   true,
			document: resp.GetDocument(),
			hot:      resp.GetHot(),
			fixed:    resp.GetFixed(),
			restart:  resp.GetRestart(),
			live:     resp.GetLive(),
		},
	}
	out := m.settings.render(newTheme(true), 80)
	assert.Contains(t, out, "Settings")
	assert.Contains(t, out, "live: live")
	assert.Contains(t, out, "log_level:")
	assert.Contains(t, out, "⚡")
}

// TestSettingsViewReadOnlyInDaemonMode checks that the daemon mode shows
// the document as read-only (roadmap 6.5.3).
func TestSettingsViewReadOnlyInDaemonMode(t *testing.T) {
	t.Parallel()
	base := startMockCore(t)
	client, base := httpClientFor(base)
	cfgClient := streamcrewv1alpha1connect.NewConfigServiceClient(connect.NewClient(connecthttp.NewTransport(client, base, connecthttp.WithReadMaxBytes(0)), bearerInterceptor(testToken)))

	resp, err := cfgClient.Get(t.Context(), &v1alpha1.GetConfigRequest{})
	require.NoError(t, err)
	assert.False(t, resp.GetLive())

	m := model{
		settings: settingsView{
			loaded:   true,
			document: resp.GetDocument(),
			live:     resp.GetLive(),
		},
	}
	out := m.settings.render(newTheme(true), 80)
	assert.Contains(t, out, "read-only")
	assert.NotContains(t, out, "e edit in $EDITOR")
}

// TestSettingsKeyReloads checks that r reloads the document and e is
// refused in the read-only state (roadmap 6.5.3).
func TestSettingsKeyReloads(t *testing.T) {
	base := startMockCoreServer(t)
	m := newModel(t.Context(), Options{Server: base, Token: testToken})

	_, cmd := m.updateSettingsKey(tea.Key{Code: 'r'})
	require.NotNil(t, cmd, "r returns the reload command")

	// e without a loaded live document does nothing.
	_, cmd = m.updateSettingsKey(tea.Key{Code: 'e'})
	assert.Nil(t, cmd)

	// e on a read-only document does nothing.
	m.settings.loaded = true
	m.settings.live = false
	_, cmd = m.updateSettingsKey(tea.Key{Code: 'e'})
	assert.Nil(t, cmd)

	// e on a live document opens the editor command.
	m.settings.live = true
	m.settings.document = "log_level: info\n"
	t.Setenv("EDITOR", "/bin/true")
	_, cmd = m.updateSettingsKey(tea.Key{Code: 'e'})
	require.NotNil(t, cmd)
}

// TestSettingsAnnotation maps the settings to their marks (roadmap 6.5.3).
func TestSettingsAnnotation(t *testing.T) {
	t.Parallel()
	s := settingsView{
		hot:     []string{"dry-run", "log-level", "listen", "mode", "grant"},
		fixed:   []string{"data-dir"},
		restart: []string{"profile"},
	}
	assert.Equal(t, "⚡", s.annotation("dry_run"))
	assert.Equal(t, "⚡", s.annotation("log_level"))
	assert.Equal(t, "⚡", s.annotation("listen"))
	assert.Equal(t, "⚡", s.annotation("mode"))
	assert.Equal(t, "✋", s.annotation("data_dir"))
	assert.Equal(t, "⟳", s.annotation("profile"))
}
