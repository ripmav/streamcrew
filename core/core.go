// SPDX-License-Identifier: MIT

// Package core is the public start API of the streamcrew core (ADR-0006).
// It contains only the narrow Run function and the Options type; domain
// types, services and internals stay internal, and every other interaction
// runs through the API. The desktop app uses it for the self-start
// (ADR-0006); `streamcrew serve` uses the same function.
package core

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
)

// Options configures the core. Empty fields fall back to the defaults of
// the streamcrew configuration (Code-ADR-0005): mode "daemon", the loopback
// listen address, the data directory in the user's config directory, info
// logs to stderr and the log file.
type Options struct {
	// DataDir is the data directory for profiles, logs and runtime files.
	DataDir string
	// Profile is the profile to use; empty is the active profile.
	Profile string
	// Mode is the operating mode: "desktop", "daemon" or "server".
	Mode string
	// Listen is the address of the HTTP server; empty selects the default
	// of the mode (loopback, except server mode).
	Listen string
	// OverlayListen is the address of the overlay server (roadmap 7.1);
	// empty selects the default of the mode (loopback, except server
	// mode). Changes need a restart.
	OverlayListen string
	// OverlayToken protects the overlay URLs: when set, the page, the
	// WebSocket and the media URLs require the value as the query
	// parameter token (roadmap 7.1). Empty leaves the endpoints open.
	OverlayToken string
	// Dev serves pprof under /debug/pprof/ and requires a loopback address.
	Dev bool
	// DryRun makes the platforms log what they would do instead of doing it.
	DryRun bool
	// CorsAllow lists the origins allowed to call the API cross-origin
	// (CORS); empty refuses every cross-origin request.
	CorsAllow []string
	// TLSCert and TLSKey are the PEM files of the server certificate and
	// key; both or neither switches the HTTP server to TLS.
	TLSCert string
	TLSKey  string
	// Grant and Revoke turn capabilities of the mode on and off
	// (Code-ADR-0019).
	Grant  []string
	Revoke []string
	// FileRoot names directories outside the data directory that the file
	// action may use; OutboundAllow lists the targets that web requests may
	// reach in server mode.
	FileRoot      map[string]string
	OutboundAllow []string
	// ObsHost, ObsPort and ObsPassword are the OBS obs-websocket of the
	// OBS connection (roadmap 7.4); empty selects the defaults of the
	// configuration.
	ObsHost     string
	ObsPort     int
	ObsPassword string
	// LogLevel and LogFormat configure the console log (empty is "info" and
	// "text"); LogFile is the JSON log file: "true" (the streamcrew binary
	// default) writes the file in the data directory, "false" disables it,
	// and a path writes that file; LogMaxSize and LogMaxFiles bound
	// its rotation (empty is 10 MiB and 5 files); LogComponentLevel
	// overrides the level of one component.
	LogLevel          string
	LogFormat         string
	LogFile           string
	LogMaxSize        int
	LogMaxFiles       int
	LogComponentLevel map[string]string
	// ShutdownTimeout limits the whole shutdown.
	ShutdownTimeout time.Duration
	// StreamBuffer is how many events the stream hub may owe to the bus
	// before the bus drops them (0 is 1024); StreamCatchUp is how many of
	// the last events are kept for the since catch-up (0 is 256). Both
	// apply only at the start of the core (roadmap 6.2.6).
	StreamBuffer  int
	StreamCatchUp int
	// TwitchEventSub and TwitchIRC switch the chat adapters of the
	// Twitch platform ("on" or "off", empty is "on" the default); at
	// least one must be on.
	TwitchEventSub string
	TwitchIRC      string
	// BootstrapToken is the value of --bootstrap-token (STREAMCREW_TOKEN);
	// in the server mode without a stored token, it creates the bootstrap
	// admin token at the first start.
	BootstrapToken string
	// SecretKey is the value of STREAMCREW_SECRET_KEY (ADR-0012); it wins
	// over the environment variable.
	SecretKey string
	// LogWriter receives the console log; nil uses os.Stderr.
	LogWriter io.Writer
	// ConfigFile is the path of a YAML configuration file to apply and
	// watch; empty skips it.
	ConfigFile string
}

