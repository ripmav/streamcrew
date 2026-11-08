// SPDX-License-Identifier: MIT

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
	"strconv"
	"strings"
	"time"
)

// ReloadInterval is how often Watcher compares the configuration file
// (Code-ADR-0019, point 5).
const ReloadInterval = time.Second

// Hot returns the settings whose changes in the configuration file apply
// without a restart (roadmap 6.5): the rights of Code-ADR-0019 plus the
// log settings, the dry run, the developer mode, the CORS allowlist, the
// listen address with its TLS certificate, the mode and the shutdown
// timeout. log-file is the exception: it applies only at start
// (Code-ADR-0021, Nachtrag 2026-10-24, point 6).
func Hot() []string {
	return []string{
		"grant", "revoke", "file-root", "outbound-allow",
		"log-level", "log-component-level", "log-format",
		"log-max-size", "log-max-files",
		"dry-run", "dev", "cors-allow", "listen", "tls-cert", "tls-key",
		"mode", "shutdown-timeout",
	}
}

// RestartOnly returns the settings that apply only after a restart
// (roadmap 6.5): the data directory and the profile open files and
// databases the running process cannot move, the configuration file path
// itself, and log-file (Code-ADR-0021, Nachtrag 2026-10-24, point 6).
func RestartOnly() []string {
	return []string{"data-dir", "profile", "config", "log-file"}
}

// ApplyFuncs are the hooks the Watcher calls for the hot settings it finds
// in the configuration file (roadmap 6.5). Every hook is optional; a nil
// hook leaves the setting unapplied. The hooks run in the order of the
// settings in Hot.
type ApplyFuncs struct {
	// Log applies the log settings of the file.
	Log func(LogConfig) error
	// DryRun switches the dry run of the platforms.
	DryRun func(bool)
	// Dev switches the pprof routes.
	Dev func(bool)
	// CorsAllow replaces the CORS allowlist.
	CorsAllow func([]string)
	// Listen moves the HTTP server to the address with the TLS
	// certificate; the old binding must stay active on error.
	Listen func(addr, tlsCert, tlsKey string) error
	// ShutdownTimeout applies the shutdown timeout.
	ShutdownTimeout func(time.Duration)
}

// Watcher reloads the settings from the configuration file without a
// restart (Code-ADR-0019, point 5, extended by roadmap 6.5). It compares
// the content of the file every ReloadInterval and takes the hot settings
// only together and only from a valid file; otherwise the settings stay as
// they are. Settings set by flag or environment variable stay fixed.
// Changes of the other settings apply after a restart, which the log says.
// Run it under the supervisor.
type Watcher struct {
	path     string
	base     Config
	fromFile map[string]bool
	allowed  map[string]bool
	start    map[string]any
	live     *Live
	applies  *ApplyFuncs
	logger   *slog.Logger

	// last is the content last compared; existed reports whether there
	// was a file.
	last    []byte
	existed bool
	// problem is the last problem logged, so that it is logged once.
	problem string
	// restart are the keys last reported as needing a restart.
	restart []string
	// current is the configuration the last applied change left the core
	// in, for comparing against the next file content.
	current Config
	// logFile is the log file setting the core runs with: log-file
	// applies only at start (Code-ADR-0021, Nachtrag 2026-10-24, point
	// 6), so the hot applies pass it through unchanged.
	logFile string
}

// NewWatcher returns a watcher of the file that file read or would read,
// for the configuration base the core started with. It stores the rights
// in live. applies may be nil for the rights-only behavior of
// Code-ADR-0019.
func NewWatcher(file *FileResolver, base Config, live *Live, applies *ApplyFuncs, logger *slog.Logger) *Watcher {
	return &Watcher{
		path:     file.watch,
		base:     base,
		fromFile: maps.Clone(file.fromFile),
		allowed:  maps.Clone(file.allowed),
		start:    maps.Clone(file.values),
		live:     live,
		applies:  applies,
		logger:   logger,
		last:     bytes.Clone(file.data),
		existed:  file.data != nil,
		current:  base,
		logFile:  base.Log.File,
	}
}

