// SPDX-License-Identifier: Apache-2.0

// Package app is the composition root of the core (Code-ADR-0002): it builds
// all components from the startup configuration, registers the long-running
// ones with the supervisor and runs them until the context ends.
//
// Start-up order (plan §6.6): lock the data directory, resolve and open the
// profile (with a backup before migrations), load the settings and the
// vault key, then run the supervised components.
//
// "streamcrew serve" uses it directly; the public start API core.Run
// (ADR-0006, roadmap phase 6) will be a thin wrapper around the same calls.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/backup"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/httpserver"
	"github.com/ripmav/streamcrew/internal/lockfile"
	"github.com/ripmav/streamcrew/internal/logging"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/supervisor"
	"github.com/ripmav/streamcrew/internal/vault"
)

// logFileName is the name of the log file in the log directory.
const logFileName = "streamcrew.log"

// Option configures an App.
type Option func(*options)

type options struct {
	console io.Writer
	keyring vault.Keyring
	envKey  string
}

// WithConsole sets the destination of the console log; the default is
// os.Stderr.
func WithConsole(w io.Writer) Option {
	return func(o *options) { o.console = w }
}

// WithKeyring sets the system keyring for the vault key; nil skips the
// keyring. The default is vault.SystemKeyring.
func WithKeyring(k vault.Keyring) Option {
	return func(o *options) { o.keyring = k }
}

// WithSecretKey passes the value of STREAMCREW_SECRET_KEY (ADR-0012).
func WithSecretKey(base64Key string) Option {
	return func(o *options) { o.envKey = base64Key }
}

// App is the wired core.
type App struct {
	cfg      config.Config
	version  string
	logger   *slog.Logger
	closeLog io.Closer
	lock     *lockfile.Lock
	profile  profile.Profile
	store    *store.Store
	settings *settings.Service
	vault    *vault.Vault
	bus      *event.Bus
	sup      *supervisor.Supervisor
	http     *httpserver.Server
	ready    *readiness
}

