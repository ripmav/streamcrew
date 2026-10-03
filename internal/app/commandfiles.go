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
	report := CommandFileReport{Files: len(files), Problems: []commandfile.Problem{}}
	var docs []commandfile.Document
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return report, err
		}
		d, problems := commandfile.Read(f, data)
		docs = append(docs, d...)
		report.Problems = append(report.Problems, problems...)
	}
	report.Documents = len(docs)

	dir, err := os.MkdirTemp("", "streamcrew-validate-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(dir)
	copyPath := filepath.Join(dir, "profile.db")
	if err := snapshotProfile(ctx, profilePath, copyPath); err != nil {
		return report, err
	}
	st, err := store.Open(ctx, copyPath, store.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		return report, fmt.Errorf("open the copy of the profile: %w", err)
	}
	defer st.Close()

	problems, err := checkInto(ctx, st, rights, docs)
	if err != nil {
		return report, err
	}
	report.Problems = append(report.Problems, problems...)
	order := make(map[string]int, len(files))
	for i, f := range files {
		order[f] = i
	}
	slices.SortStableFunc(report.Problems, func(a, b commandfile.Problem) int {
		return cmp.Or(cmp.Compare(order[a.File], order[b.File]), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column))
	})
	for _, p := range report.Problems {
		if p.Severity == commandfile.SeverityWarning {
			report.Warnings++
		} else {
			report.Errors++
		}
	}
	return report, nil
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

// checkInto converts docs and saves them into st, the copy of a profile.
func checkInto(ctx context.Context, st *store.Store, rights config.Rights, docs []commandfile.Document) ([]commandfile.Problem, error) {
	live := config.NewLive(rights)
	actions, err := ActionCatalog(live)
	if err != nil {
		return nil, err
	}
	identifiers, err := IdentifierCatalog()
	if err != nil {
		return nil, err
	}
	codec, err := command.NewCodec(actions.Entries()...)
	if err != nil {
		return nil, err
	}
	svc, err := command.NewService(st, codec, command.Checks{
		Counters: st, Names: Reserved(identifiers, actions), Types: actions, Roots: live,
	})
	if err != nil {
		return nil, err
	}
	existing, err := existingObjects(ctx, st)
	if err != nil {
		return nil, err
	}
	types := commandfile.Types{Actions: actions.Descriptors(), Codec: codec}
	plan, problems := commandfile.Convert(docs, existing, types)
	plan, conflicts := commandfile.Conflicts(plan, existing)
	problems = append(problems, conflicts...)
	saved, err := commandfile.Apply(ctx, plan, svc, types)
	return append(problems, saved...), err
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