// Run implements supervisor.Runnable. It returns when ctx ends.
func (w *Watcher) Run(ctx context.Context) error {
	if w.path == "" {
		<-ctx.Done()
		return nil
	}
	for _, flag := range Hot() {
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
		// Some editors remove the file while saving; the settings stay.
		w.report(ctx, err.Error())
		return
	case w.existed && bytes.Equal(data, w.last):
		return
	}
	w.last, w.existed = data, true

	cfg, restart, err := w.parse(data)
	if err != nil {
		w.report(ctx, err.Error())
		return
	}
	w.problem = ""
	rights, err := cfg.Rights()
	if err != nil {
		w.report(ctx, err.Error())
		return
	}
	if old := w.live.Rights(); !rights.Equal(old) {
		w.live.Store(rights)
		w.logger.InfoContext(ctx, "rights reloaded from the configuration file", rightsAttrs(rights)...)
		for _, warning := range rights.Warnings() {
			w.logger.WarnContext(ctx, "rights", "warning", warning)
		}
	}
	w.applyHot(ctx, cfg)
	if !slices.Equal(restart, w.restart) {
		w.restart = restart
		if len(restart) > 0 {
			w.logger.InfoContext(ctx, "configuration changes apply after a restart", "settings", strings.Join(restart, ", "))
		}
	}
}

// applyHot calls the hooks for the hot settings that changed against the
// current configuration. A failing hook keeps the previous state and logs
// the problem.
func (w *Watcher) applyHot(ctx context.Context, cfg Config) {
	if w.applies == nil {
		w.current = cfg
		return
	}
	old := w.current
	oldLog := old.Log
	oldDryRun := old.DryRun
	oldDev := old.Dev
	oldCors := old.CorsAllow
	oldListen, oldCert, oldKey := old.Listen, old.TLSCert, old.TLSKey
	oldMode, oldTimeout := old.Mode, old.ShutdownTimeout

	// log-file applies only at start (Code-ADR-0021, Nachtrag 2026-10-24,
	// point 6): the hot apply keeps the file setting the core runs with,
	// so a change of log-file alone never touches the log.
	oldLog.File = w.logFile
	newLog := cfg.Log
	newLog.File = w.logFile
	if !reflect.DeepEqual(oldLog, newLog) && w.applies.Log != nil {
		if err := w.applies.Log(newLog); err != nil {
			w.applyError(ctx, "log", err)
		} else {
			w.applied(ctx, "log", newLog.Level)
		}
	}
	if oldDryRun != cfg.DryRun && w.applies.DryRun != nil {
		w.applies.DryRun(cfg.DryRun)
		w.applied(ctx, "dry-run", strconv.FormatBool(cfg.DryRun))
	}
	if oldDev != cfg.Dev && w.applies.Dev != nil {
		w.applies.Dev(cfg.Dev)
		w.applied(ctx, "dev", strconv.FormatBool(cfg.Dev))
	}
	if !slices.Equal(oldCors, cfg.CorsAllow) && w.applies.CorsAllow != nil {
		w.applies.CorsAllow(cfg.CorsAllow)
		w.applied(ctx, "cors-allow", strings.Join(cfg.CorsAllow, ", "))
	}
	if oldListen != cfg.Listen || oldCert != cfg.TLSCert || oldKey != cfg.TLSKey {
		if w.applies.Listen != nil {
			if err := w.applies.Listen(cfg.Listen, cfg.TLSCert, cfg.TLSKey); err != nil {
				w.applyError(ctx, "listen", err)
			} else {
				w.applied(ctx, "listen", cfg.Listen)
			}
		}
	}
	if oldMode != cfg.Mode {
		// The mode changes the base of the rights, which the rights
		// update of check already applied; nothing else is hot.
		w.applied(ctx, "mode", string(cfg.Mode))
	}
	if oldTimeout != cfg.ShutdownTimeout && w.applies.ShutdownTimeout != nil {
		w.applies.ShutdownTimeout(cfg.ShutdownTimeout)
		w.applied(ctx, "shutdown-timeout", cfg.ShutdownTimeout.String())
	}
	w.current = cfg
}

