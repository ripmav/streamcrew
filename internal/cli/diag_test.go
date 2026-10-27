// SPDX-License-Identifier: MIT

package cli

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/apitoken"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
)

// TestDiagBundle covers the diagnostics bundle (roadmap 6.3): it contains
// the version, the configuration without secrets, the paths, the profiles,
// the API tokens without their hashes and the log files.
func TestDiagBundle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := t.Context()

	// A profile and a token the bundle must mention; the token lives in
	// the profile database (S5 of the code review 2026-10-24).
	mgr := profile.NewManager(dir)
	prof, err := mgr.Create(ctx, "Demo", "demo")
	require.NoError(t, err)
	s, err := store.Open(ctx, prof.Path)
	require.NoError(t, err)
	_, value, err := apitoken.New(s).Create(ctx, "diag", []apitoken.Scope{apitoken.Read})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	require.NoError(t, os.MkdirAll(dir+"/logs", 0o700))
	require.NoError(t, os.WriteFile(dir+"/logs/streamcrew.log", []byte("a log line\n"), 0o600))

	cfg := config.Config{
		DataDir:         dir,
		Profile:         "demo",
		Mode:            config.ModeDaemon,
		ShutdownTimeout: time.Second,
		Stream:          config.StreamConfig{Buffer: 1024, CatchUp: 256},
		Log:             config.LogConfig{Level: "info", Format: "text", MaxSize: 1},
	}
	require.NoError(t, cfg.Resolve())
	out := filepath.Join(t.TempDir(), "bundle.zip")
	env := &Env{Stdout: io.Discard, Stderr: io.Discard, Config: &cfg, File: config.NewFileResolver("")}
	require.NoError(t, diagBundleCmd{Out: out}.Run(ctx, env))

	zf, err := zip.OpenReader(out)
	require.NoError(t, err)
	defer zf.Close()
	files := map[string]string{}
	for _, f := range zf.File {
		rc, err := f.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		files[f.Name] = string(data)
	}

	assert.Contains(t, files, "README.txt")
	assert.Contains(t, files, "version.txt")
	assert.Contains(t, files, "config.yaml")
	assert.Contains(t, files, "paths.txt")
	assert.Contains(t, files, "profiles.txt")
	assert.Contains(t, files, "api-tokens.txt")
	assert.Equal(t, "a log line\n", files["logs/streamcrew.log"])
	assert.Contains(t, files["profiles.txt"], "Demo (demo)")
	assert.Contains(t, files["api-tokens.txt"], "diag")

	// No secret leaves the data directory: the token value and the raw
	// token store are not in the bundle.
	for name, content := range files {
		assert.NotContains(t, content, value, "the token value in %s", name)
	}
	assert.NotContains(t, files, "api-tokens.json")
}
