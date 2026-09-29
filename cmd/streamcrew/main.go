// SPDX-License-Identifier: Apache-2.0

// Command streamcrew runs the headless core of streamcrew, a bot and
// automation service for live streams. Frontends such as the CLI/TUI, the
// desktop app and the web interface talk to it exclusively through its API.
//
// The command line itself lives in internal/cli; this package only sets up
// the process: signals, environment and exit code. "streamcrew --help"
// lists the subcommands.
package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	_ "time/tzdata" // time zones must work in minimal containers without zoneinfo (plan §6.22)

	"github.com/ripmav/streamcrew/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// After the first signal, restore the default behavior, so that a second
	// Ctrl+C ends the process at once (Code-ADR-0004).
	context.AfterFunc(ctx, stop)

	// Secrets never come as flags (Code-ADR-0005), and only this package and
	// internal/config read the environment.
	secretKey, _ := os.LookupEnv("STREAMCREW_SECRET_KEY")
	code := cli.Run(ctx, os.Args[1:], cli.Env{
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Exit:       os.Exit,
		Executable: executable(),
		SecretKey:  secretKey,
	})
	stop()
	os.Exit(code)
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
