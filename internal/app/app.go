// SPDX-License-Identifier: MIT

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
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/action/commands"
	"github.com/ripmav/streamcrew/internal/action/flow"
	"github.com/ripmav/streamcrew/internal/action/host"
	"github.com/ripmav/streamcrew/internal/action/moderation"
	"github.com/ripmav/streamcrew/internal/action/network"
	"github.com/ripmav/streamcrew/internal/action/users"
	"github.com/ripmav/streamcrew/internal/action/values"
	"github.com/ripmav/streamcrew/internal/auth"
	"github.com/ripmav/streamcrew/internal/backup"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/eventservice"
	"github.com/ripmav/streamcrew/internal/httpserver"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/lockfile"
	"github.com/ripmav/streamcrew/internal/logging"
	"github.com/ripmav/streamcrew/internal/netguard"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/requirement"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/supervisor"
	"github.com/ripmav/streamcrew/internal/template"
	"github.com/ripmav/streamcrew/internal/vault"
)

// logFileName is the name of the log file in the log directory.
const logFileName = "streamcrew.log"

// Option configures an App.
type Option func(*options)

type options struct {
	console  io.Writer
	keyring  vault.Keyring
	envKey   string
	file     *config.FileResolver
	platform func(ctx context.Context, b PlatformBuilder) (connector.Platform, error)
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

// WithConfigFile passes the resolver of the configuration file, so that
// changes of the rights in the file apply without a restart
// (Code-ADR-0019, point 5). Without it, the rights stay as they were at
// start.
func WithConfigFile(f *config.FileResolver) Option {
	return func(o *options) { o.file = f }
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
	auth     *auth.Service
	bus      *event.Bus
	sup      *supervisor.Supervisor
	http     *httpserver.Server
	ready    *readiness
	rights   *config.Live

	// The wiring of the command engine (roadmap 3.6): the platforms of the
	// profile and their lookup of users, the state of the stream, the
	// muted chat, and the template engine, the action types, the command
	// service, the engine and the event service.
	platforms  *platformSet
	users      *userLookup
	mute       *chatMute
	programEnv []string
	// systemLocale is the locale of the environment, read once at start
	// (B41); the templates format dates and times with it when the profile
	// has "system" as the locale of the formats.
	systemLocale string
	templates    *template.Engine
	actions      *action.Registry
	commands     *command.Service
	engine       *engine.Engine
	events       *eventservice.Service
	catalog      *event.Catalog
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

	rights, err := cfg.Rights()
	if err != nil {
		return a, err
	}
	a.rights = config.NewLive(rights)
	// The environment the host action gives the programs it starts: it is
	// read once at start (actions.md, B117; Code-ADR-0005).
	a.programEnv = config.ProgramEnv()
	if a.programEnv == nil {
		a.programEnv = []string{}
	}
	a.systemLocale = config.SystemLocale()

	catalog, err := newCatalog()
	if err != nil {
		return a, err
	}
	a.catalog = catalog
	a.bus = event.NewBus(component(logger, "event"), event.WithCatalog(catalog))

	// The auth service keeps the platform logins and their tokens
	// (roadmap 4.1, ADR-0014); in server mode its requests go through the
	// outbound allow list (Code-ADR-0019).
	authClient := &http.Client{Timeout: 30 * time.Second}
	if cfg.Mode == config.ModeServer {
		dialer := netguard.Dialer{
			Protect:   true,
			Allowlist: a.rights.Outbound,
		}
		authClient.Transport = &http.Transport{DialContext: dialer.DialContext}
	}
	if a.auth, err = auth.New(auth.Ports{
		Store:     a.store,
		Vault:     func(repo vault.Repository) *vault.Vault { return vault.New(repo, ks) },
		Flows:     authFlows(authClient),
		Publisher: a.bus,
		Logger:    component(logger, "auth"),
	}); err != nil {
		return a, err
	}

	// The platforms of the profile are built after the event service,
	// which is their receiver; until then the set is empty.
	a.platforms = newPlatformSet()
	a.users = &userLookup{store: a.store, set: a.platforms}
	a.mute = newChatMute(component(logger, "moderation"))

	messageCatalog, err := i18n.Load()
	if err != nil {
		return a, err
	}
	requirements, err := requirement.New(requirement.Ports{
		Catalog:   messageCatalog,
		Language:  localeLanguage{settings: a.settings},
		Platforms: a.platforms,
		Cooldowns: a.store,
		Streamer:  a.users,
		Logger:    component(logger, "requirement"),
	})
	if err != nil {
		return a, err
	}

	// The template engine and its sources, in the order of B10: first the
	// global values, then the counters.
	globals := template.NewGlobals()
	identifiers, err := template.NewRegistry(
		template.CharacterFamily(),
		template.RunFamily(),
		template.UserFamily(a.users),
		template.DateTimeFamily(),
		template.MessageFamily(),
		template.StreamFamily(streamStates{set: a.platforms}),
		template.ArgumentFamily(),
		template.RandomFamily(),
	)
	if err != nil {
		return a, err
	}
	a.templates = template.New(identifiers,
		template.WithLogger(component(logger, "template")),
		template.WithSources(globals, template.CounterSource(a.store)),
	)

	// The command service decodes commands with the types that have ports,
	// so that the engine runs the actions it loads (Code-ADR-0013, point 3).
	// The command action switches through the service, which the late port
	// names once it is built.
	late := &lateSwitches{}
	if a.actions, err = a.actionTypes(globals, late); err != nil {
		return a, err
	}
	codec, err := command.NewCodec(a.actions.Entries()...)
	if err != nil {
		return a, err
	}
	reserved, err := IdentifierCatalog()
	if err != nil {
		return a, err
	}
	a.commands, err = command.NewService(a.store, codec, command.Checks{
		Counters: a.store,
		Names:    Reserved(reserved, a.actions),
		Types:    a.actions,
		Roots:    a.rights,
	})
	if err != nil {
		return a, err
	}
	late.set(a.commands)
	a.engine, err = engine.New(a.commands, a.actions,
		engine.WithLogger(component(logger, "engine")),
		engine.WithPublisher(a.bus),
		engine.WithConfig(a.engineConfig),
		engine.WithShutdownTimeout(cfg.ShutdownTimeout),
		engine.WithRequirements(requirements),
		engine.WithUsers(a.users),
	)
	if err != nil {
		return a, err
	}
	a.events, err = eventservice.New(ctx, eventservice.Ports{
		Store:     a.store,
		Commands:  a.commands,
		Engine:    a.engine,
		Publisher: a.bus,
		Settings:  a.eventsSettings,
	}, eventservice.WithLogger(component(logger, "events")))
	if err != nil {
		return a, err
	}

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
		a.sup.Add("auth", a.auth),
		a.sup.Add("backup", scheduler),
		a.sup.Add("http", a.http, supervisor.WithCritical()),
		a.sup.Add("events", a.events),
	)
	// The platform stops after the engine on shutdown, so that the engine
	// can still deliver its pending instances to it; on start the
	// supervisor runs the runnables at the same time, so the platforms are
	// connected when the core is ready and "app.started" goes out.
	if o.platform != nil {
		err = errors.Join(err, a.addPlatform(ctx, o.platform))
	}
	err = errors.Join(err, a.sup.Add("engine", a.engine))
	if o.file != nil {
		err = errors.Join(err, a.sup.Add("config",
			config.NewWatcher(o.file, cfg, a.rights, component(logger, "config"))))
	}
	return a, err
}