// applied logs a hot setting that the Watcher applied.
func (w *Watcher) applied(ctx context.Context, setting, value string) {
	w.logger.InfoContext(ctx, "setting applied from the configuration file", "setting", setting, "value", value)
}

// applyError logs a hot setting the Watcher could not apply; the previous
// state stays.
func (w *Watcher) applyError(ctx context.Context, setting string, err error) {
	w.logger.WarnContext(ctx, "setting not applied, the previous value stays", "setting", setting, "error", err)
}

// ErrNarrowOnly reports that a document extends the currently effective
// configuration. The API may only narrow it (Code-ADR-0021, Nachtrag
// 2026-10-24, points 1 to 3); the keys the document extends are named in
// the error, so that they can be extended in the configuration file.
var ErrNarrowOnly = errors.New("the document extends the currently effective configuration")

// Update validates data against the configuration the core runs with
// and stores it in the file when it differs from the content last seen
// (roadmap 6.5.2). It returns the hot settings that change at runtime
// and the settings that apply after a restart, and the stored document.
// The Watcher applies the change on its next comparison. A document that
// extends the currently effective configuration is rejected as a whole
// (Code-ADR-0021, Nachtrag 2026-10-24); only the file itself may extend.
func (w *Watcher) Update(ctx context.Context, data []byte) (applied, restart []string, stored []byte, err error) {
	cfg, restart, err := w.parse(data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("update configuration: %w", err)
	}
	if w.existed && bytes.Equal(data, w.last) {
		return nil, restart, data, nil
	}
	if keys := extendKeys(w.current, cfg); len(keys) > 0 {
		return nil, nil, nil, fmt.Errorf("%w: %s; extend them in the configuration file",
			ErrNarrowOnly, strings.Join(keys, ", "))
	}
	if err := writeFile(w.path, data); err != nil {
		return nil, nil, nil, fmt.Errorf("update configuration: %w", err)
	}
	applied = hotChanges(w.current, cfg)
	w.logger.InfoContext(ctx, "configuration document updated", "file", w.path)
	return applied, restart, data, nil
}

// extendKeys returns the keys of next that extend the effective old
// configuration, in the order of the file keys; empty if the document
// only narrows (Code-ADR-0021, Nachtrag 2026-10-24, points 1 to 2).
// Measured is the current effective configuration, not the start: what
// the file narrowed before, the API cannot widen back.
func extendKeys(old, next Config) []string {
	seen := map[string]bool{}
	var out []string
	add := func(keys ...string) {
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}

	oldRights, errOld := old.Rights()
	nextRights, errNext := next.Rights()
	if errOld != nil || errNext != nil {
		// Fail-closed: without the rights of both sides, nothing of
		// them may change.
		return []string{"grant", "revoke", "file-root", "outbound-allow"}
	}
	// The effective capabilities (mode base, grant, revoke) may only
	// shrink; a new grant entry or a removed revoke entry extends.
	if len(oldRights.Capabilities.Missing(nextRights.Capabilities.List())) > 0 {
		add("grant", "revoke")
	}
	// A new root, or a root that leaves the directory of the old root,
	// extends; a subdirectory of it narrows.
	for name, dir := range nextRights.Roots {
		oldDir, ok := oldRights.Roots[name]
		if !ok || !insideDir(dir, oldDir) {
			add("file-root")
		}
	}
	// An outbound target not in the current allowlist extends.
	oldOutbound := make(map[string]bool, len(oldRights.Outbound.Entries()))
	for _, entry := range oldRights.Outbound.Entries() {
		oldOutbound[entry] = true
	}
	for _, entry := range nextRights.Outbound.Entries() {
		if !oldOutbound[entry] {
			add("outbound-allow")
		}
	}
	// Every mode change extends: the API writes in the server mode, and
	// every other mode has a larger rights base and no SSRF protection.
	if next.Mode != old.Mode {
		add("mode")
	}
	// An origin not in the current allowlist extends.
	for _, origin := range next.CorsAllow {
		if !slices.Contains(old.CorsAllow, origin) {
			add("cors-allow")
		}
	}
	if extendsListen(old.Listen, next.Listen) {
		add("listen")
	}
	// The overlay listen is checked the same way: a bind beyond the old
	// host extends (Code-ADR-0021, Nachtrag 2026-10-29, point 2). An
	// unchanged (or both unset) value is no extension.
	if next.OverlayListen != old.OverlayListen && extendsListen(old.OverlayListen, next.OverlayListen) {
		add("overlay-listen")
	}
	// Removing TLS beyond loopback extends; swapping the certificate
	// stays allowed.
	if old.TLSCert != "" && next.TLSCert == "" && next.PublicInsecure() {
		add("tls-cert", "tls-key")
	}
	// Turning the developer mode on extends.
	if !old.Dev && next.Dev {
		add("dev")
	}
	return out
}

