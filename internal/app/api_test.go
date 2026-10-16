// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/app"
)

// TestAPIServesTheCounterAndSettings exercises the Counter and Settings
// services through the real ConnectRPC clients (roadmap 6.2.1).
func TestAPIServesTheCounterAndSettings(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	var console bytes.Buffer
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errRun := make(chan error, 1)
	go func() { errRun <- a.Run(ctx) }()
	select {
	case <-a.HTTPServer().Listening():
	case err := <-errRun:
		t.Fatalf("Run returned early: %v", err)
	}

	base := "http://" + a.HTTPServer().Addr().String()
	counterClient := streamcrewv1alpha1connect.NewCounterServiceClient(http.DefaultClient, base)
	settingsClient := streamcrewv1alpha1connect.NewSettingsServiceClient(http.DefaultClient, base)
	name := "apiTestCounter"

	// Counter: create, read, change, delete.
	cr, err := counterClient.Create(t.Context(), connect.NewRequest(&v1alpha1.CreateCounterRequest{Name: name, Value: "10", Step: "2"}))
	require.NoError(t, err)
	assert.Equal(t, "10", cr.Msg.Counter.Value)
	assert.Equal(t, "2", cr.Msg.Counter.Step)

	got, err := counterClient.Get(t.Context(), connect.NewRequest(&v1alpha1.GetCounterRequest{Name: name}))
	require.NoError(t, err)
	assert.Equal(t, "10", got.Msg.Counter.Value)

	add, err := counterClient.Add(t.Context(), connect.NewRequest(&v1alpha1.AddCounterRequest{Name: name}))
	require.NoError(t, err)
	assert.Equal(t, "12", add.Msg.Counter.Value, "empty amount adds one step")

	set, err := counterClient.Set(t.Context(), connect.NewRequest(&v1alpha1.SetCounterRequest{Name: name, Value: "5"}))
	require.NoError(t, err)
	assert.Equal(t, "5", set.Msg.Counter.Value)

	reset, err := counterClient.Reset(t.Context(), connect.NewRequest(&v1alpha1.ResetCounterRequest{Name: name}))
	require.NoError(t, err)
	assert.Equal(t, "0", reset.Msg.Counter.Value)

	list, err := counterClient.List(t.Context(), connect.NewRequest(&v1alpha1.ListCountersRequest{Prefix: name[:3]}))
	require.NoError(t, err)
	assert.NotEmpty(t, list.Msg.Counters, "the prefix filter finds the counter")

	// Update keeps the value, changes the step and the reset-on-start switch.
	up, err := counterClient.Update(t.Context(), connect.NewRequest(&v1alpha1.UpdateCounterRequest{Name: name, Step: "5"}))
	require.NoError(t, err)
	assert.Equal(t, "5", up.Msg.Counter.Step)

	del, err := counterClient.Delete(t.Context(), connect.NewRequest(&v1alpha1.DeleteCounterRequest{Name: name}))
	require.NoError(t, err)
	require.NotNil(t, del.Msg)

	_, err = counterClient.Get(t.Context(), connect.NewRequest(&v1alpha1.GetCounterRequest{Name: name}))
	assert.Error(t, err, "the counter is gone")

	// Settings: the sections are listed, a stored section is read back, and a
	// never-saved section returns its defaults.
	secs, err := settingsClient.ListSections(t.Context(), connect.NewRequest(&v1alpha1.ListSectionsRequest{}))
	require.NoError(t, err)
	assert.NotEmpty(t, secs.Msg.Sections)

	g, err := settingsClient.Get(t.Context(), connect.NewRequest(&v1alpha1.GetSettingsRequest{Name: "chat"}))
	require.NoError(t, err)
	assert.Contains(t, g.Msg.Document, `"type":"chat"`, "a never-saved section returns its defaults")

	u, err := settingsClient.Update(t.Context(), connect.NewRequest(&v1alpha1.UpdateSettingsRequest{Name: "chat", Document: `{"type":"chat","schemaVersion":1,"historySize":7}`}))
	require.NoError(t, err)
	assert.Contains(t, u.Msg.Document, "7")

	g2, err := settingsClient.Get(t.Context(), connect.NewRequest(&v1alpha1.GetSettingsRequest{Name: "chat"}))
	require.NoError(t, err)
	assert.Contains(t, g2.Msg.Document, "7", "the updated section is stored")

	_, err = settingsClient.Update(t.Context(), connect.NewRequest(&v1alpha1.UpdateSettingsRequest{Name: "chat", Document: `{"type":"general","schemaVersion":1}`}))
	assert.Error(t, err, "a document with the wrong type is rejected")
}

// TestAPIServesTheCore starts the core and reads its state through the API
// (roadmap 6.2.1): Status reports the version and the active profile, and
// ListProfiles names the same profile as active.
func TestAPIServesTheCore(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	var console bytes.Buffer
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- a.Run(ctx) }()

	select {
	case <-a.HTTPServer().Listening():
	case err := <-errc:
		t.Fatalf("Run returned early: %v", err)
	}
	base := "http://" + a.HTTPServer().Addr().String()
	client := streamcrewv1alpha1connect.NewSystemServiceClient(http.DefaultClient, base)

	st, err := client.Status(t.Context(), connect.NewRequest(&v1alpha1.StatusRequest{}))
	require.NoError(t, err)
	assert.NotEmpty(t, st.Msg.Version, "the core reports its version")
	assert.NotEmpty(t, st.Msg.Profile, "the core reports its active profile")

	lp, err := client.ListProfiles(t.Context(), connect.NewRequest(&v1alpha1.ListProfilesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, st.Msg.Profile, lp.Msg.Active, "the active profile matches Status")
	seen := false
	for _, p := range lp.Msg.Profiles {
		if p.Name == lp.Msg.Active {
			seen = true
		}
	}
	assert.True(t, seen, "the active profile is in the list")
}
