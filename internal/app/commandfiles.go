// SPDX-License-Identifier: Apache-2.0

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/store"
)

// CommandFileReport is the result of checking files of commands as code
// (commands-as-code.md, B31, B32).
type CommandFileReport struct {
	Files     int `json:"files"`
	Documents int `json:"documents"`
	Errors    int `json:"errors"`
	Warnings  int `json:"warnings"`
	// Problems are in the order of the files, then of their places.
	Problems []commandfile.Problem `json:"problems"`
}

// CheckCommandFiles checks files of commands as code against the profile
// at profilePath without changing it (commands-as-code.md, B31): it reads
// them, converts their documents, checks triggers and event types against
// the profile and saves them into a copy of the profile, with the checks of
// saving and the rights of the start configuration. The copy is made as
// for a backup, so the core may run meanwhile, and is deleted afterwards.
func CheckCommandFiles(ctx context.Context, profilePath string, rights config.Rights, files []string) (CommandFileReport, error) {
	report, docs, err := readCommandFiles(files)
	if err != nil {
		return report, err
	}
	err = withProfileCopy(ctx, profilePath, func(st *store.Store) error {
		_, problems, err := checkInto(ctx, st, rights, docs)
		report.Problems = append(report.Problems, problems...)
		return err
	})
	if err != nil {
		return report, err
	}
	report.finish(files)
	return report, nil
}

// ExportCommands returns the documents of the commands named, with the
// groups and cooldown groups they refer to, or of all objects without
// names (commands-as-code.md, B35, B36). It reads a copy of the profile at
// profilePath, made as for a backup, so the core may run meanwhile.
func ExportCommands(ctx context.Context, profilePath string, names []string) ([]commandfile.Exported, error) {
	actions, err := ActionCatalog(capability.Set{})
	if err != nil {
		return nil, err
	}
	codec, err := command.NewCodec(actions.Entries()...)
	if err != nil {
		return nil, err
	}
	var docs []commandfile.Exported
	err = withProfileCopy(ctx, profilePath, func(st *store.Store) error {
		var src commandfile.Source
		var err error
		if src.Commands, err = st.Commands(ctx); err != nil {
			return err
		}
		if src.Groups, err = st.Groups(ctx); err != nil {
			return err
		}
		if src.CooldownGroups, err = st.CooldownGroups(ctx); err != nil {
			return err
		}
		docs, err = commandfile.Export(src, names, commandfile.Types{Actions: actions.Descriptors(), Codec: codec})
		return err
	})
	return docs, err
}

// withProfileCopy runs fn with a copy of the profile at profilePath, made
// as for a backup and migrated to the current schema; the copy is deleted
// afterwards.
func withProfileCopy(ctx context.Context, profilePath string, fn func(st *store.Store) error) error {
	dir, err := os.MkdirTemp("", "streamcrew-profile-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	copyPath := filepath.Join(dir, "profile.db")
	if err := snapshotProfile(ctx, profilePath, copyPath); err != nil {
		return err
	}
	st, err := store.Open(ctx, copyPath, store.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		return fmt.Errorf("open the copy of the profile: %w", err)
	}
	return errors.Join(fn(st), st.Close())
}

// CommandImportReport is the result of an import of files of commands as
// code (commands-as-code.md, B33): the report of their check and what the
// import took over.
type CommandImportReport struct {
	CommandFileReport
	// Imported reports whether the import took the documents over; with
	// errors it takes over none (B62).
	Imported bool `json:"imported"`
	// Created counts the cooldown groups, groups and commands that are new
	// in the profile, Replaced those that replaced one of the same kind and
	// name (B33).
	Created  int `json:"created"`
	Replaced int `json:"replaced"`
}

// errRejected rolls back an import whose documents have errors.
var errRejected = errors.New("the documents have errors")

// ImportCommandFiles imports files of commands as code into the profile at
// profilePath (commands-as-code.md, B33, B34): with the checks of
// CheckCommandFiles, in one transaction, so that it takes over all
// documents or, with errors, none (B62). A document replaces the object of
// the same kind and name and keeps its ID and creation time; the others
// are created; objects without a document stay as they are. The core must
// be stopped; the caller holds the lock of the data directory. opts are
// passed to store.Open, e.g. for a backup before migrations.
func ImportCommandFiles(ctx context.Context, profilePath string, opts []store.Option, rights config.Rights, files []string) (CommandImportReport, error) {
	base, docs, err := readCommandFiles(files)
	report := CommandImportReport{CommandFileReport: base}
	if err != nil {
		return report, err
	}
	// store.Open would create a missing profile.
	if _, err := os.Stat(profilePath); err != nil {
		return report, fmt.Errorf("open the profile: %w", err)
	}
	st, err := store.Open(ctx, profilePath, opts...)
	if err != nil {
		return report, fmt.Errorf("open the profile: %w", err)
	}
	defer st.Close()

	var plan commandfile.Plan
	err = st.Atomically(ctx, func(tx *store.Store) error {
		p, problems, err := checkInto(ctx, tx, rights, docs)
		if err != nil {
			return err
		}
		plan = p
		report.Problems = append(report.Problems, problems...)
		if commandfile.HasErrors(report.Problems) {
			return errRejected
		}
		return nil
	})
	if err != nil && !errors.Is(err, errRejected) {
		return report, err
	}
	report.finish(files)
	if err == nil {
		report.Imported = true
		report.Created, report.Replaced = plan.Counts()
	}
	return report, nil
}

// readCommandFiles reads files into documents and a report with their
// problems so far.
func readCommandFiles(files []string) (CommandFileReport, []commandfile.Document, error) {
	report := CommandFileReport{Files: len(files), Problems: []commandfile.Problem{}}
	var docs []commandfile.Document
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return report, nil, err
		}
		d, problems := commandfile.Read(f, data)
		docs = append(docs, d...)
		report.Problems = append(report.Problems, problems...)
	}
	report.Documents = len(docs)
	return report, docs, nil
}

