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
	// LogLevel and LogFormat configure the console log (empty is "info" and
	// "text"); LogFile writes the JSON log file in the data directory (the
	// streamcrew binary defaults to on); LogMaxSize and LogMaxFiles bound
	// its rotation (empty is 10 MiB and 5 files); LogComponentLevel
	// overrides the level of one component.
	LogLevel          string
	LogFormat         string
	LogFile           bool
	LogMaxSize        int
	LogMaxFiles       int
	LogComponentLevel map[string]string
	// ShutdownTimeout limits the whole shutdown.
	ShutdownTimeout time.Duration
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
	cfg := config.Config{
		DataDir:         opts.DataDir,
		Profile:         opts.Profile,
		Mode:            config.Mode(opts.Mode),
		Listen:          opts.Listen,
		Dev:             opts.Dev,
		DryRun:          opts.DryRun,
		CorsAllow:       opts.CorsAllow,
		TLSCert:         opts.TLSCert,
		TLSKey:          opts.TLSKey,
		Grant:           opts.Grant,
		Revoke:          opts.Revoke,
		FileRoot:        opts.FileRoot,
		OutboundAllow:   opts.OutboundAllow,
		ShutdownTimeout: opts.ShutdownTimeout,
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
		optsList = append(optsList, app.WithConfigFile(config.NewFileResolver(opts.ConfigFile)))
	}
	a, err := app.New(ctx, cfg, optsList...)
	if err != nil {
		return err
	}
	slog.SetDefault(a.Logger())
	return a.Run(ctx)
}