// actionTypes returns the registry of the action types with their ports,
// so that the engine runs them (Code-ADR-0013, point 3); switches is the
// port of the command action, set once the command service is built.
func (a *App) actionTypes(globals *template.Globals, switches commands.Switches) (*action.Registry, error) {
	var descs []action.Descriptor
	var errs []error
	add := func(d []action.Descriptor, err error) {
		if err != nil {
			errs = append(errs, err)
			return
		}
		descs = append(descs, d...)
	}
	add(chat.Descriptors(chat.Ports{
		Templates: a.templates,
		Platforms: a.platforms,
		Logger:    component(a.logger, "action"),
	}))
	add(commands.Descriptors(commands.Ports{
		Templates: a.templates,
		Switches:  switches,
		Logger:    component(a.logger, "action"),
	}))
	add(flow.Descriptors(flow.Ports{
		Templates: a.templates,
		IntN:      rand.IntN,
	}))
	add(host.Descriptors(host.Ports{
		Templates: a.templates,
		Env:       a.programEnv,
		Opener:    host.SystemOpener(),
		Roots:     a.rights,
		IntN:      rand.IntN,
		Logger:    component(a.logger, "action"),
	}))
	add(moderation.Descriptors(moderation.Ports{
		Templates: a.templates,
		Platforms: a.platforms,
		Users:     a.users,
		Strikes:   a.store,
		Mute:      a.mute,
		Logger:    component(a.logger, "action"),
	}))
	add(network.Descriptors(network.Ports{
		Templates: a.templates,
		Dialer: netguard.Dialer{
			Protect:   a.cfg.Mode == config.ModeServer,
			Allowlist: a.rights.Outbound,
		},
		Logger: component(a.logger, "action"),
	}))
	add(users.Descriptors(users.Ports{
		Templates: a.templates,
		Platforms: a.platforms,
		Users:     a.users,
		Logger:    component(a.logger, "action"),
	}))
	add(values.Descriptors(values.Ports{
		Templates: a.templates,
		Counters:  a.store,
		Globals:   globals,
	}))
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return action.NewRegistry(a.rights, descs...)
}

