// SPDX-License-Identifier: MIT

package config_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/config"
)

// caps returns the names of the capabilities in s.
func caps(s capability.Set) []string {
	names := []string{}
	for _, c := range s.List() {
		names = append(names, string(c))
	}
	return names
}

// TestDefaultCapabilities covers Code-ADR-0019, point 1.
func TestDefaultCapabilities(t *testing.T) {
	t.Parallel()
	local := []string{"host:fs", "host:process", "host:audio", "net:outbound", "script"}
	assert.Equal(t, local, caps(config.DefaultCapabilities(config.ModeDesktop)))
	assert.Equal(t, local, caps(config.DefaultCapabilities(config.ModeDaemon)))
	assert.Equal(t, []string{"net:outbound", "script"}, caps(config.DefaultCapabilities(config.ModeServer)))
	assert.Empty(t, caps(config.DefaultCapabilities("kiosk")))
}

// TestRightsFromSources covers Code-ADR-0019, points 2 to 4: the settings
// come from flags, environment variables and the file like every other
// setting (Code-ADR-0005).
func TestRightsFromSources(t *testing.T) {
	clearEnv(t)
	dataDir, obs, clips := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dataDir, "config.yaml"), `
mode: server
grant: [host:fs]
revoke:
  - script
file_root:
  obs: `+obs+`
outbound_allow: [192.168.1.0/24, HomeAssistant]
`)

	t.Run("file", func(t *testing.T) {
		cfg, file := parse(t, config.Defaults{DataDir: dataDir})
		require.NoError(t, file.Err())
		require.NoError(t, cfg.Resolve())
		r, err := cfg.Rights()
		require.NoError(t, err)
		assert.Equal(t, config.ModeServer, r.Mode)
		assert.Equal(t, []string{"host:fs", "net:outbound"}, caps(r.Capabilities))
		assert.Equal(t, map[string]string{"obs": obs}, r.Roots)
		assert.Equal(t, []string{"192.168.1.0/24", "homeassistant"}, r.Outbound.Entries())
		assert.True(t, r.Outbound.Contains(netip.MustParseAddr("192.168.1.7")))
	})
	t.Run("environment replaces the file", func(t *testing.T) {
		t.Setenv("STREAMCREW_GRANT", "host:process,host:audio")
		t.Setenv("STREAMCREW_FILE_ROOT", "clips="+clips+";obs="+obs)
		cfg, file := parse(t, config.Defaults{DataDir: dataDir})
		require.NoError(t, file.Err())
		require.NoError(t, cfg.Resolve())
		r, err := cfg.Rights()
		require.NoError(t, err)
		assert.Equal(t, []string{"host:process", "host:audio", "net:outbound"}, caps(r.Capabilities))
		assert.Equal(t, map[string]string{"clips": clips, "obs": obs}, r.Roots)
	})
	t.Run("flags replace the file", func(t *testing.T) {
		cfg, file := parse(t, config.Defaults{DataDir: dataDir},
			"--grant", "host:process", "--grant", "host:fs", "--revoke", "net:outbound", "--outbound-allow", "10.0.0.1")
		require.NoError(t, file.Err())
		require.NoError(t, cfg.Resolve())
		r, err := cfg.Rights()
		require.NoError(t, err)
		assert.Equal(t, []string{"host:fs", "host:process", "script"}, caps(r.Capabilities), "revoke from the file no longer applies")
		assert.Equal(t, []string{"10.0.0.1"}, r.Outbound.Entries())
	})
	t.Run("config show", func(t *testing.T) {
		cfg, _ := parse(t, config.Defaults{DataDir: dataDir})
		require.NoError(t, cfg.Resolve())
		v := cfg.View()
		assert.Equal(t, []string{"host:fs"}, v.Grant)
		assert.Equal(t, []string{"script"}, v.Revoke)
		assert.Equal(t, map[string]string{"obs": obs}, v.FileRoot)
		assert.Equal(t, []string{"192.168.1.0/24", "HomeAssistant"}, v.OutboundAllow, "as set")
	})
}

