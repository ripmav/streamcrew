// SPDX-License-Identifier: MIT

// Package config defines the startup configuration of streamcrew and how it is
// read: command-line flags, STREAMCREW_* environment variables and an optional
// YAML configuration file, in this order of precedence (Code-ADR-0005).
//
// Only cmd/streamcrew and the composition root import this package. Components
// receive their own small configuration structs (Code-ADR-0002).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"time"
)

// Mode is the operating mode of the core (ADR-0003).
type Mode string

// Operating modes. The defaults suit the streaming PC; the server mode has to
// be chosen explicitly.
const (
	// ModeDesktop is a core started by the desktop app.
	ModeDesktop Mode = "desktop"
	// ModeDaemon is a local core on the streaming PC, operated via CLI or TUI.
	ModeDaemon Mode = "daemon"
	// ModeServer is a core on a home server, VPS or in a container.
	ModeServer Mode = "server"
)

// DefaultPort is the TCP port of the HTTP server unless --listen says otherwise.
const DefaultPort = 8740

// Config is the startup configuration. Its kong tags define the global flags
// of the streamcrew command; environment variables and the configuration file
// use the same names (--log-level, STREAMCREW_LOG_LEVEL, log_level).
type Config struct {
	ConfigFile      string        `name:"config" type:"path" placeholder:"FILE" help:"Configuration file (YAML). Without this flag, config.yaml in the default data directory is read if it exists."`
	DataDir         string        `name:"data-dir" type:"path" default:"${default_data_dir}" placeholder:"DIR" help:"Data directory for profiles, logs and runtime files. Default: ${default}."`
	Profile         string        `placeholder:"ID" help:"Profile to use. Default: the active profile (streamcrew profile use)."`
	Mode            Mode          `enum:"desktop,daemon,server" default:"daemon" help:"Operating mode: ${enum}."`
	Listen          string        `placeholder:"HOST:PORT" help:"Address of the HTTP server. Default: 127.0.0.1:8740, in server mode :8740."`
	Dev             bool          `help:"Developer mode: serves pprof under /debug/pprof/. Requires a loopback listen address."`
	ShutdownTimeout time.Duration `default:"15s" help:"Time limit for the whole shutdown."`
	Log             LogConfig     `embed:"" prefix:"log-"`
}

// LogConfig configures logging (Code-ADR-0003).
type LogConfig struct {
	Level          string            `default:"info" enum:"debug,info,warn,error" help:"Log level: ${enum}."`
	ComponentLevel map[string]string `name:"component-level" placeholder:"COMPONENT=LEVEL" help:"Log level of a single component, e.g. supervisor=debug. Repeatable."`
	Format         string            `default:"text" enum:"text,json" help:"Format of the console log: ${enum}."`
	File           bool              `default:"true" negatable:"" help:"Write a JSON log file with rotation to <data-dir>/logs."`
	MaxSize        int               `default:"10" placeholder:"MIB" help:"Rotate the log file when it would exceed this size in MiB. Default: ${default}."`
	MaxFiles       int               `default:"5" placeholder:"N" help:"Number of rotated log files to keep. Default: ${default}."`
}

// DefaultListen returns the listen address used when --listen is not set:
// loopback only, except in server mode.
func DefaultListen(m Mode) string {
	host := "127.0.0.1"
	if m == ModeServer {
		host = ""
	}
	return net.JoinHostPort(host, strconv.Itoa(DefaultPort))
}

// LogDir returns the directory of the log files.
func (c *Config) LogDir() string {
	return filepath.Join(c.DataDir, "logs")
}

// Resolve fills in the defaults that depend on other settings and checks the
// configuration. The returned error lists every problem found.
func (c *Config) Resolve() error {
	if c.Listen == "" {
		c.Listen = DefaultListen(c.Mode)
	}

	var errs []error
	switch c.Mode {
	case ModeDesktop, ModeDaemon, ModeServer:
	default:
		errs = append(errs, fmt.Errorf("--mode: unknown mode %q", c.Mode))
	}
	if c.DataDir == "" {
		errs = append(errs, errors.New("--data-dir: no default data directory available, set --data-dir or STREAMCREW_DATA_DIR"))
	}
	host, err := splitListen(c.Listen)
	if err != nil {
		errs = append(errs, fmt.Errorf("--listen: %w", err))
	} else if c.Dev && !isLoopback(host) {
		errs = append(errs, fmt.Errorf("--dev: pprof is only served on a loopback address, not on %q", c.Listen))
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, fmt.Errorf("--shutdown-timeout: must be positive, got %s", c.ShutdownTimeout))
	}
	errs = append(errs, c.Log.check()...)
	return errors.Join(errs...)
}

func (l *LogConfig) check() []error {
	var errs []error
	if _, err := ParseLevel(l.Level); err != nil {
		errs = append(errs, fmt.Errorf("--log-level: %w", err))
	}
	for component, level := range l.ComponentLevel {
		if component == "" {
			errs = append(errs, errors.New("--log-component-level: empty component name"))
		}
		if _, err := ParseLevel(level); err != nil {
			errs = append(errs, fmt.Errorf("--log-component-level %s: %w", component, err))
		}
	}
	switch l.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("--log-format: unknown format %q", l.Format))
	}
	if l.MaxSize < 1 {
		errs = append(errs, fmt.Errorf("--log-max-size: must be at least 1 MiB, got %d", l.MaxSize))
	}
	if l.MaxFiles < 0 {
		errs = append(errs, fmt.Errorf("--log-max-files: must not be negative, got %d", l.MaxFiles))
	}
	return errs
}

// ParseLevel converts a level name (debug, info, warn, error) to a slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown level %q, want debug, info, warn or error", s)
	}
}

func splitListen(addr string) (host string, err error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	// Port 0 lets the system choose a free port.
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", fmt.Errorf("invalid port %q in %q", port, addr)
	}
	return host, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