// addPlatform builds the platform of the profile, registers its event
// types in the catalog of the bus, and runs it with the supervisor
// (Code-ADR-0004).
func (a *App) addPlatform(ctx context.Context, build func(ctx context.Context, b PlatformBuilder) (connector.Platform, error)) error {
	p, err := build(ctx, PlatformBuilder{Receiver: a.events, Publisher: a.bus, Logger: a.logger})
	if err != nil {
		return fmt.Errorf("build platform: %w", err)
	}
	r, ok := any(p).(supervisor.Runnable)
	if !ok {
		return fmt.Errorf("platform %s: not a runnable", p.Name())
	}
	if reg, ok := any(p).(eventRegistrar); ok {
		if err := reg.RegisterEvents(a.catalog); err != nil {
			return fmt.Errorf("event types of platform %s: %w", p.Name(), err)
		}
	}
	if err := a.platforms.fill(p); err != nil {
		return err
	}
	return a.sup.Add(string(p.Name()), r)
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

// Rights returns the rights that apply now: the capabilities, the released
// roots and the allowlist of network targets (Code-ADR-0019). The action
// type registry, the file action and the web requests read them at every
// check.
func (a *App) Rights() *config.Live {
	return a.rights
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
//
// The application events go through the event service (events.md, B12);
// "app.started" when the core is ready for the first time, so that its
// event command runs and its output reaches the connected platforms, and
// "app.stopping" when the context ends, which the engine still takes for
// its event command (command-engine.md, B55).
func (a *App) Run(ctx context.Context) (err error) {
	defer func() { err = errors.Join(err, a.close()) }()

	stopWatching := context.AfterFunc(ctx, func() {
		stopCtx := context.WithoutCancel(ctx)
		if err := a.ready.publishStopping(func() error {
			return a.events.Application(stopCtx, eventtype.AppStopping, Stopping{})
		}); err != nil {
			a.logger.ErrorContext(stopCtx, "the stopping event failed", "error", err)
		}
	})
	defer stopWatching()

	a.logger.InfoContext(ctx, "streamcrew starting",
		"version", a.version, "mode", a.cfg.Mode, "profile", a.profile.ID,
		"data_dir", a.cfg.DataDir, "pid", os.Getpid())
	if a.cfg.Dev {
		a.logger.WarnContext(ctx, "developer mode is on: pprof is served under /debug/pprof/")
	}
	config.LogRights(ctx, a.logger, a.rights.Rights())

	supErr := make(chan error, 1)
	go func() { supErr <- a.sup.Run(ctx) }()

	// "app.started" goes through the event service (events.md, B12), when
	// the core is ready for the first time: the engine takes instances and
	// the platforms are connected, so its event command runs and its output
	// reaches the platforms. publishStarted waits for and is seen by the
	// shutdown, so it is never published after "app.stopping".
	select {
	case <-a.ready.Done():
		if ctx.Err() == nil {
			if err := a.ready.publishStarted(func() error {
				return a.events.Application(ctx, eventtype.AppStarted, Started{Version: a.version, Mode: string(a.cfg.Mode), Profile: a.profile.ID})
			}); err != nil {
				a.logger.ErrorContext(ctx, "the started event failed", "error", err)
			}
		}
	case err := <-supErr:
		a.logger.ErrorContext(ctx, "streamcrew stopped with an error", "error", err)
		return err
	case <-ctx.Done():
	}

	if err := <-supErr; err != nil {
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

// onStatus publishes every state change of a runnable and then tracks
// readiness, so that "app.started", which follows the readiness, reaches the
// bus after the "supervisor.status" events it is based on.
func (a *App) onStatus(st supervisor.Status) {
	a.publish(context.Background(), TypeSupervisorStatus, st)
	a.ready.update(st)
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
	// started is true once "app.started" was published or given up.
	started bool
	// done closes when every runnable runs for the first time.
	done   chan struct{}
	closed bool
}

func newReadiness() *readiness {
	return &readiness{states: make(map[string]supervisor.State), done: make(chan struct{})}
}

func (r *readiness) update(st supervisor.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[st.Name] = st.State
	r.doneOnce()
}

// doneOnce closes done when every runnable runs for the first time. r.mu is
// held.
func (r *readiness) doneOnce() {
	if r.closed || r.shutdown || len(r.states) == 0 {
		return
	}
	for _, s := range r.states {
		if s != supervisor.StateRunning {
			return
		}
	}
	r.closed = true
	close(r.done)
}

// Done returns a channel that closes when every runnable runs for the first
// time; it stays open if a runnable never reaches running.
func (r *readiness) Done() <-chan struct{} {
	return r.done
}

// publishStarted publishes "app.started" through fn, at most once, and never
// after the shutdown has begun (events.md, B12). It holds the lock while fn
// runs, so the shutdown waits for a publication in flight. fn must not call
// back into the readiness.
func (r *readiness) publishStarted(fn func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shutdown || r.started {
		return nil
	}
	r.started = true
	return fn()
}

// publishStopping marks the shutdown and publishes "app.stopping" through fn
// (events.md, B12): it waits for a "app.started" in flight, and every later
// publishStarted sees the shutdown and gives up.
func (r *readiness) publishStopping(fn func() error) error {
	r.mu.Lock()
	r.shutdown = true
	r.mu.Unlock()
	return fn()
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
