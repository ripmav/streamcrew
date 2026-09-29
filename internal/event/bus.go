// SPDX-License-Identifier: Apache-2.0

package event

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// DefaultBuffer is the buffer size of a subscription unless WithBuffer sets
// another one.
const DefaultBuffer = 256

// lagWarnInterval limits the warnings about dropped events per subscription.
const lagWarnInterval = time.Minute

// Bus delivers envelopes to subscriptions. It is safe for concurrent use and
// starts no goroutines; create one with NewBus.
type Bus struct {
	logger  *slog.Logger
	catalog *Catalog

	mu     sync.RWMutex
	subs   map[*Subscription]struct{}
	closed bool
}

// BusOption configures a Bus.
type BusOption func(*Bus)

// WithCatalog makes Publish reject envelopes whose type or payload does not
// match the catalog.
func WithCatalog(c *Catalog) BusOption {
	return func(b *Bus) { b.catalog = c }
}

// NewBus returns a bus that logs through logger; a nil logger discards.
func NewBus(logger *slog.Logger, opts ...BusOption) *Bus {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	b := &Bus{logger: logger, subs: make(map[*Subscription]struct{})}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Publish offers e to every matching subscription without blocking. It
// returns an error only if e does not match the catalog. After Close,
// Publish discards e.
func (b *Bus) Publish(ctx context.Context, e Envelope) error {
	if b.catalog != nil {
		if err := b.catalog.Check(e); err != nil {
			return err
		}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return nil
	}
	for s := range b.subs {
		if s.matches(e) {
			s.offer(ctx, b.logger, e)
		}
	}
	return nil
}

// Subscribe returns a subscription that ends when ctx ends, when its Close
// is called or when the bus is closed. Its channel is closed then.
func (b *Bus) Subscribe(ctx context.Context, opts ...SubscribeOption) *Subscription {
	s := &Subscription{bus: b, buffer: DefaultBuffer}
	for _, opt := range opts {
		opt(s)
	}
	s.ch = make(chan Envelope, s.buffer)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(s.ch)
		return s
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()

	// If ctx has already ended, AfterFunc may call Close before the
	// assignment; watchMu makes Close wait for it.
	s.watchMu.Lock()
	s.stopWatching = context.AfterFunc(ctx, s.Close)
	s.watchMu.Unlock()
	return s
}

// Close ends all subscriptions. Later subscriptions end at once.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for s := range b.subs {
		delete(b.subs, s)
		close(s.ch)
	}
}

// remove ends one subscription; it reports whether it was still active.
func (b *Bus) remove(s *Subscription) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s]; !ok {
		return false
	}
	delete(b.subs, s)
	close(s.ch)
	return true
}

// SubscribeOption configures a subscription.
type SubscribeOption func(*Subscription)

// WithTypes limits a subscription to the given event types.
func WithTypes(types ...Type) SubscribeOption {
	return func(s *Subscription) { s.types = append(s.types, types...) }
}

// WithPrefixes limits a subscription to event types starting with one of the
// prefixes, e.g. "twitch.".
func WithPrefixes(prefixes ...string) SubscribeOption {
	return func(s *Subscription) { s.prefixes = append(s.prefixes, prefixes...) }
}

// WithFilter limits a subscription to envelopes for which f returns true. It
// runs in the publisher's goroutine and must be fast and not block.
func WithFilter(f func(Envelope) bool) SubscribeOption {
	return func(s *Subscription) { s.filter = f }
}

// WithBuffer sets the buffer size. Consumers that must not miss events use a
// large buffer and hand events on to their own queue at once.
func WithBuffer(n int) SubscribeOption {
	return func(s *Subscription) { s.buffer = max(n, 1) }
}

// WithName names the subscription in log messages.
func WithName(name string) SubscribeOption {
	return func(s *Subscription) { s.name = name }
}

// Subscription receives the envelopes of a bus that match its filters.
type Subscription struct {
	bus      *Bus
	ch       chan Envelope
	name     string
	buffer   int
	types    []Type
	prefixes []string
	filter   func(Envelope) bool

	watchMu      sync.Mutex
	stopWatching func() bool

	sendMu   sync.Mutex // serializes offers, so a lag notice precedes the next event
	dropped  uint64
	lastWarn time.Time
}

// C returns the channel of the subscription. It is closed when the
// subscription ends. An envelope of type TypeLag reports dropped events.
func (s *Subscription) C() <-chan Envelope {
	return s.ch
}

// Close ends the subscription. It is safe to call more than once.
func (s *Subscription) Close() {
	if !s.bus.remove(s) {
		return
	}
	s.watchMu.Lock()
	stop := s.stopWatching
	s.watchMu.Unlock()
	if stop != nil {
		stop()
	}
}

func (s *Subscription) matches(e Envelope) bool {
	if len(s.types) > 0 || len(s.prefixes) > 0 {
		ok := false
		for _, t := range s.types {
			if t == e.Type {
				ok = true
				break
			}
		}
		for _, p := range s.prefixes {
			if !ok && strings.HasPrefix(string(e.Type), p) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return s.filter == nil || s.filter(e)
}

// offer sends e without blocking. It is called with the bus read lock held,
// so the channel cannot be closed meanwhile.
func (s *Subscription) offer(ctx context.Context, logger *slog.Logger, e Envelope) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.dropped > 0 {
		lag := New(Source{Kind: SourceSystem, Name: "event"}, TypeLag, Lag{Dropped: s.dropped})
		select {
		case s.ch <- lag:
			s.dropped = 0
		default:
			s.drop(ctx, logger)
			return
		}
	}
	select {
	case s.ch <- e:
	default:
		s.drop(ctx, logger)
	}
}

func (s *Subscription) drop(ctx context.Context, logger *slog.Logger) {
	s.dropped++
	if now := time.Now(); now.Sub(s.lastWarn) >= lagWarnInterval {
		s.lastWarn = now
		logger.WarnContext(ctx, "event subscription is lagging, dropping events",
			"subscription", s.name, "dropped", s.dropped, "buffer", s.buffer)
	}
}
