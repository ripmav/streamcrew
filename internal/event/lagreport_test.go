// SPDX-License-Identifier: MIT

package event

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dropEnv is a test envelope for the drop tests.
func dropEnv(n int) Envelope { return New(Source{Kind: SourceSystem, Name: "test"}, "test.drop", n) }

// safeBuffer is a log sink readable while the reporter writes to it.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *safeBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

// TestSubscriptionTotalCountsDropsMonotonic covers the monotonic drop
// counter (roadmap 6.2.7): it counts every drop and, unlike the resetting
// counter, it keeps its value after the lag envelope is delivered.
func TestSubscriptionTotalCountsDropsMonotonic(t *testing.T) {
	t.Parallel()
	b := NewBus(nil)
	// A buffer of two: the lag envelope needs a slot of its own, so the
	// event after it fits next to it.
	s := b.Subscribe(t.Context(), WithBuffer(2))
	for i := range 5 {
		require.NoError(t, b.Publish(t.Context(), dropEnv(i)))
	}
	// Two events are in the buffer, three were dropped; the counter is a
	// monotonic total, readable without draining.
	assert.Equal(t, uint64(3), s.Count())
	for range 2 {
		_, ok := <-s.C()
		require.True(t, ok)
	}
	// The next offer delivers the lag envelope first and the event next to
	// it; afterwards the resetting counter is zero, the monotonic one is
	// not.
	require.NoError(t, b.Publish(t.Context(), dropEnv(5)))
	lag, ok := <-s.C()
	require.True(t, ok)
	require.Equal(t, TypeLag, lag.Type)
	_, ok = <-s.C()
	require.True(t, ok)
	s.Close()
	assert.Equal(t, uint64(0), s.dropped)
	assert.Equal(t, uint64(3), s.Count())
}

// TestDropCountersListsActiveSubscriptions covers the pull accessor
// (roadmap 6.2.7): live subscriptions appear with their names, ended ones
// fall out of the snapshot.
func TestDropCountersListsActiveSubscriptions(t *testing.T) {
	t.Parallel()
	b := NewBus(nil)
	one := b.Subscribe(t.Context(), WithName("one"))
	b.Subscribe(t.Context(), WithName("two"))
	names := map[string]bool{}
	for _, c := range b.DropCounters() {
		names[c.Name()] = true
	}
	assert.True(t, names["one"] && names["two"])
	one.Close()
	for _, c := range b.DropCounters() {
		assert.NotEqual(t, "one", c.Name())
	}
}

// TestLagReporterLogsDropsInTheWindow covers the reporter (roadmap 6.2.7):
// after a quiet window it logs the per-window drop count of a lagging
// subscription; without drops it stays silent.
func TestLagReporterLogsDropsInTheWindow(t *testing.T) {
	t.Parallel()
	out := &safeBuffer{}
	b := NewBus(nil)
	b.Subscribe(t.Context(), WithBuffer(1), WithName("laggy"))
	rep := NewLagReporter(b, slog.New(slog.NewTextHandler(out, nil))).WithReportInterval(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go rep.Run(ctx)

	// Let the first window pass without drops: it stays silent.
	time.Sleep(40 * time.Millisecond)
	assert.Empty(t, out.String(), "no drops, no log")

	for i := range 4 {
		require.NoError(t, b.Publish(t.Context(), dropEnv(i)))
	}
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "laggy") }, time.Second, time.Millisecond, "the drop warning")
	msg := out.String()
	assert.Contains(t, msg, "drops=3") // 4 published, 1 buffered
	assert.Contains(t, msg, "total=3") // nothing was consumed
	assert.Contains(t, msg, "window=10ms")
}

// TestLagReporterStopsWithTheContext covers that the reporter stops logging
// once its context ends (roadmap 6.2.7).
func TestLagReporterStopsWithTheContext(t *testing.T) {
	t.Parallel()
	out := &safeBuffer{}
	b := NewBus(nil)
	b.Subscribe(t.Context(), WithBuffer(1), WithName("laggy"))
	rep := NewLagReporter(b, slog.New(slog.NewTextHandler(out, nil))).WithReportInterval(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	go rep.Run(ctx)
	// Let a quiet window pass first, so the reporter sees the drops
	// between two polls instead of in its baseline.
	time.Sleep(40 * time.Millisecond)
	for i := range 4 {
		require.NoError(t, b.Publish(t.Context(), dropEnv(i)))
	}
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "laggy") }, time.Second, time.Millisecond, "the first warning")
	size := out.Len()
	cancel()
	time.Sleep(50 * time.Millisecond)
	for i := range 4 {
		require.NoError(t, b.Publish(t.Context(), dropEnv(i)))
	}
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, size, out.Len(), "no further warning after cancel")
}
