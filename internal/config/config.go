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
	"strings"
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
	Mode            Mode          `enum:"desktop,daemon,server" default:"daemon" help:"Operating mode: ${enum}. The mode decides the base of the host rights and the default listen address."`
	Listen          string        `placeholder:"HOST:PORT" help:"Address of the HTTP server. Default: 127.0.0.1:8740, in server mode :8740."`
	Dev             bool          `help:"Developer mode: serves pprof under /debug/pprof/. Requires a loopback listen address."`
	CorsAllow       []string      `name:"cors-allow" placeholder:"ORIGIN" help:"Origins allowed to call the API cross-origin (CORS), e.g. http://localhost:5173. Repeatable. Default: none — cross-origin requests are refused."`
	TLSCert         string        `name:"tls-cert" placeholder:"FILE" help:"TLS certificate of the HTTP server (PEM). Together with --tls-key. Default: none — plain HTTP."`
	TLSKey          string        `name:"tls-key" placeholder:"FILE" help:"TLS private key of the HTTP server (PEM). Together with --tls-cert. Default: none — plain HTTP."`
	DryRun          bool          `name:"dry-run" help:"Dry run: the platforms read the channel and log what they would do, instead of doing it (chat, moderation, twitch actions)."`
	ShutdownTimeout time.Duration `default:"15s" help:"Time limit for the whole shutdown."`
	Log             LogConfig     `embed:"" prefix:"log-"`
	// Stream configures the sizes of the stream path; both apply only at
	// the start of the core (roadmap 6.2.6).
	Stream StreamConfig `embed:"" prefix:"stream-"`
	// Twitch toggles the chat adapters of the Twitch platform; it
	// applies only at the start of the core (no hot apply).
	Twitch TwitchConfig `embed:"" prefix:"twitch-"`

	// Grant, Revoke, FileRoot and OutboundAllow are the rights of
	// Code-ADR-0019. Changes of them in the configuration file apply
	// without a restart (Watcher).
	Grant         []string          `placeholder:"CAPABILITY" help:"Turn on a capability in addition to those of the mode, e.g. host:fs. Repeatable."`
	Revoke        []string          `placeholder:"CAPABILITY" help:"Turn off a capability of the mode, e.g. net:outbound. Repeatable."`
	FileRoot      map[string]string `name:"file-root" placeholder:"NAME=DIR" help:"Directory the file action may use under NAME, an absolute path outside the data directory. Repeatable."`
	OutboundAllow []string          `name:"outbound-allow" placeholder:"TARGET" help:"Internal target that web requests may reach in server mode: an IP address, a network in CIDR notation or a host name. Repeatable."`
}

// LogConfig configures logging (Code-ADR-0003).
type LogConfig struct {
	Level          string            `default:"info" enum:"debug,info,warn,error" help:"Log level: ${enum}."`
	ComponentLevel map[string]string `name:"component-level" placeholder:"COMPONENT=LEVEL" help:"Log level of a single component, e.g. supervisor=debug. Repeatable."`
	Format         string            `default:"text" enum:"text,json" help:"Format of the console log: ${enum}."`
	File           string            `default:"true" placeholder:"PATH" help:"The JSON log file with rotation: true writes <data-dir>/logs/streamcrew.log, false disables it, a path writes that file."`
	MaxSize        int               `default:"10" placeholder:"MIB" help:"Rotate the log file when it would exceed this size in MiB. Default: ${default}."`
	MaxFiles       int               `default:"5" placeholder:"N" help:"Number of rotated log files to keep. Default: ${default}."`
}

// StreamConfig sets the sizes of the stream path (roadmap 6.2.6): the bus
// buffer the stream hub owes to the bus and the catch-up buffer for the
// since replays. Both apply only at the start of the core; a change in the
// configuration file needs a restart (no hot apply, no API or TUI path).
type StreamConfig struct {
	// Buffer is how many events the stream hub may owe to the bus before
	// the bus drops them for the hub (event.lag).
	Buffer int `default:"1024" placeholder:"N" help:"Events the stream hub may owe to the bus before the bus drops them. Default: ${default}."`
	// CatchUp is how many of the last events are kept for the since catch-up
	// of a reconnecting client.
	CatchUp int `default:"256" placeholder:"N" help:"Recent events kept for the since catch-up of reconnecting clients. Default: ${default}."`
}

// check validates the stream sizes; like the other startup settings they
// must be positive.
func (s *StreamConfig) check() []error {
	var errs []error
	if s.Buffer <= 0 {
		errs = append(errs, fmt.Errorf("--stream-buffer: must be positive, got %d", s.Buffer))
	}
	if s.CatchUp <= 0 {
		errs = append(errs, fmt.Errorf("--stream-catchup: must be positive, got %d", s.CatchUp))
	}
	return errs
}

