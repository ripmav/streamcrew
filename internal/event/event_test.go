// SPDX-License-Identifier: MIT

package event_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/event"
)

type follow struct{ User string }

func ev(typ event.Type, payload any) event.Envelope {
	return event.New(event.Source{Kind: event.SourcePlatform, Name: "twitch"}, typ, payload)
}

// drain returns the envelopes currently buffered in s.
func drain(s *event.Subscription) []event.Envelope {
	var out []event.Envelope
	for {
		select {
		case e, ok := <-s.C():
			if !ok {
				return out
			}
			out = append(out, e)
		default:
			return out
		}
	}
}

func types(envs []event.Envelope) []event.Type {
	out := make([]event.Type, 0, len(envs))
	for _, e := range envs {
		out = append(out, e.Type)
	}
	return out
}

func TestNewEnvelope(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := ev("twitch.channel.follow", follow{User: "alice"})
		assert.False(t, e.ID.IsZero())
		assert.Equal(t, time.Now().UTC(), e.Time)
		assert.Equal(t, time.UTC, e.Time.Location())

		p, ok := event.Payload[follow](e)
		assert.True(t, ok)
		assert.Equal(t, "alice", p.User)
		_, ok = event.Payload[string](e)
		assert.False(t, ok)
	})
}

func TestCatalog(t *testing.T) {
	t.Parallel()
	c := event.NewCatalog()
	require.NoError(t, event.Register[follow](c, "twitch.channel.follow"))
	require.ErrorContains(t, event.Register[follow](c, "twitch.channel.follow"), "already registered")
	for _, bad := range []event.Type{"follow", "twitch..follow", "Twitch.follow", "twitch.channel-follow", ".chat"} {
		assert.Error(t, event.Register[follow](c, bad), bad)
	}

	require.NoError(t, c.Check(ev("twitch.channel.follow", follow{})))
	require.ErrorIs(t, c.Check(ev("twitch.channel.raid", follow{})), event.ErrUnknownType)
	require.ErrorIs(t, c.Check(ev("twitch.channel.follow", "alice")), event.ErrPayloadType)
	assert.Equal(t, []event.Type{event.TypeLag, "twitch.channel.follow"}, c.Types())
}

func TestFiltersAndOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	bus := event.NewBus(nil)
	all := bus.Subscribe(ctx)
	exact := bus.Subscribe(ctx, event.WithTypes("chat.message"))
	prefix := bus.Subscribe(ctx, event.WithPrefixes("twitch."))
	filtered := bus.Subscribe(ctx, event.WithFilter(func(e event.Envelope) bool { return e.Source.Name == "kick" }))

	published := []event.Type{"chat.message", "twitch.channel.follow", "twitch.channel.raid", "app.started"}
	for _, typ := range published {
		require.NoError(t, bus.Publish(ctx, ev(typ, nil)))
	}
	require.NoError(t, bus.Publish(ctx, event.New(event.Source{Kind: event.SourcePlatform, Name: "kick"}, "kick.chat.message", nil)))

	assert.Equal(t, append(published, "kick.chat.message"), types(drain(all)), "all events in publish order")
	assert.Equal(t, []event.Type{"chat.message"}, types(drain(exact)))
	assert.Equal(t, []event.Type{"twitch.channel.follow", "twitch.channel.raid"}, types(drain(prefix)))
	assert.Equal(t, []event.Type{"kick.chat.message"}, types(drain(filtered)))
}

func TestPublishNeverBlocksAndReportsLag(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		bus := event.NewBus(slog.New(slog.NewTextHandler(&logs, nil)))
		slow := bus.Subscribe(t.Context(), event.WithBuffer(2), event.WithName("slow"))

		// Nobody reads: the publisher must not block (synctest would report
		// a deadlock).
		for range 10 {
			require.NoError(t, bus.Publish(t.Context(), ev("chat.message", nil)))
		}
		got := drain(slow)
		assert.Len(t, got, 2, "the buffer holds the first two events")
		assert.Equal(t, 1, strings.Count(logs.String(), "event subscription is lagging"), "one warning per minute")

		require.NoError(t, bus.Publish(t.Context(), ev("app.started", nil)))
		got = drain(slow)
		require.Len(t, got, 2)
		assert.Equal(t, event.TypeLag, got[0].Type, "the lag notice comes before the next event")
		lag, ok := event.Payload[event.Lag](got[0])
		require.True(t, ok)
		assert.Equal(t, uint64(8), lag.Dropped)
		assert.Equal(t, event.Type("app.started"), got[1].Type)

		// No lag once the subscriber keeps up.
		require.NoError(t, bus.Publish(t.Context(), ev("chat.message", nil)))
		assert.Equal(t, []event.Type{"chat.message"}, types(drain(slow)))

		// The next warning comes after a minute at the earliest.
		for range 3 {
			require.NoError(t, bus.Publish(t.Context(), ev("chat.message", nil)))
		}
		assert.Equal(t, 1, strings.Count(logs.String(), "event subscription is lagging"))
		time.Sleep(time.Minute)
		require.NoError(t, bus.Publish(t.Context(), ev("chat.message", nil)))
		assert.Equal(t, 2, strings.Count(logs.String(), "event subscription is lagging"))
		assert.Contains(t, logs.String(), "subscription=slow")
	})
}

