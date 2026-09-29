// SPDX-License-Identifier: Apache-2.0

// Package cli is the command line of streamcrew: the kong definition of the
// global flags (config.Config) and the subcommands, their implementation and
// the mapping of errors to exit codes (Code-ADR-0003, Code-ADR-0005).
//
// cmd/streamcrew only sets up the process (signals, environment, exit) and
// calls Run.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/alecthomas/kong"

	"github.com/ripmav/streamcrew/internal/config"
)

// Exit codes (Code-ADR-0003).
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// Env is what the command line takes from the process.
type Env struct {
	Stdout, Stderr io.Writer
	// Exit is called by kong after --help; os.Exit in production.
	Exit func(int)
	// Executable is the path of the running binary, for the portable mode
	// (Code-ADR-0005); empty skips that check.
	Executable string
	// SecretKey is the value of STREAMCREW_SECRET_KEY (ADR-0012); empty if
	// unset.
	SecretKey string
}

// Run parses args, runs the selected command and returns the exit code.
func Run(ctx context.Context, args []string, env Env) int {
	defaults, defaultsErr := config.DetectDefaults(env.Executable)
	file := config.NewFileResolver(defaults.ConfigFile())

	var r root
	opts := append([]kong.Option{
		kong.Name("streamcrew"),
		kong.Description("Headless core of streamcrew, a bot and automation service for live streams."),
		kong.Writers(env.Stdout, env.Stderr),
		kong.Exit(env.Exit),
		kong.UsageOnError(),
	}, config.KongOptions(defaults, file)...)
	parser, err := kong.New(&r, opts...)
	if err != nil {
		fmt.Fprintf(env.Stderr, "streamcrew: %v\n", err)
		return ExitFailure
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(env.Stderr, "streamcrew: %v\n", err)
		return ExitUsage
	}
	if err := file.Err(); err != nil {
		fmt.Fprintf(env.Stderr, "streamcrew: %v\n", err)
		return ExitUsage
	}

	re := &runEnv{
		stdout:      env.Stdout,
		stderr:      env.Stderr,
		cfg:         &r.Config,
		defaults:    defaults,
		defaultsErr: defaultsErr,
		file:        file,
		envKey:      env.SecretKey,
	}
	kctx.BindTo(ctx, (*context.Context)(nil))
	return exitCode(kctx.Run(re), env.Stderr)
}

// usageError marks an invalid command line or configuration (exit code 2).
type usageError struct {
	err error
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// reportedError marks a failure that has already been reported, e.g. logged
// by the core or printed as a check result. Only the exit code is set.
type reportedError struct {
	err error
}

func (e *reportedError) Error() string { return e.err.Error() }
func (e *reportedError) Unwrap() error { return e.err }

// exitCode reports err on stderr unless it has been reported already and
// returns the matching exit code.
func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return ExitOK
	}
	if _, ok := errors.AsType[*reportedError](err); ok {
		return ExitFailure
	}
	fmt.Fprintf(stderr, "streamcrew: %v\n", err)
	if _, ok := errors.AsType[*usageError](err); ok {
		return ExitUsage
	}
	return ExitFailure
}