// New builds the core from a resolved configuration (config.Config.Resolve).
// It takes the data directory lock and opens the profile; Run releases
// them. If New fails, everything it opened is closed again.
func New(ctx context.Context, cfg config.Config, opts ...Option) (a *App, err error) {
	o := options{console: os.Stderr, keyring: vault.SystemKeyring{}}
	for _, opt := range opts {
		opt(&o)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	logger, closeLog, err := newLogger(cfg, o.console)
	if err != nil {
		return nil, err
	}
	a = &App{cfg: cfg, version: buildinfo.Read().Version, logger: logger, closeLog: closeLog, ready: newReadiness()}
	defer func() {
		if err != nil {
			err = errors.Join(err, a.close())
		}
	}()

	if a.lock, err = LockDataDir(cfg.DataDir); err != nil {
		return a, err
	}
	backups := BackupDir(cfg.DataDir)
	storeOpts := []store.Option{
		store.WithLogger(component(logger, "store")),
		store.WithBeforeMigrate(PreMigrationBackup(backups, a.version, component(logger, "backup"))),
	}
	profiles := profile.NewManager(cfg.DataDir, storeOpts...)
	if a.profile, err = profiles.Resolve(ctx, cfg.Profile); err != nil {
		return a, err
	}
	if a.store, err = store.Open(ctx, a.profile.Path, storeOpts...); err != nil {
		return a, fmt.Errorf("open profile %q: %w", a.profile.ID, err)
	}
	if err := a.resetCounters(ctx); err != nil {
		return a, err
	}
	if a.settings, err = settings.New(a.store); err != nil {
		return a, err
	}
	keys := vault.NewKeys(cfg.DataDir, o.envKey, o.keyring, component(logger, "vault"))
	ks, err := keys.Load(ctx)
	if err != nil {
		return a, fmt.Errorf("vault key: %w", err)
	}
	a.vault = vault.New(a.store, ks)

	catalog, err := newCatalog()
	if err != nil {
		return a, err
	}
	a.bus = event.NewBus(component(logger, "event"), event.WithCatalog(catalog))
	a.sup = supervisor.New(component(logger, "supervisor"),
		supervisor.WithStatusFunc(a.onStatus),
		supervisor.WithShutdownTimeout(cfg.ShutdownTimeout),
	)
	a.http = httpserver.New(httpserver.Config{
		Addr:            cfg.Listen,
		Dev:             cfg.Dev,
		ShutdownTimeout: httpShutdownTimeout(cfg.ShutdownTimeout),
	}, component(logger, "http"), a.ready.Ready)
	scheduler := backup.NewScheduler(a.store, backups,
		backup.Request{ProfileID: a.profile.ID, ProfileName: a.profile.Name, AppVersion: a.version},
		a.backupSchedule, component(logger, "backup"))

	err = errors.Join(
		a.sup.Add("backup", scheduler),
		a.sup.Add("http", a.http, supervisor.WithCritical()),
	)
	return a, err
}

// Logger returns the root logger of the core.
func (a *App) Logger() *slog.Logger {
	return a.logger
}

// HTTPServer returns the HTTP server, e.g. to find its address.
func (a *App) HTTPServer() *httpserver.Server {
	return a.http
}

// Bus returns the event bus.
func (a *App) Bus() *event.Bus {
	return a.bus
}

// Profile returns the running profile.
func (a *App) Profile() profile.Profile {
	return a.profile
}

// Ready reports whether the core is ready: all runnables run and the core is
// not shutting down.
func (a *App) Ready() bool {
	return a.ready.Ready()
}

// Run runs the core until ctx ends or a critical component fails, then shuts
// it down, closes the profile, releases the lock and closes the log file.
// The returned error has been logged.
func (a *App) Run(ctx context.Context) (err error) {
	defer func() { err = errors.Join(err, a.close()) }()

	stopWatching := context.AfterFunc(ctx, func() {
		a.ready.stopping()
		a.publish(context.WithoutCancel(ctx), eventtype.AppStopping, Stopping{})
	})
	defer stopWatching()

	a.logger.InfoContext(ctx, "streamcrew starting",
		"version", a.version, "mode", a.cfg.Mode, "profile", a.profile.ID,
		"data_dir", a.cfg.DataDir, "pid", os.Getpid())
	if a.cfg.Dev {
		a.logger.WarnContext(ctx, "developer mode is on: pprof is served under /debug/pprof/")
	}
	a.publish(ctx, eventtype.AppStarted, Started{Version: a.version, Mode: string(a.cfg.Mode), Profile: a.profile.ID})

	if err := a.sup.Run(ctx); err != nil {
		a.logger.ErrorContext(ctx, "streamcrew stopped with an error", "error", err)
		return err
	}
	a.logger.InfoContext(ctx, "streamcrew stopped")
	return nil
}

// Close releases what New opened without running the core. Run does this
// itself; calling Close after Run is harmless.
func (a *App) Close() error {
	return a.close()
}

// close releases what New opened, in reverse order; the log file last.
func (a *App) close() error {
	var errs []error
	if a.bus != nil {
		a.bus.Close()
	}
	if a.store != nil {
		errs = append(errs, a.store.Close())
		a.store = nil
	}
	if a.lock != nil {
		errs = append(errs, a.lock.Release())
		a.lock = nil
	}
	if a.closeLog != nil {
		errs = append(errs, a.closeLog.Close())
		a.closeLog = nil
	}
	return errors.Join(errs...)
}

// resetCounters sets the counters that reset on start to 0 (spec
// counters-and-quotes.md, B3).
func (a *App) resetCounters(ctx context.Context) error {
	n, err := a.store.ResetCountersOnStart(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		a.logger.InfoContext(ctx, "counters reset on start", "count", n)
	}
	return nil
}

// onStatus tracks readiness and publishes every state change of a runnable.
func (a *App) onStatus(st supervisor.Status) {
	a.ready.update(st)
	a.publish(context.Background(), TypeSupervisorStatus, st)
}

func (a *App) publish(ctx context.Context, typ event.Type, payload any) {
	e := event.New(event.Source{Kind: event.SourceSystem, Name: "app"}, typ, payload)
	if err := a.bus.Publish(ctx, e); err != nil {
		a.logger.ErrorContext(ctx, "publishing an event failed", "type", typ, "error", err)
	}
}

// backupSchedule reads the backup settings of the profile.
func (a *App) backupSchedule(ctx context.Context) (backup.Schedule, error) {
	b, err := settings.Load(ctx, a.settings, settings.DefaultBackups())
	if err != nil {
		return backup.Schedule{}, err
	}
	t, err := settings.Load(ctx, a.settings, settings.DefaultTime())
	if err != nil {
		return backup.Schedule{}, err
	}
	loc, err := t.Location()
	if err != nil {
		// A stored zone this build cannot load: UTC instead of stopping the
		// schedule (Code-ADR-0009).
		a.logger.WarnContext(ctx, "invalid profile time zone", "error", err)
	}
	hour, minute, err := b.Clock()
	if err != nil {
		return backup.Schedule{}, err
	}
	return backup.Schedule{
		Enabled:  b.Enabled,
		Hour:     hour,
		Minute:   minute,
		Policy:   backup.Policy{Daily: b.KeepDaily, Weekly: b.KeepWeekly, Monthly: b.KeepMonthly},
		Location: loc,
	}, nil
}

// httpShutdownTimeout is the part of the shutdown timeout the HTTP server
// may use to finish requests: half of it, at most 5 s. The rest remains for
// the other runnables and the forced close of lingering connections.
func httpShutdownTimeout(total time.Duration) time.Duration {
	return min(total/2, 5*time.Second)
}

func newLogger(cfg config.Config, console io.Writer) (*slog.Logger, io.Closer, error) {
	level, err := config.ParseLevel(cfg.Log.Level)
	if err != nil {
		return nil, nil, fmt.Errorf("log level: %w", err)
	}
	levels := make(map[string]slog.Level, len(cfg.Log.ComponentLevel))
	for name, l := range cfg.Log.ComponentLevel {
		if levels[name], err = config.ParseLevel(l); err != nil {
			return nil, nil, fmt.Errorf("log level of %s: %w", name, err)
		}
	}
	lc := logging.Config{
		Level:           level,
		ComponentLevels: levels,
		Console:         console,
		ConsoleFormat:   logging.Format(cfg.Log.Format),
		MaxSize:         int64(cfg.Log.MaxSize) << 20,
		MaxFiles:        cfg.Log.MaxFiles,
	}
	if cfg.Log.File {
		lc.File = filepath.Join(cfg.LogDir(), logFileName)
	}
	logger, closer, err := logging.New(lc)
	if err != nil {
		return nil, nil, fmt.Errorf("set up logging: %w", err)
	}
	return logger, closer, nil
}

func component(logger *slog.Logger, name string) *slog.Logger {
	return logger.With(logging.ComponentKey, name)
}

// readiness derives the readiness of the core from the states of its
// runnables.
type readiness struct {
	mu       sync.Mutex
	states   map[string]supervisor.State
	shutdown bool
}

func newReadiness() *readiness {
	return &readiness{states: make(map[string]supervisor.State)}
}

func (r *readiness) update(st supervisor.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[st.Name] = st.State
}

func (r *readiness) stopping() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shutdown = true
}

// Ready reports whether all runnables run and no shutdown has begun.
func (r *readiness) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shutdown || len(r.states) == 0 {
		return false
	}
	for _, s := range r.states {
		if s != supervisor.StateRunning {
			return false
		}
	}
	return true
}
