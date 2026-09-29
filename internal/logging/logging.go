// SPDX-License-Identifier: Apache-2.0

// Package logging builds the slog logger of streamcrew (Code-ADR-0003):
// console output as text or JSON, a JSON log file with size-based rotation,
// log levels per component and masking of secrets.
//
// Components get the logger injected with their name in the "component"
// attribute:
//
//	logger.With(logging.ComponentKey, "supervisor")
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// Config configures New.
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

// New builds the logger described by cfg. The returned closer closes the log
// file; call it after the last log call.
func New(cfg Config) (*slog.Logger, io.Closer, error) {
	lowest := cfg.Level
	for _, level := range cfg.ComponentLevels {
		lowest = min(lowest, level)
	}
	opts := &slog.HandlerOptions{Level: lowest, ReplaceAttr: redact}

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

	h := newLevelHandler(slog.NewMultiHandler(handlers...), cfg.Level, cfg.ComponentLevels)
	return slog.New(h), closer, nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// errClosed is returned when writing to a closed RotatingFile.
var errClosed = errors.New("log file closed")