// TestRightsRejects covers Code-ADR-0019, points 2 to 4: every problem is
// an error that names its flag.
func TestRightsRejects(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	for _, tc := range []struct {
		name string
		edit func(c *config.Config)
		want string
	}{
		{"unknown capability", func(c *config.Config) { c.Grant = []string{"host:root"} }, `--grant: unknown capability "host:root"`},
		{"unknown revoke", func(c *config.Config) { c.Revoke = []string{"fs"} }, `--revoke: unknown capability "fs"`},
		{"grant and revoke", func(c *config.Config) {
			c.Grant, c.Revoke = []string{"host:fs"}, []string{"host:fs"}
		}, "--grant, --revoke: host:fs is in both"},
		{"root name", func(c *config.Config) { c.FileRoot = map[string]string{"OBS": "/srv/obs"} }, "--file-root OBS: the name is not"},
		{"long root name", func(c *config.Config) { c.FileRoot = map[string]string{strings.Repeat("a", 33): "/srv/x"} }, "the name is not"},
		{"relative root", func(c *config.Config) { c.FileRoot = map[string]string{"obs": "obs"} }, `--file-root obs: "obs" is not an absolute path`},
		{"home root", func(c *config.Config) { c.FileRoot = map[string]string{"obs": "~/obs"} }, "is not an absolute path"},
		{"data directory", func(c *config.Config) { c.FileRoot = map[string]string{"x": dataDir} }, "overlaps the data directory"},
		{"in the data directory", func(c *config.Config) {
			c.FileRoot = map[string]string{"x": filepath.Join(dataDir, "logs")}
		}, "overlaps the data directory"},
		{"contains the data directory", func(c *config.Config) {
			c.FileRoot = map[string]string{"x": filepath.Dir(dataDir)}
		}, "overlaps the data directory"},
		{"allowlist", func(c *config.Config) { c.OutboundAllow = []string{"*.local"} }, `--outbound-allow: invalid allowlist entry "*.local"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := config.Config{Mode: config.ModeDaemon, DataDir: dataDir}
			tc.edit(&c)
			_, err := c.Rights()
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// TestRightsDataDirLink: a root that reaches the data directory through a
// symbolic link is rejected as well.
func TestRightsDataDirLink(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.Symlink(dataDir, link))
	c := config.Config{Mode: config.ModeDaemon, DataDir: dataDir, FileRoot: map[string]string{"x": link}}
	_, err := c.Rights()
	require.ErrorContains(t, err, "overlaps the data directory")
}

// TestRightsWarnings covers Code-ADR-0019, point 7.
func TestRightsWarnings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plain := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(plain, nil, 0o600))

	c := config.Config{Mode: config.ModeServer, DataDir: t.TempDir(), Grant: []string{"host:process"},
		FileRoot: map[string]string{"gone": filepath.Join(dir, "gone"), "plain": plain, "ok": dir}}
	r, err := c.Rights()
	require.NoError(t, err)
	ws := r.Warnings()
	require.Len(t, ws, 4)
	assert.Equal(t, "host:process is on in server mode", ws[0])
	assert.Contains(t, ws[1], "file root gone:")
	assert.Equal(t, "file root plain: "+plain+" is not a directory", ws[2])
	assert.Equal(t, "the file roots have no effect without host:fs", ws[3])

	c = config.Config{Mode: config.ModeDaemon, DataDir: t.TempDir(), OutboundAllow: []string{"nas"},
		FileRoot: map[string]string{"ok": dir}}
	r, err = c.Rights()
	require.NoError(t, err)
	assert.Equal(t, []string{"the outbound allowlist has no effect outside server mode"}, r.Warnings())

	c = config.Config{Mode: config.ModeServer, DataDir: t.TempDir(), OutboundAllow: []string{"nas"}}
	r, err = c.Rights()
	require.NoError(t, err)
	assert.Empty(t, r.Warnings())
}

// TestLive covers Code-ADR-0019, point 5: the users of the rights read the
// current ones.
func TestLive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := config.Config{Mode: config.ModeDaemon, DataDir: t.TempDir(), FileRoot: map[string]string{"obs": dir},
		OutboundAllow: []string{"nas"}}
	r, err := c.Rights()
	require.NoError(t, err)
	live := config.NewLive(r)

	var src capability.Source = live
	assert.True(t, src.Current().Has(capability.HostFS))
	assert.True(t, live.HasRoot("obs"))
	got, ok := live.Root("obs")
	assert.True(t, ok)
	assert.Equal(t, dir, got)
	assert.True(t, live.Outbound().HasHost("nas"))

	r.Roots["clips"] = "/elsewhere"
	assert.False(t, live.HasRoot("clips"), "Live keeps its own copy")

	c = config.Config{Mode: config.ModeServer, DataDir: t.TempDir()}
	r, err = c.Rights()
	require.NoError(t, err)
	live.Store(r)
	assert.False(t, live.Current().Has(capability.HostFS))
	assert.False(t, live.HasRoot("obs"))
	assert.False(t, live.Outbound().HasHost("nas"))
	assert.True(t, live.Rights().Equal(r))
}

// TestProgramEnv covers actions.md B117: programs get the environment
// without the variables of streamcrew.
func TestProgramEnv(t *testing.T) {
	t.Setenv("STREAMCREW_SECRET_KEY", "secret")
	t.Setenv("Streamcrew_Mixed", "1")
	t.Setenv("STREAMCREWISH", "kept")
	t.Setenv("OTHER_VAR", "kept")
	env := config.ProgramEnv()
	assert.Contains(t, env, "OTHER_VAR=kept")
	assert.Contains(t, env, "STREAMCREWISH=kept")
	for _, kv := range env {
		assert.False(t, strings.HasPrefix(strings.ToUpper(kv), "STREAMCREW_"), kv)
	}
}
