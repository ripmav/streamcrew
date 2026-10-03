// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/store"
)

type commandCmd struct {
	Validate commandValidateCmd `cmd:"" help:"Check files of commands as code against the profile without changing it. Works while the core runs."`
	Import   commandImportCmd   `cmd:"" help:"Import files of commands as code into the profile: all documents or, with errors, none. Needs a stopped core."`
	Export   commandExportCmd   `cmd:"" help:"Export commands with their groups and cooldown groups as files of commands as code. Works while the core runs."`
}

type commandExportCmd struct {
	Names  []string `arg:"" optional:"" help:"Commands to export, regardless of case. Without names: all commands, groups and cooldown groups."`
	File   string   `type:"path" xor:"target" env:"-" placeholder:"FILE" help:"Write into this file instead of the standard output."`
	Dir    string   `type:"path" xor:"target" env:"-" placeholder:"DIR" help:"Write one file per document into this directory, e.g. chat-command-hug.v1alpha1.yaml."`
	Format string   `enum:"yaml,json" default:"yaml" env:"-" help:"Format: ${enum}."`
}

// Run exports commands as files of commands as code (commands-as-code.md,
// B35 to B38): YAML or JSON on the standard output, into a file, or one
// file per document into a directory.
func (c commandExportCmd) Run(ctx context.Context, e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	path, err := e.profilePath(cfg)
	if err != nil {
		return err
	}
	docs, err := app.ExportCommands(ctx, path, c.Names)
	if err != nil {
		return err
	}
	encode := func(docs []commandfile.Exported, single bool) ([]byte, error) {
		if c.Format == "json" {
			return commandfile.EncodeJSON(docs, single)
		}
		return commandfile.EncodeYAML(docs)
	}
	switch {
	case c.Dir != "":
		if err := os.MkdirAll(c.Dir, 0o750); err != nil {
			return err
		}
		names := commandfile.FileNames(docs, "."+c.Format)
		for i, d := range docs {
			out, err := encode([]commandfile.Exported{d}, true)
			if err != nil {
				return err
			}
			if err := writeFile(e, filepath.Join(c.Dir, names[i]), out); err != nil {
				return err
			}
		}
		return nil
	case c.File != "":
		out, err := encode(docs, false)
		if err != nil {
			return err
		}
		return writeFile(e, c.File, out)
	default:
		out, err := encode(docs, false)
		if err != nil {
			return err
		}
		_, err = e.Stdout.Write(out)
		return err
	}
}

// writeFile writes data to path and says so.
func writeFile(e *Env, path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	_, err := fmt.Fprintf(e.Stdout, "wrote %s\n", path)
	return err
}

type commandValidateCmd struct {
	output
	Paths []string `arg:"" type:"path" help:"Files (.yaml, .yml, .json) or directories with them, also in subdirectories."`
}

// Run checks files of commands as code (commands-as-code.md, B31, B32) and
// fails if they have errors; warnings do not change the outcome.
func (c commandValidateCmd) Run(ctx context.Context, e *Env) error {
	in, err := e.commandFiles(c.Paths)
	if err != nil {
		return err
	}
	report, err := app.CheckCommandFiles(ctx, in.profile, in.rights, in.files)
	if err != nil {
		return err
	}
	if c.Output == "json" {
		err = writeJSON(e.Stdout, report)
	} else {
		err = writeReport(e, report, "")
	}
	if err != nil {
		return err
	}
	if report.Errors > 0 {
		return &reportedError{err: errors.New("the files have errors")}
	}
	return nil
}

type commandImportCmd struct {
	output
	Paths []string `arg:"" type:"path" help:"Files (.yaml, .yml, .json) or directories with them, also in subdirectories."`
}

// Run imports files of commands as code (commands-as-code.md, B33, B34)
// and fails if they have errors, then importing nothing (B62).
func (c commandImportCmd) Run(ctx context.Context, e *Env) (err error) {
	in, err := e.commandFiles(c.Paths)
	if err != nil {
		return err
	}
	l, err := e.lock(in.cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	opts := []store.Option{store.WithBeforeMigrate(
		app.PreMigrationBackup(app.BackupDir(in.cfg.DataDir), buildinfo.Read().Version, discardLogger()))}
	report, err := app.ImportCommandFiles(ctx, in.profile, opts, in.rights, in.files)
	if err != nil {
		return err
	}
	if c.Output == "json" {
		err = writeJSON(e.Stdout, report)
	} else {
		summary := "nothing imported"
		if report.Imported {
			summary = fmt.Sprintf("imported: %d new, %d replaced", report.Created, report.Replaced)
		}
		err = writeReport(e, report.CommandFileReport, summary)
	}
	if err != nil {
		return err
	}
	if !report.Imported {
		return &reportedError{err: errors.New("the files have errors; nothing imported")}
	}
	return nil
}

// commandFilesInput is what the commands on files of commands as code need.
type commandFilesInput struct {
	cfg     *config.Config
	rights  config.Rights
	files   []string
	profile string
}

// commandFiles resolves the configuration, the files of paths (B37) and
// the database of the profile, which must exist.
func (e *Env) commandFiles(paths []string) (commandFilesInput, error) {
	var in commandFilesInput
	cfg, err := e.resolve()
	if err != nil {
		return in, err
	}
	in.cfg = cfg
	if in.rights, err = cfg.Rights(); err != nil {
		return in, &usageError{err: err}
	}
	if in.files, err = commandfile.Files(paths); err != nil {
		return in, &usageError{err: err}
	}
	if len(in.files) == 0 {
		return in, &usageError{err: fmt.Errorf("no files of commands as code in %s", strings.Join(paths, ", "))}
	}
	in.profile, err = e.profilePath(cfg)
	return in, err
}

// profilePath returns the database of --profile or the active profile,
// which must exist.
func (e *Env) profilePath(cfg *config.Config) (string, error) {
	id, err := e.profileID(cfg)
	if err != nil {
		return "", err
	}
	path := e.profiles(cfg).Path(id)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("profile %q does not exist; create it with \"streamcrew profile create\" or start the core once", id)
	}
	return path, nil
}

// writeReport writes the problems, one per line, and a summary, ending in
// extra if it is not empty.
func writeReport(e *Env, r app.CommandFileReport, extra string) error {
	for _, p := range r.Problems {
		if _, err := fmt.Fprintln(e.Stdout, p.String()); err != nil {
			return err
		}
	}
	summary := fmt.Sprintf("%d %s in %d %s: %d %s, %d %s",
		r.Documents, plural(r.Documents, "document", "documents"), r.Files, plural(r.Files, "file", "files"),
		r.Errors, plural(r.Errors, "error", "errors"), r.Warnings, plural(r.Warnings, "warning", "warnings"))
	if extra != "" {
		summary += "; " + extra
	}
	_, err := fmt.Fprintln(e.Stdout, summary)
	return err
}

// plural returns one or many by n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
