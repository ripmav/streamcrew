// SPDX-License-Identifier: MIT

package logging

import (
	"context"
	"log/slog"
	"maps"
	"sync"
)

// LevelStore holds the log level and the per-component levels of a
// Dynamic logger; changing them applies to every logger built from it
// without rebuilding (roadmap 6.6).
type LevelStore struct {
	mu     sync.RWMutex
	level  slog.Level
	levels map[string]slog.Level
}

// NewLevelStore returns a store with the given levels.
func NewLevelStore(level slog.Level, levels map[string]slog.Level) *LevelStore {
	return &LevelStore{level: level, levels: maps.Clone(levels)}
}

// Set changes the level and the per-component levels.
func (s *LevelStore) Set(level slog.Level, levels map[string]slog.Level) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.level = level
	s.levels = maps.Clone(levels)
}

// LevelFor returns the minimum level of the component; "" uses the base
// level.
func (s *LevelStore) LevelFor(component string) slog.Level {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if l, ok := s.levels[component]; ok {
		return l
	}
	return s.level
}

// Min returns the lowest level of the store, the base level or any
// component level.
func (s *LevelStore) Min() slog.Level {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lowest := s.level
	for _, l := range s.levels {
		lowest = min(lowest, l)
	}
	return lowest
}

// levelHandler filters records by the level of their component. The
// component is taken from the ComponentKey attribute passed to WithAttrs,
// that is from Logger.With; attributes inside a group do not count. The
// level is read from the shared store per record, so level changes apply
// to every derived logger (roadmap 6.6).
type levelHandler struct {
	next      slog.Handler
	store     *LevelStore
	component string
	grouped   bool
}

func newLevelHandler(next slog.Handler, store *LevelStore) *levelHandler {
	return &levelHandler{next: next, store: store}
}

// Enabled implements slog.Handler.
func (h *levelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.store.LevelFor(h.component) && h.next.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (h *levelHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.next.Handle(ctx, r)
}

// WithAttrs implements slog.Handler.
func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	component := h.component
	if !h.grouped {
		for _, a := range attrs {
			if a.Key == ComponentKey {
				component = a.Value.String()
			}
		}
	}
	return &levelHandler{next: h.next.WithAttrs(attrs), store: h.store, component: component, grouped: h.grouped}
}

// WithGroup implements slog.Handler.
func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{next: h.next.WithGroup(name), store: h.store, component: h.component, grouped: true}
}