// Run starts the core and blocks until ctx ends. It applies the defaults
// of the streamcrew configuration (Code-ADR-0005) to the empty options,
// routes the default log logger into the core's log handlers
// (Code-ADR-0003) and returns the error of the shutdown, or nil if the
// core stopped cleanly.
func Run(ctx context.Context, opts Options) error {
	if opts.Mode == "" {
		opts.Mode = "daemon"
	}
	if opts.LogLevel == "" {
		opts.LogLevel = "info"
	}
	if opts.LogFormat == "" {
		opts.LogFormat = "text"
	}
	if opts.LogMaxSize <= 0 {
		opts.LogMaxSize = 10
	}
	if opts.LogMaxFiles <= 0 {
		opts.LogMaxFiles = 5
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 15 * time.Second
	}
	if opts.StreamBuffer <= 0 {
		opts.StreamBuffer = 1024
	}
	if opts.StreamCatchUp <= 0 {
		opts.StreamCatchUp = 256
	}
	if opts.ObsHost == "" {
		opts.ObsHost = "127.0.0.1"
	}
	if opts.ObsPort <= 0 {
		opts.ObsPort = 4455
	}
	cfg := config.Config{
		DataDir:         opts.DataDir,
		Profile:         opts.Profile,
		Mode:            config.Mode(opts.Mode),
		Listen:          opts.Listen,
		OverlayListen:   opts.OverlayListen,
		OverlayToken:    opts.OverlayToken,
		Dev:             opts.Dev,
		DryRun:          opts.DryRun,
		CorsAllow:       opts.CorsAllow,
		TLSCert:         opts.TLSCert,
		TLSKey:          opts.TLSKey,
		Grant:           opts.Grant,
		Revoke:          opts.Revoke,
		FileRoot:        opts.FileRoot,
		OutboundAllow:   opts.OutboundAllow,
		BootstrapToken:  opts.BootstrapToken,
		ObsHost:         opts.ObsHost,
		ObsPort:         opts.ObsPort,
		ObsPassword:     opts.ObsPassword,
		ShutdownTimeout: opts.ShutdownTimeout,
		Stream: config.StreamConfig{
			Buffer:  opts.StreamBuffer,
			CatchUp: opts.StreamCatchUp,
		},
		Twitch: config.TwitchConfig{
			EventSub: opts.TwitchEventSub,
			IRC:      opts.TwitchIRC,
		},
		Log: config.LogConfig{
			Level:          opts.LogLevel,
			ComponentLevel: opts.LogComponentLevel,
			Format:         opts.LogFormat,
			File:           opts.LogFile,
			MaxSize:        opts.LogMaxSize,
			MaxFiles:       opts.LogMaxFiles,
		},
	}
	if err := cfg.Resolve(); err != nil {
		return err
	}
	optsList := []app.Option{app.WithSecretKey(opts.SecretKey)}
	if opts.LogWriter != nil {
		optsList = append(optsList, app.WithConsole(opts.LogWriter))
	}
	if opts.ConfigFile != "" {
		// Load the resolver, so the watcher watches the file and
		// ConfigService/Update knows its allowed keys (code review
		// 2026-10-24, C14); only after the Update check that the API may
		// only narrow (Code-ADR-0021, Nachtrag 2026-10-24).
		file := config.NewFileResolver(opts.ConfigFile)
		if err := file.Load(opts.ConfigFile); err != nil {
			return err
		}
		optsList = append(optsList, app.WithConfigFile(file))
	}
	a, err := app.New(ctx, cfg, optsList...)
	if err != nil {
		return err
	}
	slog.SetDefault(a.Logger())
	return a.Run(ctx)
}
