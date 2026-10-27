// SPDX-License-Identifier: MIT

package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/config"
)

// narrowBase returns a server configuration with the security settings
// the narrowing check measures against (Code-ADR-0021, Nachtrag
// 2026-10-24).
func narrowBase(t *testing.T, obs string) config.Config {
	t.Helper()
	cfg := base(t)
	cfg.Dev = true
	cfg.CorsAllow = []string{"http://a.example"}
	cfg.TLSCert, cfg.TLSKey = "/cert.pem", "/key.pem"
	cfg.Grant = []string{"host:fs"}
	cfg.Revoke = []string{"script"}
	cfg.FileRoot = map[string]string{"obs": obs}
	cfg.OutboundAllow = []string{"nas"}
	return cfg
}

// narrowDoc returns the complete document that holds the configuration of
// narrowBase: Update takes complete documents, a missing list key resets
// the setting.
func narrowDoc(obs string) string {
	return "mode: server\n" +
		"listen: 127.0.0.1:8740\n" +
		"dev: true\n" +
		"cors_allow: [http://a.example]\n" +
		"tls_cert: /cert.pem\n" +
		"tls_key: /key.pem\n" +
		"grant: [host:fs]\n" +
		"revoke: [script]\n" +
		"file_root:\n" +
		"  obs: " + obs + "\n" +
		"outbound_allow: [nas]\n"
}

// TestWatcherUpdateOnlyNarrows covers the narrowing rule of
// ConfigService/Update (Code-ADR-0021, Nachtrag 2026-10-24, points 1 to
// 3): a document that extends the currently effective configuration is
// rejected as a whole, names the keys it extends, and is not written.
func TestWatcherUpdateOnlyNarrows(t *testing.T) {
	t.Parallel()
	obs, other := t.TempDir(), t.TempDir()
	base := narrowBase(t, obs)

	cases := []struct {
		name   string
		doc    string
		denied []string // the keys the error names
	}{
		{"the same document is accepted", narrowDoc(obs), nil},
		{"a deeper revoke narrows", strings.Replace(narrowDoc(obs), "revoke: [script]\n", "revoke: [script, net:outbound]\n", 1), nil},
		{"a new grant entry extends", strings.Replace(narrowDoc(obs), "grant: [host:fs]\n", "grant: [host:fs, host:process]\n", 1), []string{"grant", "revoke"}},
		{"a removed revoke entry extends", strings.Replace(narrowDoc(obs), "revoke: [script]\n", "", 1), []string{"grant", "revoke"}},
		{"a new file root extends", strings.Replace(narrowDoc(obs), "  obs: "+obs+"\n", "  obs: "+obs+"\n  more: "+other+"\n", 1), []string{"file-root"}},
		{"a file root that moves extends", strings.Replace(narrowDoc(obs), "  obs: "+obs+"\n", "  obs: "+other+"\n", 1), []string{"file-root"}},
		{"a file root into a subdirectory narrows", strings.Replace(narrowDoc(obs), "  obs: "+obs+"\n", "  obs: "+obs+"/sub\n", 1), nil},
		{"a removed file root narrows", strings.Replace(narrowDoc(obs), "file_root:\n  obs: "+obs+"\n", "", 1), nil},
		{"a new outbound target extends", strings.Replace(narrowDoc(obs), "outbound_allow: [nas]\n", "outbound_allow: [nas, pc]\n", 1), []string{"outbound-allow"}},
		{"a removed outbound target narrows", strings.Replace(narrowDoc(obs), "outbound_allow: [nas]\n", "outbound_allow: []\n", 1), nil},
		{"a mode change extends", strings.Replace(narrowDoc(obs), "mode: server\n", "mode: daemon\n", 1), []string{"mode", "grant", "revoke"}},
		{"a new CORS origin extends", strings.Replace(narrowDoc(obs), "cors_allow: [http://a.example]\n", "cors_allow: [http://a.example, http://b.example]\n", 1), []string{"cors-allow"}},
		{"an emptied CORS allowlist narrows", strings.Replace(narrowDoc(obs), "cors_allow: [http://a.example]\n", "cors_allow: []\n", 1), nil},
		{"another port on the same host narrows", strings.Replace(narrowDoc(obs), "listen: 127.0.0.1:8740\n", "listen: 127.0.0.1:9000\n", 1), nil},
		// dev is off: the developer mode requires a loopback address
		// (Resolve), so a public bind comes without it.
		{"a new host extends", strings.Replace(strings.Replace(narrowDoc(obs), "dev: true\n", "dev: false\n", 1), "listen: 127.0.0.1:8740\n", "listen: 0.0.0.0:8740\n", 1), []string{"listen"}},
		{"a Unix socket is local", strings.Replace(narrowDoc(obs), "listen: 127.0.0.1:8740\n", "listen: unix:/tmp/core.sock\n", 1), nil},
		{"removing TLS on loopback is allowed", strings.Replace(narrowDoc(obs), "tls_cert: /cert.pem\ntls_key: /key.pem\n", "tls_cert: \"\"\ntls_key: \"\"\n", 1), nil},
		{"turning the developer mode off narrows", strings.Replace(narrowDoc(obs), "dev: true\n", "dev: false\n", 1), nil},
		// The document is rejected as a whole: the extension is named, and
		// the file is not written. dev is off, see above.
		{"an extension rejects the whole document", strings.Replace(strings.Replace(narrowDoc(obs), "dev: true\n", "dev: false\n", 1), "listen: 127.0.0.1:8740\n", "listen: 0.0.0.0:8740\n", 1), []string{"listen"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path, f := updateFile(t, "log_level: debug\n")
			w := config.NewWatcher(f, base, nil, nil, testLogger(t))

			_, _, _, err := w.Update(t.Context(), []byte(tc.doc))
			if tc.denied == nil {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, config.ErrNarrowOnly)
			for _, key := range tc.denied {
				assert.Contains(t, err.Error(), key)
			}
			assert.Contains(t, err.Error(), "configuration file", "the hint where the keys can be extended")

			// The file keeps the content last seen.
			data, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, "log_level: debug\n", string(data))
		})
	}
}