// extendsListen reports whether next binds a host that old did not,
// besides loopback and Unix sockets (Code-ADR-0021, Nachtrag 2026-10-24,
// point 2); another port on the same host is no extension. An
// unparseable address denies, fail-closed.
func extendsListen(old, next string) bool {
	if strings.HasPrefix(next, "unix:") {
		return false
	}
	nextHost, err := splitListen(next)
	if err != nil {
		return true
	}
	if isLoopback(nextHost) {
		return false
	}
	if strings.HasPrefix(old, "unix:") {
		return true // old bound no TCP host
	}
	oldHost, err := splitListen(old)
	if err != nil {
		return true
	}
	return nextHost != oldHost
}

// insideDir reports whether inner is the same directory as outer or lies
// in it; both are the clean absolute directories of the roots.
func insideDir(inner, outer string) bool {
	if inner == outer {
		return true
	}
	rel, err := filepath.Rel(outer, inner)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hotChanges returns the hot settings that differ between old and new.
func hotChanges(old, next Config) []string {
	var out []string
	// log-file is restart-only (Code-ADR-0021, Nachtrag 2026-10-24,
	// point 6) and changes nothing at runtime.
	oldLog, nextLog := old.Log, next.Log
	oldLog.File, nextLog.File = "", ""
	if !reflect.DeepEqual(oldLog, nextLog) {
		out = append(out, "log")
	}
	if old.DryRun != next.DryRun {
		out = append(out, "dry-run")
	}
	if old.Dev != next.Dev {
		out = append(out, "dev")
	}
	if !slices.Equal(old.CorsAllow, next.CorsAllow) {
		out = append(out, "cors-allow")
	}
	if old.Listen != next.Listen || old.TLSCert != next.TLSCert || old.TLSKey != next.TLSKey {
		out = append(out, "listen")
	}
	if old.Mode != next.Mode {
		out = append(out, "mode")
	}
	if old.ShutdownTimeout != next.ShutdownTimeout {
		out = append(out, "shutdown-timeout")
	}
	return out
}

// writeFile stores data at path atomically (tmp + rename) with mode 0600.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Clean(path))
}

// report logs a problem with the file once; the settings stay as they are.
func (w *Watcher) report(ctx context.Context, problem string) {
	if problem == w.problem {
		return
	}
	w.problem = problem
	w.logger.WarnContext(ctx, "configuration file not reloaded, the settings stay as they are",
		"file", w.path, "error", problem)
}

