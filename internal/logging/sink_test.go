// SPDX-License-Identifier: MIT

package logging

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordSink collects the records it receives.
type recordSink struct {
	mu      sync.Mutex
	records []slog.Record
}

func (s *recordSink) record(r slog.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
}

func (s *recordSink) Enabled(context.Context, slog.Level) bool { return true }
func (s *recordSink) Handle(_ context.Context, r slog.Record) error {
	s.record(r)
	return nil
}
func (s *recordSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *recordSink) WithGroup(string) slog.Handler      { return s }

// TestSetSinkDeliversRecordsBesideTheOutput covers the sink contract
// (roadmap 6.5): the sink receives every record the level filter passes,
// beside the output, and survives Update.
func TestSetSinkDeliversRecordsBesideTheOutput(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{Console: &buf, ConsoleFormat: "text", Level: slog.LevelWarn}
	logger, dyn, err := NewDynamic(cfg)
	require.NoError(t, err)
	require.NoError(t, dyn.Close())

	sink := &recordSink{}
	dyn.SetSink(sink)
	logger.Info("info stays out")
	logger.Warn("warn goes through")
	logger.Error("error goes through")
	assert.Len(t, sink.records, 2)
	assert.Equal(t, "warn goes through", sink.records[0].Message)

	// Update keeps the sink; the new level applies to it as well.
	require.NoError(t, dyn.Update(Config{
		Console: &buf, ConsoleFormat: "text", Level: slog.LevelDebug,
	}))
	logger.Info("info now goes through")
	logger.Debug("debug now goes through")
	require.Len(t, sink.records, 4)
	assert.Equal(t, "debug now goes through", sink.records[3].Message)
}

// TestSetSinkSeesComponentLoggers covers that the component loggers of the
// tree (With with the component attribute) deliver to the sink as well.
func TestSetSinkSeesComponentLoggers(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{Console: &buf, ConsoleFormat: "text", Level: slog.LevelDebug}
	logger, dyn, err := NewDynamic(cfg)
	require.NoError(t, err)
	require.NoError(t, dyn.Close())

	sink := &recordSink{}
	dyn.SetSink(sink)
	logger.With(slog.String(ComponentKey, "engine")).Info("a component line")
	require.Len(t, sink.records, 1)
}
