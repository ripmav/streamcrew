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
)

// TestDiagBundle covers the diagnostics bundle (roadmap 6.3): it contains
// the version, the configuration without secrets, the paths, the profiles,
// the API tokens without their hashes and the log files.
func TestDiagBundle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := t.Context()

	// A profile and a token the bundle must mention.
	mgr := profile.NewManager(dir)
	_, err := mgr.Create(ctx, "Demo", "demo")
	require.NoError(t, err)
	_, value, err := apitoken.Open(dir).Create("diag", []apitoken.Scope{apitoken.Read})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir+"/logs", 0o700))
	require.NoError(t, os.WriteFile(dir+"/logs/streamcrew.log", []byte("a log line\n"), 0o600))

	cfg := config.Config{
		DataDir:         dir,
		Mode:            config.ModeDaemon,
		ShutdownTimeout: time.Second,
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
