// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/commandfile"
)

type commandCmd struct {
	Validate commandValidateCmd `cmd:"" help:"Check files of commands as code against the profile without changing it. Works while the core runs."`
}

type commandValidateCmd struct {
	output
	Paths []string `arg:"" type:"path" help:"Files (.yaml, .yml, .json) or directories with them, also in subdirectories."`
}

// Run checks files of commands as code (commands-as-code.md, B31, B32) and
// fails if they have errors; warnings do not change the outcome.
func (c commandValidateCmd) Run(ctx context.Context, e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	rights, err := cfg.Rights()
	if err != nil {
		return &usageError{err: err}
	}
	files, err := commandfile.Files(c.Paths)
	if err != nil {
		return &usageError{err: err}
	}
	if len(files) == 0 {
		return &usageError{err: fmt.Errorf("no files of commands as code in %s", strings.Join(c.Paths, ", "))}
	}
	id, err := e.profileID(cfg)
	if err != nil {
		return err
	}
	path := e.profiles(cfg).Path(id)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("profile %q does not exist; create it with \"streamcrew profile create\" or start the core once", id)
	}
	report, err := app.CheckCommandFiles(ctx, path, rights, files)
	if err != nil {
		return err
	}
	if c.Output == "json" {
		err = writeJSON(e.Stdout, report)
	} else {
		err = writeReport(e, report)
	}
	if err != nil {
		return err
	}
	if report.Errors > 0 {
		return &reportedError{err: errors.New("the files have errors")}
	}
	return nil
}

// writeReport writes the problems, one per line, and a summary.
func writeReport(e *Env, r app.CommandFileReport) error {
	for _, p := range r.Problems {
		if _, err := fmt.Fprintln(e.Stdout, p.String()); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(e.Stdout, "%d %s in %d %s: %d %s, %d %s\n",
		r.Documents, plural(r.Documents, "document", "documents"), r.Files, plural(r.Files, "file", "files"),
		r.Errors, plural(r.Errors, "error", "errors"), r.Warnings, plural(r.Warnings, "warning", "warnings"))
	return err
}

// plural returns one or many by n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
