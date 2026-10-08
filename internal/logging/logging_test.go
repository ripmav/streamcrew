// SPDX-License-Identifier: MIT

package logging_test

import (
	"bufio"
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/logging"
)

// newLogger returns a logger writing JSON to the returned buffer.
func newLogger(t *testing.T, cfg logging.Config) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	cfg.Console = &buf
	cfg.ConsoleFormat = logging.FormatJSON
	logger, closer, err := logging.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })
	return logger, &buf
}

// records decodes JSON lines.
func records(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var rec map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &rec), "line %q", sc.Text())
		out = append(out, rec)
	}
	require.NoError(t, sc.Err())
	return out
}

func TestComponentLevels(t *testing.T) {
	t.Parallel()
	logger, buf := newLogger(t, logging.Config{
		Level:           slog.LevelInfo,
		ComponentLevels: map[string]slog.Level{"chatty": slog.LevelDebug, "quiet": slog.LevelError},
	})
	ctx := t.Context()

	chatty := logger.With(logging.ComponentKey, "chatty")
	quiet := logger.With(logging.ComponentKey, "quiet")
	other := logger.With(logging.ComponentKey, "other")
	grouped := logger.WithGroup("request").With(logging.ComponentKey, "chatty")

	chatty.DebugContext(ctx, "chatty debug")
	quiet.WarnContext(ctx, "quiet warn")
	quiet.ErrorContext(ctx, "quiet error")
	other.DebugContext(ctx, "other debug")
	other.InfoContext(ctx, "other info")
	grouped.DebugContext(ctx, "grouped debug")
	chatty.With("key", "value").DebugContext(ctx, "chatty child debug")

	var msgs []string
	for _, rec := range records(t, buf.Bytes()) {
		msgs = append(msgs, rec[slog.MessageKey].(string))
	}
	assert.Equal(t, []string{"chatty debug", "quiet error", "other info", "chatty child debug"}, msgs)
}

func TestRedaction(t *testing.T) {
	t.Parallel()
	logger, buf := newLogger(t, logging.Config{Level: slog.LevelInfo})

	logger.With("api_key", "k1").InfoContext(t.Context(), "request",
		"access_token", "t1",
		"authorization", "Bearer t2",
		"user", "alice",
		"value", logging.Secret("s1"),
		slog.Group("oauth", "client_secret", "s2", "scope", "chat:read"),
	)

	out := buf.String()
	for _, secret := range []string{"k1", "t1", "t2", "s1", "s2"} {
		assert.NotContains(t, out, secret)
	}
	recs := records(t, buf.Bytes())
	require.Len(t, recs, 1)
	rec := recs[0]
	assert.Equal(t, logging.Redacted, rec["api_key"])
	assert.Equal(t, logging.Redacted, rec["access_token"])
	assert.Equal(t, logging.Redacted, rec["authorization"])
	assert.Equal(t, logging.Redacted, rec["value"])
	assert.Equal(t, "alice", rec["user"])
	assert.Equal(t, map[string]any{"client_secret": logging.Redacted, "scope": "chat:read"}, rec["oauth"])
}

func TestSecretFormatting(t *testing.T) {
	t.Parallel()
	s := logging.Secret("hunter2")

	assert.Equal(t, "hunter2", s.Reveal())
	for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
		assert.NotContains(t, fmt.Sprintf(format, s), "hunter2", format)
	}
	out, err := json.Marshal(struct{ Token logging.Secret }{s})
	require.NoError(t, err)
	assert.JSONEq(t, `{"Token":"[REDACTED]"}`, string(out))
}

func TestTextConsole(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, closer, err := logging.New(logging.Config{Level: slog.LevelInfo, Console: &buf})
	require.NoError(t, err)
	defer closer.Close()

	logger.InfoContext(t.Context(), "started", "addr", "127.0.0.1:8740")
	assert.Contains(t, buf.String(), `msg=started addr=127.0.0.1:8740`)
}

func TestUnknownConsoleFormat(t *testing.T) {
	t.Parallel()
	_, _, err := logging.New(logging.Config{Console: &bytes.Buffer{}, ConsoleFormat: "xml"})
	assert.ErrorContains(t, err, "xml")
}

func TestFileOutput(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "logs", "streamcrew.log")
	logger, closer, err := logging.New(logging.Config{Level: slog.LevelInfo, File: path, MaxSize: 1 << 20, MaxFiles: 1})
	require.NoError(t, err)

	logger.With(logging.ComponentKey, "test").InfoContext(t.Context(), "to file", "password", "p1")
	require.NoError(t, closer.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	recs := records(t, data)
	require.Len(t, recs, 1)
	assert.Equal(t, "to file", recs[0][slog.MessageKey])
	assert.Equal(t, "test", recs[0][logging.ComponentKey])
	assert.Equal(t, logging.Redacted, recs[0]["password"])
}

func TestNoOutputs(t *testing.T) {
	t.Parallel()
	logger, closer, err := logging.New(logging.Config{})
	require.NoError(t, err)
	logger.ErrorContext(t.Context(), "goes nowhere")
	assert.NoError(t, closer.Close())
}
