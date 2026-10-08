// SPDX-License-Identifier: MIT

// Command streamcrew runs the headless core of streamcrew, a bot and
// automation service for live streams. Frontends such as the CLI/TUI, the
// desktop app and the web interface talk to it exclusively through its API.
//
// This package sets up the process and kong: signals, environment, the
// configuration sources (Code-ADR-0005), parsing and the exit code. The
// commands themselves are defined in internal/cli. "streamcrew --help" lists
// them.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	_ "time/tzdata" // time zones must work in minimal containers without zoneinfo (plan §6.22)

	"github.com/alecthomas/kong"

	"github.com/ripmav/streamcrew/internal/cli"
	"github.com/ripmav/streamcrew/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// After the first signal, restore the default behavior, so that a second
	// Ctrl+C ends the process at once (Code-ADR-0004).
	context.AfterFunc(ctx, stop)

	// Secrets never come as flags (Code-ADR-0005); only this package and
	// internal/config read the environment.
	secretKey, _ := os.LookupEnv("STREAMCREW_SECRET_KEY")
	code := run(ctx, os.Args[1:], process{
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		exit:       os.Exit,
		executable: executable(),
		secretKey:  secretKey,
	})
	stop()
	os.Exit(code)
}

// process is what run takes from the process; tests replace it.
type process struct {
	stdout, stderr io.Writer
	exit           func(int) // called by kong after --help
	executable     string    // for the portable mode; empty skips it
	secretKey      string    // STREAMCREW_SECRET_KEY (ADR-0012)
}

// run sets up kong with the command line of internal/cli, parses args, runs
// the selected command and returns the exit code.
func run(ctx context.Context, args []string, p process) int {
	defaults, defaultsErr := config.DetectDefaults(p.executable)
	file := config.NewFileResolver(defaults.ConfigFile())

	var root cli.Root
	opts := append([]kong.Option{
		kong.Name("streamcrew"),
		kong.Description("Headless core of streamcrew, a bot and automation service for live streams."),
		kong.Writers(p.stdout, p.stderr),
		kong.Exit(p.exit),
		kong.UsageOnError(),
	}, config.KongOptions(defaults, file)...)
	parser, err := kong.New(&root, opts...)
	if err != nil {
		fmt.Fprintf(p.stderr, "streamcrew: %v\n", err)
		return cli.ExitFailure
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(p.stderr, "streamcrew: %v\n", err)
		return cli.ExitUsage
	}
	if err := file.Err(); err != nil {
		fmt.Fprintf(p.stderr, "streamcrew: %v\n", err)
		return cli.ExitUsage
	}

	env := &cli.Env{
		Stdout:      p.stdout,
		Stderr:      p.stderr,
		Config:      &root.Config,
		Defaults:    defaults,
		DefaultsErr: defaultsErr,
		File:        file,
		SecretKey:   p.secretKey,
	}
	kctx.BindTo(ctx, (*context.Context)(nil))
	return cli.ExitCode(kctx.Run(env), p.stderr)
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
