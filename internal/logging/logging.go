// SPDX-License-Identifier: MIT

// Package logging builds the slog logger of streamcrew (Code-ADR-0003):
// console output as text or JSON, a JSON log file with size-based rotation,
// log levels per component and masking of secrets.
//
// Components get the logger injected with their name in the "component"
// attribute:
//
//	logger.With(logging.ComponentKey, "supervisor")
//
// Dynamic builds a logger whose levels and output can change while it
// runs (roadmap 6.5, on-the-fly configuration):
//
//	logger, dyn, _ := logging.NewDynamic(cfg)
//	dyn.Update(newCfg)
package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// ComponentKey is the attribute that names the component of a logger. Set it
// with Logger.With; per-component levels only see it there.
const ComponentKey = "component"

// Format is the output format of the console.
type Format string

// Console formats.
const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Config configures New and Dynamic.Update.
type Config struct {
	// Level is the minimum level of all components without an entry in
	// ComponentLevels.
	Level slog.Level
	// ComponentLevels overrides Level per component.
	ComponentLevels map[string]slog.Level
	// Console receives the console output; nil disables it.
	Console io.Writer
	// ConsoleFormat is the format of the console output.
	ConsoleFormat Format
	// File is the path of the JSON log file; empty disables it.
	File string
	// MaxSize is the size in bytes at which the log file is rotated.
	MaxSize int64
	// MaxFiles is the number of rotated log files to keep.
	MaxFiles int
}

// New builds the static logger described by cfg: its levels and output do
// not change while it runs. The returned closer closes the log file; call
// it after the last log call.
func New(cfg Config) (*slog.Logger, io.Closer, error) {
	base, closer, err := buildBase(cfg)
	if err != nil {
		return nil, nil, err
	}
	h := newLevelHandler(base, NewLevelStore(cfg.Level, cfg.ComponentLevels))
	return slog.New(h), closer, nil
}

// Dynamic is the mutable part of a logger built by NewDynamic: the levels
// and the output (console format, log file, rotation). Update applies a new
// configuration to the whole logger tree; the old log file is closed.
type Dynamic struct {
	console io.Writer

	mu       sync.Mutex
	base     *slog.Handler // the current output; swapped by Update
	sink     *slog.Handler // an extra handler beside the output; set by SetSink
	store    *LevelStore
	maxSize  int64
	maxFiles int
	closer   io.Closer
}

// NewDynamic builds the logger described by cfg with a Dynamic handle for
// changing levels and output at runtime (roadmap 6.5). The returned logger
// and every logger derived from it see the changes.
func NewDynamic(cfg Config) (*slog.Logger, *Dynamic, error) {
	base, closer, err := buildBase(cfg)
	if err != nil {
		return nil, nil, err
	}
	d := &Dynamic{
		console:  cfg.Console,
		base:     &base,
		store:    NewLevelStore(cfg.Level, cfg.ComponentLevels),
		maxSize:  cfg.MaxSize,
		maxFiles: cfg.MaxFiles,
		closer:   closer,
	}
	h := newLevelHandler(&baseSlot{d: d}, d.store)
	return slog.New(h), d, nil
}

// Update changes the levels and the output of the logger tree. The console
// writer and the file parameters come from cfg; the previous log file is
// closed after the new output is in place.
func (d *Dynamic) Update(cfg Config) error {
	base, closer, err := buildBase(Config{
		Console:       d.console,
		ConsoleFormat: cfg.ConsoleFormat,
		File:          cfg.File,
		MaxSize:       cfg.MaxSize,
		MaxFiles:      cfg.MaxFiles,
	})
	if err != nil {
		return err
	}
	d.mu.Lock()
	old := d.closer
	*d.base = base
	d.closer = closer
	d.maxSize = cfg.MaxSize
	d.maxFiles = cfg.MaxFiles
	d.mu.Unlock()
	d.store.Set(cfg.Level, cfg.ComponentLevels)
	if err := old.Close(); err != nil {
		return fmt.Errorf("close old log file: %w", err)
	}
	return nil
}

// SetSink adds a handler that receives every record the level filter
// passes, beside the console and file output (roadmap 6.5: the hot log
// settings apply to the sink as well). The sink survives Update; it must
// not block and must not log through the same tree (its records would loop
// through it).
func (d *Dynamic) SetSink(h slog.Handler) {
	d.mu.Lock()
	d.sink = &h
	d.mu.Unlock()
}

// sinkHandler returns the current sink handler, or nil without one.
func (d *Dynamic) sinkHandler() slog.Handler {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sink == nil {
		return nil
	}
	return *d.sink
}

