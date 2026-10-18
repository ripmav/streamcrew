// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"log/slog"
	"strings"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/logging"
)

// logSinkHandler adapts the log records of the core to "log.line" bus events
// (roadmap 6.6). It is the sink of the dynamic logger: every record the
// level filter passes becomes an event beside the console and file output.
type logSinkHandler struct {
	a      *App
	attrs  []slog.Attr
	groups []string
}

// Enabled lets every record through; the level filter of the logger tree is
// upstream of the sink.
func (logSinkHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle publishes the record as a "log.line" event. Publishing never
// blocks and a failure is dropped: logging must not slow down or break the
// core, and logging the failure would loop through the sink again.
func (h logSinkHandler) Handle(ctx context.Context, r slog.Record) error {
	component := ""
	appendAttrs := func(attrs []slog.Attr) {
		for _, at := range attrs {
			if at.Key == logging.ComponentKey {
				component = at.Value.String()
			}
		}
	}
	appendAttrs(h.attrs)
	r.Attrs(func(at slog.Attr) bool {
		if at.Key == logging.ComponentKey {
			component = at.Value.String()
		}
		return true
	})
	_ = h.a.bus.Publish(ctx, event.New(event.Source{Kind: event.SourceSystem, Name: "log"}, eventtype.Log, LogPayload{
		Level:     strings.ToLower(r.Level.String()),
		Component: component,
		Message:   r.Message,
	}))
	return nil
}

// WithAttrs implements slog.Handler; the sink is derived with the same
// attributes as the output, so the record it handles carries the component.
func (h logSinkHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logSinkHandler{a: h.a, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...), groups: h.groups}
}

// WithGroup implements slog.Handler.
func (h logSinkHandler) WithGroup(group string) slog.Handler {
	return logSinkHandler{a: h.a, attrs: h.attrs, groups: append(append([]string{}, h.groups...), group)}
}
