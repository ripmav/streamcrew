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
