// SPDX-License-Identifier: MIT

// Package emote is the emote catalog of roadmap 4.4, task 9: one source
// per provider (Twitch, BetterTTV, FrankerFaceZ) behind a cache with an
// expiry and a fallback to the last good list, so the mapping of 4.3 and
// the templates resolve an emote ID to its code and image.
package emote

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Emote is an emote of one source: the code in the chat text and the base
// of its image URL, without the size suffix.
type Emote struct {
	// ID is the source-specific emote ID.
	ID string
	// Code is the emote code in the chat text, e.g. "Kappa".
	Code string
	// Image is the base of the emote image URL.
	Image string
}

// Source is a provider of the emotes a channel can use (roadmap 4.4,
// task 9). Emotes lists them for the channel given as its login.
type Source interface {
	// Name identifies the source, e.g. "twitch", "bttv", "ffz".
	Name() string
	// Emotes lists the emotes of the channel from the source; an empty
	// list for none.
	Emotes(ctx context.Context, channel string) ([]Emote, error)
}

// list is the cached emotes of one source for one channel.
type list struct {
	at    time.Time
	byID  map[string]Emote
	order []string
}

func makeList(emotes []Emote, at time.Time) list {
	l := list{at: at, byID: make(map[string]Emote, len(emotes))}
	for _, e := range emotes {
		if _, ok := l.byID[e.ID]; ok {
			continue
		}
		l.byID[e.ID] = e
		l.order = append(l.order, e.ID)
	}
	return l
}

// key is the cache key: the channel and the source.
type key struct {
	channel string
	source  string
}

// Catalog resolves the emotes of a channel from its sources. Every source
// is cached per channel with the TTL; a source that fails to refresh
// keeps its last good list (fallback), so an outage of a provider does not
// empty the catalog (roadmap 4.4, task 9).
type Catalog struct {
	sources []Source
	ttl     time.Duration
	now     func() time.Time
	log     *slog.Logger

	mu    sync.Mutex
	lists map[key]list
}

// New creates a Catalog over the sources in order. ttl is the cache
// lifetime; a non-positive ttl is the standard hour.
func New(sources []Source, ttl time.Duration, log *slog.Logger) *Catalog {
	if ttl <= 0 {
		ttl = time.Hour
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Catalog{sources: sources, ttl: ttl, now: time.Now, log: log, lists: make(map[key]list)}
}

// Refresh fetches the channel's emotes from every source whose cache is
// missing or older than the TTL. A source that fails keeps its last list
// (fallback); a source that never succeeded stays empty.
func (c *Catalog) Refresh(ctx context.Context, channel string) {
	for _, s := range c.sources {
		k := key{channel: channel, source: s.Name()}
		c.mu.Lock()
		stale := c.stale(k)
		c.mu.Unlock()
		if !stale {
			continue
		}
		emotes, err := s.Emotes(ctx, channel)
		if err != nil {
			c.log.WarnContext(ctx, "emote: keeping the last list", "source", s.Name(), "channel", channel, "err", err)
			continue
		}
		c.mu.Lock()
		c.lists[k] = makeList(emotes, c.now())
		c.mu.Unlock()
	}
}

// stale reports whether the source's list for the channel is missing or
// older than the TTL. The caller holds the lock.
func (c *Catalog) stale(k key) bool {
	l, ok := c.lists[k]
	if !ok {
		return true
	}
	return c.now().Sub(l.at) > c.ttl
}

// Resolve returns the emote with the ID for the channel, searching the
// sources in order. It reads the cache only.
func (c *Catalog) Resolve(channel, id string) (Emote, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.sources {
		if e, ok := c.lists[key{channel: channel, source: s.Name()}].byID[id]; ok {
			return e, true
		}
	}
	return Emote{}, false
}

// List is the emotes of the channel across the sources in order.
func (c *Catalog) List(channel string) []Emote {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Emote
	for _, s := range c.sources {
		l := c.lists[key{channel: channel, source: s.Name()}]
		for _, id := range l.order {
			out = append(out, l.byID[id])
		}
	}
	return out
}

// SourceList is the cached list of one source for a channel; empty if the
// source never refreshed the channel.
func (c *Catalog) SourceList(source, channel string) []Emote {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.lists[key{channel: channel, source: source}]
	out := make([]Emote, 0, len(l.order))
	for _, id := range l.order {
		out = append(out, l.byID[id])
	}
	return out
}

// Update replaces the cached list of one source for a channel — a live
// update from the source (the BetterTTV socket, plan phase 2); it bypasses
// the refresh of the TTL but keeps the age of the last poll, so the
// polling stays the safety net.
func (c *Catalog) Update(source, channel string, emotes []Emote) {
	c.mu.Lock()
	k := key{channel: channel, source: source}
	l := makeList(emotes, c.now())
	if old, ok := c.lists[k]; ok {
		l.at = old.at
	}
	c.lists[k] = l
	c.mu.Unlock()
}
