// SPDX-License-Identifier: Apache-2.0

// Command streamcrew runs the headless core of streamcrew, a bot and
// automation service for live streams. Frontends such as the CLI/TUI, the
// desktop app and the web interface talk to it exclusively through its API.
//
// Subcommands:
//
//	serve         run the core until SIGINT or SIGTERM
//	version       print version information
//	config show   print the effective configuration
//	config path   print the configuration file and the directories
//	doctor        check the environment of the core
//
// The configuration comes from flags, STREAMCREW_* environment variables and
// an optional YAML file, in this order of precedence (Code-ADR-0005).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	_ "time/tzdata" // time zones must work in minimal containers without zoneinfo (plan §6.22)

	"github.com/alecthomas/kong"

	"github.com/ripmav/streamcrew/internal/config"
)

// Exit codes (Code-ADR-0003).
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// After the first signal, restore the default behavior, so that a second
	// Ctrl+C ends the process at once (Code-ADR-0004).
	context.AfterFunc(ctx, stop)

	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Exit)
	stop()
	os.Exit(code)
}

// run parses args, runs the selected command and returns the exit code.
// exit is called by kong after --help.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, exit func(int)) int {
	defaults, defaultsErr := config.DetectDefaults(executable())
	file := config.NewFileResolver(defaults.ConfigFile())

	var c cli
	opts := append([]kong.Option{
		kong.Name("streamcrew"),
		kong.Description("Headless core of streamcrew, a bot and automation service for live streams."),
		kong.Writers(stdout, stderr),
		kong.Exit(exit),
		kong.UsageOnError(),
	}, config.KongOptions(defaults, file)...)
	parser, err := kong.New(&c, opts...)
	if err != nil {
		fmt.Fprintf(stderr, "streamcrew: %v\n", err)
		return exitFailure
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "streamcrew: %v\n", err)
		return exitUsage
	}
	if err := file.Err(); err != nil {
		fmt.Fprintf(stderr, "streamcrew: %v\n", err)
		return exitUsage
	}

	env := &runEnv{
		stdout:      stdout,
		stderr:      stderr,
		cfg:         &c.Config,
		defaults:    defaults,
		defaultsErr: defaultsErr,
		file:        file,
	}
	kctx.BindTo(ctx, (*context.Context)(nil))
	return exitCode(kctx.Run(env), stderr)
}

// executable returns the path of the running binary with symlinks resolved,
// or "" if it cannot be determined. It is used to detect the portable mode.
func executable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// usageError marks an invalid command line or configuration (exit code 2).
type usageError struct {
	err error
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// reportedError marks a failure that has already been reported, e.g. logged
// by the core or printed as a check result. main only sets exit code 1.
type reportedError struct {
	err error
}

func (e *reportedError) Error() string { return e.err.Error() }
func (e *reportedError) Unwrap() error { return e.err }

// exitCode reports err on stderr unless it has been reported already and
// returns the matching exit code.
func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return exitOK
	}
	if _, ok := errors.AsType[*reportedError](err); ok {
		return exitFailure
	}
	fmt.Fprintf(stderr, "streamcrew: %v\n", err)
	if _, ok := errors.AsType[*usageError](err); ok {
		return exitUsage
	}
	return exitFailure
}
