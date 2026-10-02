// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

// ReloadInterval is how often Watcher compares the configuration file
// (Code-ADR-0019, point 5).
const ReloadInterval = time.Second

// Reloadable returns the flags whose changes in the configuration file
// apply without a restart: the rights of Code-ADR-0019.
func Reloadable() []string {
	return []string{"grant", "revoke", "file-root", "outbound-allow"}
}

// Watcher reloads the rights from the configuration file without a restart
// (Code-ADR-0019, point 5). It compares the content of the file every
// ReloadInterval and takes grant, revoke, file_root and outbound_allow only
// together and only from a valid file; otherwise the rights stay as they
// are. Settings set by flag or environment variable stay fixed. Changes of
// the other settings apply after a restart, which the log says. Run it
// under the supervisor.
type Watcher struct {
	path     string
	base     Config
	fromFile map[string]bool
	allowed  map[string]bool
	start    map[string]any
	live     *Live
	logger   *slog.Logger

	// last is the content last compared; existed reports whether there
	// was a file.
	last    []byte
	existed bool
	// problem is the last problem logged, so that it is logged once.
	problem string
	// restart are the keys last reported as needing a restart.
	restart []string
}

// NewWatcher returns a watcher of the file that file read or would read,
// for the configuration base the core started with. It stores the rights
// in live.
func NewWatcher(file *FileResolver, base Config, live *Live, logger *slog.Logger) *Watcher {
	return &Watcher{
		path:     file.watch,
		base:     base,
		fromFile: maps.Clone(file.fromFile),
		allowed:  maps.Clone(file.allowed),
		start:    maps.Clone(file.values),
		live:     live,
		logger:   logger,
		last:     bytes.Clone(file.data),
		existed:  file.data != nil,
	}
}

// Run implements supervisor.Runnable. It returns when ctx ends.
func (w *Watcher) Run(ctx context.Context) error {
	if w.path == "" {
		<-ctx.Done()
		return nil
	}
	for _, flag := range Reloadable() {
		if !w.fromFile[flag] {
			w.logger.InfoContext(ctx, "setting fixed by flag or environment, file changes need a restart", "setting", fileKey(flag))
		}
	}
	t := time.NewTicker(ReloadInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			w.check(ctx)
		}
	}
}

// check compares the file with the content last seen and applies a
// change.
func (w *Watcher) check(ctx context.Context) {
	data, err := os.ReadFile(filepath.Clean(w.path))
	switch {
	case errors.Is(err, fs.ErrNotExist) && !w.existed:
		return // still no file
	case err != nil:
		// Some editors remove the file while saving; the rights stay.
		w.report(ctx, err.Error())
		return
	case w.existed && bytes.Equal(data, w.last):
		return
	}
	w.last, w.existed = data, true

	rights, restart, err := w.parse(data)
	if err != nil {
		w.report(ctx, err.Error())
		return
	}
	w.problem = ""
	if old := w.live.Rights(); !rights.Equal(old) {
		w.live.Store(rights)
		w.logger.InfoContext(ctx, "rights reloaded from the configuration file", rightsAttrs(rights)...)
		for _, warning := range rights.Warnings() {
			w.logger.WarnContext(ctx, "rights", "warning", warning)
		}
	}
	if !slices.Equal(restart, w.restart) {
		w.restart = restart
		if len(restart) > 0 {
			w.logger.InfoContext(ctx, "configuration changes apply after a restart", "settings", strings.Join(restart, ", "))
		}
	}
}

// report logs a problem with the file once; the rights stay as they are.
func (w *Watcher) report(ctx context.Context, problem string) {
	if problem == w.problem {
		return
	}
	w.problem = problem
	w.logger.WarnContext(ctx, "configuration file not reloaded, the rights stay as they are",
		"file", w.path, "error", problem)
}

// parse returns the rights of the file data and the other settings that
// differ from the start and need a restart.
func (w *Watcher) parse(data []byte) (Rights, []string, error) {
	values, err := parseFile(w.path, data, w.allowed)
	if err != nil {
		return Rights{}, nil, err
	}
	cfg := w.base
	var errs []error
	for _, flag := range Reloadable() {
		if !w.fromFile[flag] {
			continue
		}
		v := values[fileKey(flag)]
		switch flag {
		case "grant":
			cfg.Grant, err = stringList(flag, v)
		case "revoke":
			cfg.Revoke, err = stringList(flag, v)
		case "outbound-allow":
			cfg.OutboundAllow, err = stringList(flag, v)
		case "file-root":
			cfg.FileRoot, err = stringMap(flag, v)
		}
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return Rights{}, nil, err
	}
	rights, err := cfg.Rights()
	if err != nil {
		return Rights{}, nil, err
	}

	var restart []string
	for _, key := range slices.Sorted(maps.Keys(w.allowed)) {
		if slices.Contains(Reloadable(), strings.ReplaceAll(key, "_", "-")) {
			continue
		}
		// The values are YAML trees of maps, lists and scalars of any
		// type; reflect.DeepEqual compares them without knowing the
		// schema.
		if !reflect.DeepEqual(values[key], w.start[key]) {
			restart = append(restart, key)
		}
	}
	return rights, restart, nil
}

// stringList returns the list v of the file, a YAML list of texts or one
// text, as kong reads it.
func stringList(flag string, v any) ([]string, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{v}, nil
	case []any:
		list := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%s: %v is not a text", fileKey(flag), e)
			}
			list = append(list, s)
		}
		return list, nil
	default:
		return nil, fmt.Errorf("%s: not a list", fileKey(flag))
	}
}

// stringMap returns the map v of the file, a YAML object of texts.
func stringMap(flag string, v any) (map[string]string, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		m := make(map[string]string, len(v))
		for k, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%s: the value of %s is not a text", fileKey(flag), k)
			}
			m[k] = s
		}
		return m, nil
	default:
		return nil, fmt.Errorf("%s: not an object", fileKey(flag))
	}
}

// rightsAttrs returns the log attributes of r.
func rightsAttrs(r Rights) []any {
	caps := make([]string, 0)
	for _, c := range r.Capabilities.List() {
		caps = append(caps, string(c))
	}
	roots := make([]string, 0, len(r.Roots))
	for _, name := range slices.Sorted(maps.Keys(r.Roots)) {
		roots = append(roots, name+"="+r.Roots[name])
	}
	return []any{
		"capabilities", strings.Join(caps, ", "),
		"file_roots", strings.Join(roots, ", "),
		"outbound_allow", strings.Join(r.Outbound.Entries(), ", "),
	}
}

// LogRights logs the rights at start, with their warnings (Code-ADR-0019,
// point 7).
func LogRights(ctx context.Context, logger *slog.Logger, r Rights) {
	logger.InfoContext(ctx, "rights", rightsAttrs(r)...)
	for _, warning := range r.Warnings() {
		logger.WarnContext(ctx, "rights", "warning", warning)
	}
}