// finish sorts the problems in the order of files, then of their places,
// and counts them.
func (r *CommandFileReport) finish(files []string) {
	order := make(map[string]int, len(files))
	for i, f := range files {
		order[f] = i
	}
	slices.SortStableFunc(r.Problems, func(a, b commandfile.Problem) int {
		return cmp.Or(cmp.Compare(order[a.File], order[b.File]), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column))
	})
	r.Errors, r.Warnings = 0, 0
	for _, p := range r.Problems {
		if p.Severity == commandfile.SeverityWarning {
			r.Warnings++
		} else {
			r.Errors++
		}
	}
}

// snapshotProfile copies the profile at path to dest without changing it.
func snapshotProfile(ctx context.Context, path, dest string) error {
	ro, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return fmt.Errorf("open the profile: %w", err)
	}
	err = ro.VacuumInto(ctx, dest)
	return errors.Join(err, ro.Close())
}

// checkInto converts docs and saves them into st, the copy of a profile or
// the transaction of an import, and returns the plan it saved with the
// problems.
func checkInto(ctx context.Context, st *store.Store, rights config.Rights, docs []commandfile.Document) (commandfile.Plan, []commandfile.Problem, error) {
	var plan commandfile.Plan
	live := config.NewLive(rights)
	actions, err := ActionCatalog(live)
	if err != nil {
		return plan, nil, err
	}
	identifiers, err := IdentifierCatalog()
	if err != nil {
		return plan, nil, err
	}
	codec, err := command.NewCodec(actions.Entries()...)
	if err != nil {
		return plan, nil, err
	}
	svc, err := command.NewService(st, codec, command.Checks{
		Counters: st, Names: Reserved(identifiers, actions), Types: actions, Roots: live,
	})
	if err != nil {
		return plan, nil, err
	}
	existing, err := existingObjects(ctx, st)
	if err != nil {
		return plan, nil, err
	}
	types := commandfile.Types{Actions: actions.Descriptors(), Codec: codec}
	plan, problems := commandfile.Convert(docs, existing, types)
	plan, conflicts := commandfile.Conflicts(plan, existing)
	problems = append(problems, conflicts...)
	saved, err := commandfile.Apply(ctx, plan, svc, types)
	return plan, append(problems, saved...), err
}

// existingObjects lists the commands, groups and cooldown groups of st.
func existingObjects(ctx context.Context, st *store.Store) (commandfile.Existing, error) {
	var e commandfile.Existing
	recs, err := st.Commands(ctx)
	if err != nil {
		return e, err
	}
	for _, r := range recs {
		e.Commands = append(e.Commands, r.Header)
	}
	if e.Groups, err = st.Groups(ctx); err != nil {
		return e, err
	}
	e.CooldownGroups, err = st.CooldownGroups(ctx)
	return e, err
}