// Close closes the current log file. It is the closer of the log: call it
// after the last log call.
func (d *Dynamic) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	err := d.closer.Close()
	d.closer = nopCloser{}
	return err
}

// buildBase builds the output handlers (console and file) below the level
// filter.
func buildBase(cfg Config) (slog.Handler, io.Closer, error) {
	// The pre-filter is the lowest level: the level handler above does the
	// real filtering and may be lowered at runtime.
	opts := &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: redact}

	var handlers []slog.Handler
	if cfg.Console != nil {
		switch cfg.ConsoleFormat {
		case FormatText, "":
			handlers = append(handlers, slog.NewTextHandler(cfg.Console, opts))
		case FormatJSON:
			handlers = append(handlers, slog.NewJSONHandler(cfg.Console, opts))
		default:
			return nil, nil, fmt.Errorf("unknown console format %q", cfg.ConsoleFormat)
		}
	}
	var closer io.Closer = nopCloser{}
	if cfg.File != "" {
		file, err := OpenRotatingFile(cfg.File, cfg.MaxSize, cfg.MaxFiles)
		if err != nil {
			return nil, nil, err
		}
		handlers = append(handlers, slog.NewJSONHandler(file, opts))
		closer = file
	}
	if len(handlers) == 0 {
		return slog.DiscardHandler, closer, nil
	}
	if len(handlers) == 1 {
		return handlers[0], closer, nil
	}
	return slog.NewMultiHandler(handlers...), closer, nil
}

// baseSlot is the handler every logger of a Dynamic tree delegates to; it
// resolves the current output per call, so Update re-points the whole tree.
type baseSlot struct {
	d *Dynamic
}

func (s *baseSlot) Enabled(ctx context.Context, level slog.Level) bool {
	return s.out().Enabled(ctx, level)
}

func (s *baseSlot) Handle(ctx context.Context, r slog.Record) error {
	return s.d.deliver(ctx, s.out(), nil, nil, r)
}

func (s *baseSlot) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &derivedSlot{d: s.d, attrs: attrs}
}

func (s *baseSlot) WithGroup(name string) slog.Handler {
	return &derivedSlot{d: s.d, groups: []string{name}}
}

func (s *baseSlot) out() slog.Handler {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	return *s.d.base
}

// deliver writes r to out and to the sink, if any (roadmap 6.6). The sink
// gets the same derivation as out, so it sees the component attribute like
// the output does; every record of the tree reaches the sink.
func (d *Dynamic) deliver(ctx context.Context, out slog.Handler, attrs []slog.Attr, groups []string, r slog.Record) error {
	err := out.Handle(ctx, r)
	if h := d.sinkHandler(); h != nil {
		for _, g := range groups {
			h = h.WithGroup(g)
		}
		if len(attrs) > 0 {
			h = h.WithAttrs(attrs)
		}
		// The sink's error is dropped: the output already handled the
		// record, and reporting the sink would loop through it.
		_ = h.Handle(ctx, r)
	}
	return err
}

// baseSlotOut returns the current output of the Dynamic logger.
func (d *Dynamic) baseSlotOut() slog.Handler {
	d.mu.Lock()
	defer d.mu.Unlock()
	return *d.base
}

// derivedSlot is a WithAttrs/WithGroup derivation of baseSlot that keeps
// pointing at the current output of the Dynamic logger.
type derivedSlot struct {
	d      *Dynamic
	attrs  []slog.Attr
	groups []string
}

func (s *derivedSlot) out() slog.Handler {
	h := s.d.baseSlotOut()
	for _, g := range s.groups {
		h = h.WithGroup(g)
	}
	if len(s.attrs) > 0 {
		h = h.WithAttrs(s.attrs)
	}
	return h
}

func (s *derivedSlot) Enabled(ctx context.Context, level slog.Level) bool {
	return s.out().Enabled(ctx, level)
}

func (s *derivedSlot) Handle(ctx context.Context, r slog.Record) error {
	return s.d.deliver(ctx, s.out(), s.attrs, s.groups, r)
}

func (s *derivedSlot) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &derivedSlot{d: s.d, groups: s.groups, attrs: append(append([]slog.Attr{}, s.attrs...), attrs...)}
}

func (s *derivedSlot) WithGroup(name string) slog.Handler {
	return &derivedSlot{d: s.d, attrs: s.attrs, groups: append(append([]string{}, s.groups...), name)}
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// errClosed is returned when writing to a closed RotatingFile.
var errClosed = errors.New("log file closed")
