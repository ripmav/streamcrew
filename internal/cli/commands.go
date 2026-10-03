// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
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

// Root is the kong definition of the command line: the global flags of
// config.Config and the subcommands.
type Root struct {
	config.Config

	Serve      serveCmd   `cmd:"" help:"Run the core until SIGINT or SIGTERM."`
	ProfileCmd profileCmd `cmd:"" name:"profile" help:"Manage profiles. Changes need a stopped core."`
	Backup     backupCmd  `cmd:"" help:"Create, list and restore profile backups."`
	Vault      vaultCmd   `cmd:"" name:"secret" help:"Manage the key that encrypts tokens at rest."`
	Version    versionCmd `cmd:"" help:"Print version information."`
	ConfigCmd  configCmd  `cmd:"" name:"config" help:"Show the configuration."`
	Doctor     doctorCmd  `cmd:"" help:"Check the environment of the core."`
	Schema     schemaCmd  `cmd:"" help:"Export the JSON Schema of commands as code."`
	Command    commandCmd `cmd:"" help:"Check files of commands as code."`
}

// Env is bound to the Run methods of the commands, together with the context
// of the process. cmd/streamcrew fills it after parsing.
type Env struct {
	Stdout, Stderr io.Writer
	// Config is the parsed configuration, the global flags of Root.
	Config *config.Config
	// Defaults and DefaultsErr come from config.DetectDefaults.
	Defaults    config.Defaults
	DefaultsErr error
	// File is the resolver of the configuration file.
	File *config.FileResolver
	// SecretKey is the value of STREAMCREW_SECRET_KEY (ADR-0012); empty if
	// unset.
	SecretKey string
}

// resolve completes and checks the configuration for commands that need it.
func (e *Env) resolve() (*config.Config, error) {
	err := e.Config.Resolve()
	if err != nil && e.Config.DataDir == "" && e.DefaultsErr != nil {
		err = errors.Join(err, e.DefaultsErr)
	}
	if e.Config.Profile != "" && !profile.ValidID(e.Config.Profile) {
		err = errors.Join(err, fmt.Errorf("--profile: %w: %q", profile.ErrInvalidID, e.Config.Profile))
	}
	if err != nil {
		return nil, &usageError{err: err}
	}
	return e.Config, nil
}

// output is the --output flag of commands with machine-readable output.
type output struct {
	Output string `short:"o" enum:"text,json" default:"text" env:"-" help:"Output format: ${enum}."`
}

type serveCmd struct{}

// Run starts the core and blocks until the context ends.
func (serveCmd) Run(ctx context.Context, e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	a, err := app.New(ctx, *cfg, app.WithConsole(e.Stderr), app.WithSecretKey(e.SecretKey), app.WithConfigFile(e.File))
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
func (c versionCmd) Run(e *Env) error {
	info := buildinfo.Read()
	if c.Output == "json" {
		return writeJSON(e.Stdout, info)
	}
	_, err := fmt.Fprintf(e.Stdout, "streamcrew %s\n", info)
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
func (c configShowCmd) Run(e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	view := cfg.View()
	if c.Output == "json" {
		return writeJSON(e.Stdout, view)
	}
	out, err := yaml.Marshal(view)
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	_, err = e.Stdout.Write(out)
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
func (c configPathCmd) Run(e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	info := pathInfo{
		ConfigFile: e.File.Path(),
		DataDir:    cfg.DataDir,
		LogDir:     cfg.LogDir(),
		Portable:   e.Defaults.Portable && cfg.DataDir == e.Defaults.DataDir,
	}
	if info.ConfigFile != "" {
		info.ConfigFileExists = true
	} else {
		info.ConfigFile = e.Defaults.ConfigFile()
		if cfg.ConfigFile != "" {
			info.ConfigFile = cfg.ConfigFile
		}
		if info.ConfigFile != "" {
			_, statErr := os.Stat(info.ConfigFile)
			info.ConfigFileExists = !errors.Is(statErr, fs.ErrNotExist)
		}
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, info)
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
	tw := tabwriter.NewWriter(e.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "config file:\t%s\n", configFile)
	fmt.Fprintf(tw, "data directory:\t%s\n", dataDir)
	fmt.Fprintf(tw, "log directory:\t%s\n", info.LogDir)
	return tw.Flush()
}

type doctorCmd struct {
	output
}

// Run checks the environment and fails if a check fails.
func (c doctorCmd) Run(ctx context.Context, e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	rights, err := cfg.Rights()
	if err != nil {
		return &usageError{err: err}
	}
	results := doctor.Run(ctx, doctor.Config{
		DataDir: cfg.DataDir, ConfigFile: e.File.Path(), Listen: cfg.Listen, Rights: doctorRights(rights),
	})
	if c.Output == "json" {
		err = writeJSON(e.Stdout, results)
	} else {
		tw := tabwriter.NewWriter(e.Stdout, 0, 0, 2, ' ', 0)
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

// doctorRights returns the rights in the form of the doctor report.
func doctorRights(r config.Rights) doctor.Rights {
	caps := []string{}
	for _, c := range r.Capabilities.List() {
		caps = append(caps, string(c))
	}
	return doctor.Rights{Capabilities: caps, Roots: r.Roots, Outbound: r.Outbound.Entries(), Warnings: r.Warnings()}
}

func writeJSON(w io.Writer, v any) error {
	out, err := json.Marshal(v, jsontext.Multiline(true), jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return err
	}
	_, err = w.Write(append(out, '\n'))
	return err
}