// parse returns the effective configuration the file describes — the base
// the core started with, replaced by the file values of the settings the
// file decides — and the settings that changed and need a restart.
func (w *Watcher) parse(data []byte) (Config, []string, error) {
	values, err := parseFile(w.path, data, w.allowed)
	if err != nil {
		return Config{}, nil, err
	}
	cfg := w.base
	var errs []error
	for _, key := range slices.Sorted(maps.Keys(w.allowed)) {
		if !w.fromFile[flagOf(key)] {
			continue
		}
		v := values[key]
		switch key {
		case "data_dir", "profile", "config":
			// Not hot; the file value is still checked by Resolve.
		case "grant":
			if cfg.Grant, err = stringList(flagOf(key), v); err != nil {
				errs = append(errs, err)
			}
		case "revoke":
			if cfg.Revoke, err = stringList(flagOf(key), v); err != nil {
				errs = append(errs, err)
			}
		case "outbound_allow":
			if cfg.OutboundAllow, err = stringList(flagOf(key), v); err != nil {
				errs = append(errs, err)
			}
		case "cors_allow":
			if cfg.CorsAllow, err = stringList(flagOf(key), v); err != nil {
				errs = append(errs, err)
			}
		case "file_root":
			if cfg.FileRoot, err = stringMap(flagOf(key), v); err != nil {
				errs = append(errs, err)
			}
		case "mode":
			if m, ok := v.(string); ok {
				if cfg.Mode, err = parseMode(m); err != nil {
					errs = append(errs, err)
				}
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a text", key))
			}
		case "listen":
			if s, ok := v.(string); ok {
				cfg.Listen = s
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a text", key))
			}
		case "overlay_listen":
			if s, ok := v.(string); ok {
				cfg.OverlayListen = s
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a text", key))
			}
		case "dev":
			if b, ok := v.(bool); ok {
				cfg.Dev = b
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a bool", key))
			}
		case "tls_cert":
			if s, ok := v.(string); ok {
				cfg.TLSCert = s
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a text", key))
			}
		case "tls_key":
			if s, ok := v.(string); ok {
				cfg.TLSKey = s
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a text", key))
			}
		case "dry_run":
			if b, ok := v.(bool); ok {
				cfg.DryRun = b
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a bool", key))
			}
		case "shutdown_timeout":
			if s, ok := v.(string); ok {
				d, perr := time.ParseDuration(s)
				if perr != nil {
					errs = append(errs, fmt.Errorf("%s: %w", key, perr))
				} else {
					cfg.ShutdownTimeout = d
				}
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a duration", key))
			}
		case "log_level":
			if s, ok := v.(string); ok {
				cfg.Log.Level = s
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a text", key))
			}
		case "log_component_level":
			if m, ok := v.(map[string]any); ok {
				levels := make(map[string]string, len(m))
				for k, e := range m {
					s, ok := e.(string)
					if !ok {
						errs = append(errs, fmt.Errorf("%s: the value of %s is not a text", key, k))
						break
					}
					levels[k] = s
				}
				cfg.Log.ComponentLevel = levels
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not an object", key))
			}
		case "log_format":
			if s, ok := v.(string); ok {
				cfg.Log.Format = s
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a text", key))
			}
		case "log_file":
			if s, ok := v.(string); ok {
				cfg.Log.File = s
			} else if b, ok := v.(bool); ok {
				cfg.Log.File = strconv.FormatBool(b)
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a bool or text", key))
			}
		case "log_max_size":
			if n, ok := v.(int); ok {
				cfg.Log.MaxSize = n
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a number", key))
			}
		case "log_max_files":
			if n, ok := v.(int); ok {
				cfg.Log.MaxFiles = n
			} else if v != nil {
				errs = append(errs, fmt.Errorf("%s: not a number", key))
			}
		default:
			// The key is allowed and fixed by flag or environment: it
			// does not reach the core.
			continue
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, nil, err
	}
	if err := cfg.Resolve(); err != nil {
		return Config{}, nil, err
	}

	var restart []string
	for _, key := range slices.Sorted(maps.Keys(w.allowed)) {
		if slices.Contains(Hot(), flagOf(key)) {
			continue
		}
		// The values are YAML trees of maps, lists and scalars of any
		// type; reflect.DeepEqual compares them without knowing the
		// schema.
		if !reflect.DeepEqual(values[key], w.start[key]) {
			restart = append(restart, key)
		}
	}
	return cfg, restart, nil
}

// flagOf converts a configuration file key to the flag name.
func flagOf(key string) string {
	return strings.ReplaceAll(key, "_", "-")
}

// parseMode converts a mode name to a Mode.
func parseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeDesktop, ModeDaemon, ModeServer:
		return Mode(s), nil
	default:
		return "", fmt.Errorf("unknown mode %q", s)
	}
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
