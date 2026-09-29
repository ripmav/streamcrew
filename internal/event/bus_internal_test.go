// SPDX-License-Identifier: Apache-2.0

package event

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBusCloseStopsWatchingContexts is a regression test for the review of
// PR #17: Bus.Close must stop the context watch of every subscription, like
// Subscription.Close does, so that a long-lived context does not keep them.
func TestBusCloseStopsWatchingContexts(t *testing.T) {
	t.Parallel()
	b := NewBus(slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := b.Subscribe(ctx)

	b.Close()
	_, open := <-s.C()
	assert.False(t, open, "the channel is closed")
	s.watchMu.Lock()
	stop := s.stopWatching
	s.watchMu.Unlock()
	assert.False(t, stop(), "Bus.Close already stopped the watch")
}

// TestSubscribeAfterCloseDoesNotWatch covers a subscription made after the
// bus was closed: it ends at once and never watches its context.
func TestSubscribeAfterCloseDoesNotWatch(t *testing.T) {
	t.Parallel()
	b := NewBus(slog.New(slog.DiscardHandler))
	b.Close()
	s := b.Subscribe(t.Context())
	_, open := <-s.C()
	assert.False(t, open)
	assert.Nil(t, s.stopWatching)
}