// TestWatcherUpdateDeniesTLSRemovalBeyondLoopback covers the TLS rule
// (Code-ADR-0021, Nachtrag 2026-10-24, point 2): removing the certificate
// while the bind reaches beyond loopback extends, swapping it stays
// allowed.
func TestWatcherUpdateDeniesTLSRemovalBeyondLoopback(t *testing.T) {
	t.Parallel()
	obs := t.TempDir()
	base := narrowBase(t, obs)
	base.Listen = "0.0.0.0:8740"
	base.Dev = false // the developer mode requires a loopback address

	doc := strings.Replace(strings.Replace(narrowDoc(obs), "dev: true\n", "dev: false\n", 1), "listen: 127.0.0.1:8740\n", "listen: 0.0.0.0:8740\n", 1)
	for _, tc := range []struct {
		name string
		doc  string
		deny bool
	}{
		{"removing TLS extends", strings.Replace(doc, "tls_cert: /cert.pem\ntls_key: /key.pem\n", "tls_cert: \"\"\ntls_key: \"\"\n", 1), true},
		{"swapping the certificate is allowed", strings.Replace(doc, "tls_cert: /cert.pem\ntls_key: /key.pem\n", "tls_cert: /other.pem\ntls_key: /other.key\n", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, f := updateFile(t, "")
			w := config.NewWatcher(f, base, nil, nil, testLogger(t))
			_, _, _, err := w.Update(t.Context(), []byte(tc.doc))
			if tc.deny {
				require.Error(t, err)
				assert.ErrorIs(t, err, config.ErrNarrowOnly)
				assert.Contains(t, err.Error(), "tls-cert")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestWatcherUpdateMeasuresAgainstTheCurrentState verifies the narrowing
// check measures against the currently effective configuration, not the
// start (Code-ADR-0021, Nachtrag 2026-10-24, point 1): what the file
// narrowed, the API cannot widen back, and what the file released stays
// allowed.
func TestWatcherUpdateMeasuresAgainstTheCurrentState(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	watch(t, dataDir, nil, nil, func(w *watched) {
		// The file narrows the start: script is revoked.
		w.write("revoke: [script]\n")
		assert.False(t, w.has(capability.Script))

		// The API keeps the current state; the bytes differ from the
		// file, so the check runs.
		_, _, _, err := w.watcher.Update(t.Context(), []byte("revoke: [\"script\"]\n"))
		require.NoError(t, err)

		// Re-granting script would extend the current state: denied,
		// although the start had it.
		_, _, _, err = w.watcher.Update(t.Context(), []byte("revoke: []\n"))
		require.ErrorIs(t, err, config.ErrNarrowOnly)
		assert.Contains(t, err.Error(), "grant")
	})
}