func TestLagNoticeWaitsForSpace(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(nil)
	s := bus.Subscribe(t.Context(), event.WithBuffer(1))
	for range 3 {
		require.NoError(t, bus.Publish(t.Context(), ev("chat.message", nil)))
	}
	// Buffer full with the first event; two dropped.
	<-s.C()
	require.NoError(t, bus.Publish(t.Context(), ev("app.started", nil)))
	// Only the lag notice fits; the new event is dropped as well.
	e := <-s.C()
	require.Equal(t, event.TypeLag, e.Type)
	lag, _ := event.Payload[event.Lag](e)
	assert.Equal(t, uint64(2), lag.Dropped)

	require.NoError(t, bus.Publish(t.Context(), ev("app.stopping", nil)))
	e = <-s.C()
	require.Equal(t, event.TypeLag, e.Type)
	lag, _ = event.Payload[event.Lag](e)
	assert.Equal(t, uint64(1), lag.Dropped, "the event dropped while the lag notice took the slot")
}

func TestSubscriptionEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		bus := event.NewBus(nil)

		ctx, cancel := context.WithCancel(t.Context())
		byCtx := bus.Subscribe(ctx)
		byClose := bus.Subscribe(t.Context())
		cancel()
		byClose.Close()
		byClose.Close() // idempotent
		synctest.Wait()

		for _, s := range []*event.Subscription{byCtx, byClose} {
			_, open := <-s.C()
			assert.False(t, open)
		}
		require.NoError(t, bus.Publish(t.Context(), ev("chat.message", nil)), "no send on a closed channel")

		done, stop := context.WithCancel(t.Context())
		stop()
		already := bus.Subscribe(done)
		synctest.Wait()
		_, open := <-already.C()
		assert.False(t, open, "a subscription with an ended context ends at once")
	})
}

func TestBusClose(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(nil)
	s := bus.Subscribe(t.Context())
	bus.Close()
	bus.Close()

	_, open := <-s.C()
	assert.False(t, open)
	require.NoError(t, bus.Publish(t.Context(), ev("chat.message", nil)), "publish after close is discarded")
	late := bus.Subscribe(t.Context())
	_, open = <-late.C()
	assert.False(t, open)
	s.Close()
}

func TestPublishChecksCatalog(t *testing.T) {
	t.Parallel()
	c := event.NewCatalog()
	require.NoError(t, event.Register[follow](c, "twitch.channel.follow"))
	bus := event.NewBus(nil, event.WithCatalog(c))
	s := bus.Subscribe(t.Context())

	require.NoError(t, bus.Publish(t.Context(), ev("twitch.channel.follow", follow{User: "bob"})))
	require.ErrorIs(t, bus.Publish(t.Context(), ev("twitch.channel.raid", nil)), event.ErrUnknownType)
	require.ErrorIs(t, bus.Publish(t.Context(), ev("twitch.channel.follow", "bob")), event.ErrPayloadType)
	assert.Len(t, drain(s), 1)
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(nil)
	ctx := t.Context()
	var wg sync.WaitGroup
	received := make([]int, 4)
	for i := range received {
		s := bus.Subscribe(ctx, event.WithBuffer(10000))
		wg.Go(func() {
			for e := range s.C() {
				if e.Type == "chat.message" {
					received[i]++
				}
			}
		})
	}
	var pubs sync.WaitGroup
	for range 8 {
		pubs.Go(func() {
			for range 500 {
				assert.NoError(t, bus.Publish(ctx, ev("chat.message", nil)))
			}
		})
	}
	pubs.Wait()
	bus.Close()
	wg.Wait()
	for i, n := range received {
		assert.Equal(t, 4000, n, "subscriber %d", i)
	}
}
