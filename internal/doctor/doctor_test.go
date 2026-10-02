// SPDX-License-Identifier: MIT

package doctor_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	_ "time/tzdata" // the binary embeds the time zone database, so the test does too

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/doctor"
)

func find(t *testing.T, results []doctor.Result, check string) doctor.Result {
	t.Helper()
	for _, r := range results {
		if r.Check == check {
			return r
		}
	}
	t.Fatalf("no result for %q in %v", check, results)
	return doctor.Result{}
}

func TestHealthyEnvironment(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	results := doctor.Run(t.Context(), doctor.Config{DataDir: dataDir, Listen: "127.0.0.1:0"})

	require.Len(t, results, 7)
	for _, r := range results {
		assert.Equal(t, doctor.StatusOK, r.Status, "%s: %s", r.Check, r.Detail)
	}
	assert.False(t, doctor.Failed(results))
	assert.Equal(t, dataDir, find(t, results, "data directory").Detail)
	assert.Contains(t, find(t, results, "config file").Detail, "none")
	assert.Equal(t, "none", find(t, results, "capabilities").Detail)
}

// TestRights covers Code-ADR-0019, point 7: the report names the rights
// and warns about what the configuration knows to be wrong.
func TestRights(t *testing.T) {
	t.Parallel()
	results := doctor.Run(t.Context(), doctor.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Rights: doctor.Rights{
		Capabilities: []string{"host:fs", "net:outbound"},
		Roots:        map[string]string{"obs": "/srv/obs", "clips": "/srv/clips"},
		Outbound:     []string{"10.0.0.0/8", "nas"},
		Warnings:     []string{"host:fs is on in server mode", "file root obs: missing"},
	}})
	assert.Equal(t, "host:fs, net:outbound", find(t, results, "capabilities").Detail)
	assert.Equal(t, "clips=/srv/clips, obs=/srv/obs", find(t, results, "file roots").Detail)
	assert.Equal(t, "10.0.0.0/8, nas", find(t, results, "outbound allowlist").Detail)
	var warnings []string
	for _, r := range results {
		if r.Check == "rights" {
			assert.Equal(t, doctor.StatusWarn, r.Status)
			warnings = append(warnings, r.Detail)
		}
	}
	assert.Equal(t, []string{"host:fs is on in server mode", "file root obs: missing"}, warnings)
	assert.False(t, doctor.Failed(results), "warnings are no failure")
}

func TestDataDirectory(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	file := filepath.Join(base, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	tests := []struct {
		name       string
		dir        string
		wantStatus doctor.Status
		wantDetail string
	}{
		{name: "missing but creatable", dir: filepath.Join(base, "a", "b"), wantStatus: doctor.StatusOK, wantDetail: "created on the first start"},
		{name: "not a directory", dir: file, wantStatus: doctor.StatusFail, wantDetail: "not a directory"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := find(t, doctor.Run(t.Context(), doctor.Config{DataDir: tc.dir, Listen: "127.0.0.1:0"}), "data directory")
			assert.Equal(t, tc.wantStatus, r.Status)
			assert.Contains(t, r.Detail, tc.wantDetail)
		})
	}
}

func TestListenAddressInUse(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()

	results := doctor.Run(t.Context(), doctor.Config{DataDir: t.TempDir(), Listen: busy.Addr().String()})
	r := find(t, results, "listen address")
	assert.Equal(t, doctor.StatusFail, r.Status)
	assert.True(t, doctor.Failed(results))
}

func TestListenAddressUsedByCore(t *testing.T) {
	t.Parallel()
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer core.Close()

	r := find(t, doctor.Run(t.Context(), doctor.Config{DataDir: t.TempDir(), Listen: core.Listener.Addr().String()}), "listen address")
	assert.Equal(t, doctor.StatusOK, r.Status)
	assert.Contains(t, r.Detail, "running streamcrew core")
}

func TestConfigFile(t *testing.T) {
	t.Parallel()
	r := find(t, doctor.Run(t.Context(), doctor.Config{DataDir: t.TempDir(), ConfigFile: "/etc/streamcrew.yaml", Listen: "127.0.0.1:0"}), "config file")
	assert.Equal(t, doctor.StatusOK, r.Status)
	assert.Equal(t, "/etc/streamcrew.yaml", r.Detail)
}
