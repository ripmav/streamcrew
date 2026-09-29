// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"text/tabwriter"

	"go.yaml.in/yaml/v3"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/doctor"
	"github.com/ripmav/streamcrew/internal/profile"
)

// cli is the command line: the global flags of config.Config and the
// subcommands.
type cli struct {
	config.Config

	Serve      serveCmd   `cmd:"" help:"Run the core until SIGINT or SIGTERM."`
	ProfileCmd profileCmd `cmd:"" name:"profile" help:"Manage profiles. Changes need a stopped core."`
	Backup     backupCmd  `cmd:"" help:"Create, list and restore profile backups."`
	Vault      vaultCmd   `cmd:"" name:"secret" help:"Manage the key that encrypts tokens at rest."`
	Version    versionCmd `cmd:"" help:"Print version information."`
	ConfigCmd  configCmd  `cmd:"" name:"config" help:"Show the configuration."`
	Doctor     doctorCmd  `cmd:"" help:"Check the environment of the core."`
}

// runEnv is passed to the Run methods of the commands, together with the
// context of the process.
type runEnv struct {
	stdout, stderr io.Writer
	cfg            *config.Config
	defaults       config.Defaults
	defaultsErr    error
	file           *config.FileResolver
	// envKey is the value of STREAMCREW_SECRET_KEY (ADR-0012).
	envKey string
}

// resolve completes and checks the configuration for commands that need it.
func (e *runEnv) resolve() (*config.Config, error) {
	err := e.cfg.Resolve()
	if err != nil && e.cfg.DataDir == "" && e.defaultsErr != nil {
		err = errors.Join(err, e.defaultsErr)
	}
	if e.cfg.Profile != "" && !profile.ValidID(e.cfg.Profile) {
		err = errors.Join(err, fmt.Errorf("--profile: %w: %q", profile.ErrInvalidID, e.cfg.Profile))
	}
	if err != nil {
		return nil, &usageError{err: err}
	}
	return e.cfg, nil
}

// output is the --output flag of commands with machine-readable output.
type output struct {
	Output string `short:"o" enum:"text,json" default:"text" env:"-" help:"Output format: ${enum}."`
}

type serveCmd struct{}

// Run starts the core and blocks until the context ends.
func (serveCmd) Run(ctx context.Context, e *runEnv) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	a, err := app.New(ctx, *cfg, app.WithConsole(e.stderr), app.WithSecretKey(e.envKey))
	if err != nil {
		return err
	}
	// Route output of dependencies that use the log package or slog's
	// default logger into the same handlers (Code-ADR-0003).
	slog.SetDefault(a.Logger())
	if err := a.Run(ctx); err != nil {
		return &reportedError{err: err}
	}
	return nil
}

type versionCmd struct {
	output
}

// Run prints the version.
func (c versionCmd) Run(e *runEnv) error {
	info := buildinfo.Read()
	if c.Output == "json" {
		return writeJSON(e.stdout, info)
	}
	_, err := fmt.Fprintf(e.stdout, "streamcrew %s\n", info)
	return err
}

type configCmd struct {
	Show configShowCmd `cmd:"" help:"Print the effective configuration in the format of the configuration file."`
	Path configPathCmd `cmd:"" help:"Print the configuration file and the directories."`
}

type configShowCmd struct {
	Output string `short:"o" enum:"yaml,json" default:"yaml" env:"-" help:"Output format: ${enum}."`
}

// Run prints the effective configuration.
func (c configShowCmd) Run(e *runEnv) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	view := cfg.View()
	if c.Output == "json" {
		return writeJSON(e.stdout, view)
	}
	out, err := yaml.Marshal(view)
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	_, err = e.stdout.Write(out)
	return err
}

type configPathCmd struct {
	output
}

// pathInfo is the output of "config path".
type pathInfo struct {
	ConfigFile       string `json:"config_file"`
	ConfigFileExists bool   `json:"config_file_exists"`
	DataDir          string `json:"data_dir"`
	LogDir           string `json:"log_dir"`
	Portable         bool   `json:"portable"`
}

// Run prints where streamcrew keeps its files.
func (c configPathCmd) Run(e *runEnv) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	info := pathInfo{
		ConfigFile: e.file.Path(),
		DataDir:    cfg.DataDir,
		LogDir:     cfg.LogDir(),
		Portable:   e.defaults.Portable && cfg.DataDir == e.defaults.DataDir,
	}
	if info.ConfigFile != "" {
		info.ConfigFileExists = true
	} else {
		info.ConfigFile = e.defaults.ConfigFile()
		if cfg.ConfigFile != "" {
			info.ConfigFile = cfg.ConfigFile
		}
		if info.ConfigFile != "" {
			_, statErr := os.Stat(info.ConfigFile)
			info.ConfigFileExists = !errors.Is(statErr, fs.ErrNotExist)
		}
	}
	if c.Output == "json" {
		return writeJSON(e.stdout, info)
	}

	configFile := info.ConfigFile
	switch {
	case configFile == "":
		configFile = "(none)"
	case !info.ConfigFileExists:
		configFile += " (not present)"
	}
	dataDir := info.DataDir
	if info.Portable {
		dataDir += " (portable)"
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "config file:\t%s\n", configFile)
	fmt.Fprintf(tw, "data directory:\t%s\n", dataDir)
	fmt.Fprintf(tw, "log directory:\t%s\n", info.LogDir)
	return tw.Flush()
}

type doctorCmd struct {
	output
}

// Run checks the environment and fails if a check fails.
func (c doctorCmd) Run(ctx context.Context, e *runEnv) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	results := doctor.Run(ctx, doctor.Config{DataDir: cfg.DataDir, ConfigFile: e.file.Path(), Listen: cfg.Listen})
	if c.Output == "json" {
		err = writeJSON(e.stdout, results)
	} else {
		tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
		for _, r := range results {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Status, r.Check, r.Detail)
		}
		err = tw.Flush()
	}
	if err != nil {
		return err
	}
	if doctor.Failed(results) {
		return &reportedError{err: errors.New("doctor found problems")}
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
