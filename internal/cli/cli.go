// SPDX-License-Identifier: MIT

// Package cli defines the command line of streamcrew: the kong structure Root
// with the global flags (config.Config) and the subcommands, their
// implementation and the mapping of errors to exit codes (Code-ADR-0003,
// Code-ADR-0005).
//
// cmd/streamcrew sets up kong with Root, parses, fills an Env and runs the
// selected command.
package cli

import (
	"errors"
	"fmt"
	"io"
)

// Exit codes (Code-ADR-0003).
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

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

// ExitCode reports the error of a command on stderr unless it has been
// reported already and returns the matching exit code.
func ExitCode(err error, stderr io.Writer) int {
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