// TwitchConfig toggles the chat adapters of the Twitch platform. Each
// switch is "on" or "off" ("on" the default); at least one adapter must
// be on, without one there is no channel to operate and the core does
// not start. The values apply only at the start of the core (no hot
// apply, no API or TUI path).
type TwitchConfig struct {
	// EventSub switches the EventSub adapter: the home channel session
	// and, without the IRC adapter, the sessions of the additional
	// channels.
	EventSub string `name:"eventsub" default:"on" enum:"on,off" placeholder:"on|off" help:"The EventSub adapter of the Twitch platform: the home channel and, without the IRC adapter, the additional channels. Default: ${default}."`
	// IRC switches the IRC adapter: "off" the default, "on" turns it on.
	// The additional channels run over the IRC session while it is on,
	// and the home channel while the EventSub adapter is off.
	IRC string `default:"off" enum:"on,off" placeholder:"on|off" help:"The IRC adapter of the Twitch platform: the additional channels and, without the EventSub adapter, the home channel. Default: ${default}."`
}

// check validates the adapter switches: each is on or off, and at least
// one adapter is on (without one the core operates no channel and does
// not start).
func (t *TwitchConfig) check() []error {
	var errs []error
	for name, v := range map[string]string{"--twitch-eventsub": t.EventSub, "--twitch-irc": t.IRC} {
		switch v {
		case "", "on", "off":
		default:
			errs = append(errs, fmt.Errorf("%s: unknown switch %q, want on or off", name, v))
		}
	}
	if t.EventSub == "off" && t.IRC == "off" {
		errs = append(errs, errors.New("--twitch-eventsub and --twitch-irc: at least one chat adapter must be on"))
	}
	return errs
}

// SocketPath is the Unix socket of the desktop core.
func SocketPath(dataDir string) string { return filepath.Join(dataDir, "core.sock") }

// RuntimePath is the runtime file of the core (address, PID, version).
func RuntimePath(dataDir string) string { return filepath.Join(dataDir, RuntimeFileName) }

// DesktopTokenPath is the desktop token file of the core (ADR-0005).
func DesktopTokenPath(dataDir string) string { return filepath.Join(dataDir, DesktopTokenFileName) }

// Names of the files the core writes into the data directory.
const (
	// RuntimeFileName is the file with the address, PID and version of the
	// running core.
	RuntimeFileName = "runtime.json"
	// DesktopTokenFileName is the file with the desktop API token
	// (ADR-0005).
	DesktopTokenFileName = "desktop-token"
)

// RuntimePath is the runtime file in the data directory.
func (c Config) RuntimePath() string { return RuntimePath(c.DataDir) }

// DesktopTokenPath is the desktop token file in the data directory.
func (c Config) DesktopTokenPath() string { return DesktopTokenPath(c.DataDir) }

// DefaultListen returns the listen address used when --listen is not set:
// the Unix socket in the data directory in desktop mode, loopback only in
// daemon mode and any interface in server mode.
func DefaultListen(m Mode, dataDir string) string {
	switch m {
	case ModeDesktop:
		return "unix:" + SocketPath(dataDir)
	case ModeServer:
		return net.JoinHostPort("", strconv.Itoa(DefaultPort))
	default:
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultPort))
	}
}

// LogDir returns the directory of the log files.
func (c *Config) LogDir() string {
	return filepath.Join(c.DataDir, "logs")
}

// Resolve fills in the defaults that depend on other settings and checks the
// configuration. The returned error lists every problem found.
func (c *Config) Resolve() error {
	if c.Listen == "" {
		c.Listen = DefaultListen(c.Mode, c.DataDir)
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
	if !strings.HasPrefix(c.Listen, "unix:") {
		// A Unix socket is local: pprof and the loopback rules apply
		// without further checks.
		host, err := splitListen(c.Listen)
		if err != nil {
			errs = append(errs, fmt.Errorf("--listen: %w", err))
		} else if c.Dev && !isLoopback(host) {
			errs = append(errs, fmt.Errorf("--dev: pprof is only served on a loopback address, not on %q", c.Listen))
		}
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		errs = append(errs, errors.New("--tls-cert and --tls-key must be set together"))
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, fmt.Errorf("--shutdown-timeout: must be positive, got %s", c.ShutdownTimeout))
	}
	errs = append(errs, c.Stream.check()...)
	errs = append(errs, c.Twitch.check()...)
	errs = append(errs, c.Log.check()...)
	if _, err := c.Rights(); err != nil {
		errs = append(errs, err)
	}
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
