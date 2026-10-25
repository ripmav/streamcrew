// SPDX-License-Identifier: MIT

package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/config"
)

// fakeConfig is a fake of the api.Config port.
type fakeConfig struct {
	document string
	hot      []string
	fixed    []string
	restart  []string
	live     bool
	getErr   error
	update   func(ctx context.Context, document string) ([]string, []string, string, error)
}

func (f *fakeConfig) Get() (string, []string, []string, []string, bool, error) {
	return f.document, f.hot, f.fixed, f.restart, f.live, f.getErr
}

func (f *fakeConfig) Update(ctx context.Context, document string) ([]string, []string, string, error) {
	return f.update(ctx, document)
}

func configClient(t *testing.T, c *fakeConfig) streamcrewv1alpha1connect.ConfigServiceClient {
	t.Helper()
	h := api.New(api.ServerConfig{Config: c}).Handler()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return streamcrewv1alpha1connect.NewConfigServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, srv.URL, connecthttp.WithReadMaxBytes(0))))
}

// TestConfigGetReturnsTheDocument checks that Get returns the document
// with the categories of the settings (roadmap 6.5.2).
func TestConfigGetReturnsTheDocument(t *testing.T) {
	t.Parallel()
	c := &fakeConfig{
		document: "log_level: debug\n",
		hot:      []string{"dry-run"},
		fixed:    []string{"listen"},
		restart:  []string{"data_dir"},
		live:     true,
	}
	resp, err := configClient(t, c).Get(t.Context(), &v1alpha1.GetConfigRequest{})
	require.NoError(t, err)
	assert.Equal(t, "log_level: debug\n", resp.GetDocument())
	assert.Equal(t, []string{"dry-run"}, resp.GetHot())
	assert.Equal(t, []string{"listen"}, resp.GetFixed())
	assert.Equal(t, []string{"data_dir"}, resp.GetRestart())
	assert.True(t, resp.GetLive())
}

// TestConfigUpdateStoresTheDocument checks that Update returns the stored
// document with the changed settings (roadmap 6.5.2).
func TestConfigUpdateStoresTheDocument(t *testing.T) {
	t.Parallel()
	c := &fakeConfig{live: true, update: func(_ context.Context, document string) ([]string, []string, string, error) {
		return []string{"dry-run"}, []string{"data_dir"}, document, nil
	}}
	resp, err := configClient(t, c).Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "dry_run: true\n"})
	require.NoError(t, err)
	assert.Equal(t, []string{"dry-run"}, resp.GetApplied())
	assert.Equal(t, []string{"data_dir"}, resp.GetRestart())
	assert.Equal(t, "dry_run: true\n", resp.GetDocument())
}

// TestConfigUpdateMapsErrors checks the error mapping: invalid document is
// an invalid argument, a read-only core is a failed precondition, a
// document that extends the current configuration is a permission denied
// (Code-ADR-0021, Nachtrag 2026-10-24), and an internal error stays
// internal (roadmap 6.5.2).
func TestConfigUpdateMapsErrors(t *testing.T) {
	t.Parallel()

	invalid := &fakeConfig{live: true, update: func(context.Context, string) ([]string, []string, string, error) {
		return nil, nil, "", api.ErrConfigInvalid
	}}
	_, err := configClient(t, invalid).Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "nope"})
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	narrow := &fakeConfig{live: true, update: func(context.Context, string) ([]string, []string, string, error) {
		return nil, nil, "", fmt.Errorf("%w: grant", config.ErrNarrowOnly)
	}}
	_, err = configClient(t, narrow).Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "grant: [host:fs]\n"})
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "grant")

	readonly := &fakeConfig{live: false, update: func(context.Context, string) ([]string, []string, string, error) {
		return nil, nil, "", api.ErrConfigReadOnly
	}}
	_, err = configClient(t, readonly).Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "log_level: debug\n"})
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	broken := &fakeConfig{live: true, update: func(context.Context, string) ([]string, []string, string, error) {
		return nil, nil, "", errors.New("disk full")
	}}
	_, err = configClient(t, broken).Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "log_level: debug\n"})
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
