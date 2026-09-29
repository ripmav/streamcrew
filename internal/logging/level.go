// SPDX-License-Identifier: MIT

package logging

import (
	"context"
	"log/slog"
)

// levelHandler filters records by the level of their component. The
// component is taken from the ComponentKey attribute passed to WithAttrs,
// that is from Logger.With; attributes inside a group do not count.
type levelHandler struct {
	next    slog.Handler
	level   slog.Level
	levels  map[string]slog.Level
	grouped bool
}

func newLevelHandler(next slog.Handler, level slog.Level, levels map[string]slog.Level) *levelHandler {
	return &levelHandler{next: next, level: level, levels: levels}
}

// Enabled implements slog.Handler.
func (h *levelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level && h.next.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (h *levelHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.next.Handle(ctx, r)
}

// WithAttrs implements slog.Handler.
func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	level := h.level
	if !h.grouped {
		for _, a := range attrs {
			if a.Key != ComponentKey {
				continue
			}
			if l, ok := h.levels[a.Value.String()]; ok {
				level = l
			}
		}
	}
	return &levelHandler{next: h.next.WithAttrs(attrs), level: level, levels: h.levels, grouped: h.grouped}
}

// WithGroup implements slog.Handler.
func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{next: h.next.WithGroup(name), level: h.level, levels: h.levels, grouped: true}
}
