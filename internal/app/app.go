// SPDX-License-Identifier: MIT

// Package app is the composition root of the core (Code-ADR-0002): it builds
// all components from the startup configuration, registers the long-running
// ones with the supervisor and runs them until the context ends.
//
// Start-up order (plan §6.5): lock the data directory, resolve and open the
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/action/commandadmin"
	"github.com/ripmav/streamcrew/internal/action/commands"
	"github.com/ripmav/streamcrew/internal/action/flow"
	"github.com/ripmav/streamcrew/internal/action/host"
	"github.com/ripmav/streamcrew/internal/action/moderation"
	"github.com/ripmav/streamcrew/internal/action/network"
	streamaction "github.com/ripmav/streamcrew/internal/action/stream"
	twitchaction "github.com/ripmav/streamcrew/internal/action/twitch"
	"github.com/ripmav/streamcrew/internal/action/usercommand"
	"github.com/ripmav/streamcrew/internal/action/users"
	"github.com/ripmav/streamcrew/internal/action/values"
	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/apitoken"
	"github.com/ripmav/streamcrew/internal/auth"
	"github.com/ripmav/streamcrew/internal/backup"
	"github.com/ripmav/streamcrew/internal/breaker"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	chatsvc "github.com/ripmav/streamcrew/internal/chat"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/dryrun"
	"github.com/ripmav/streamcrew/internal/emote"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/eventservice"
	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/httpserver"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/lockfile"
	"github.com/ripmav/streamcrew/internal/logging"
	modsvc "github.com/ripmav/streamcrew/internal/moderation"
	"github.com/ripmav/streamcrew/internal/netguard"
	"github.com/ripmav/streamcrew/internal/preset"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/prompt"
	"github.com/ripmav/streamcrew/internal/requirement"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/supervisor"
	"github.com/ripmav/streamcrew/internal/template"
	timersvc "github.com/ripmav/streamcrew/internal/timer"
	"github.com/ripmav/streamcrew/internal/twitch"
	"github.com/ripmav/streamcrew/internal/userservice"
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
	// watchTick shortens the watch time tick of the user service (tests).
	watchTick time.Duration
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

// WithWatchTick shortens the watch time tick of the user service, for the
// end-to-end tests; the default is a minute. It must be positive.
func WithWatchTick(d time.Duration) Option {
	return func(o *options) { o.watchTick = d }
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
	cfg     config.Config
	version string
	logger  *slog.Logger
	// dynLog changes the log levels and output at runtime (roadmap 6.5);
	// console is the console writer the log always goes to.
	dynLog  *logging.Dynamic
	console io.Writer
	// msgLog writes the user messages (chat messages and whispers, with
	// the full text) to the separate file beside the log file; it is open
	// in the debug log level and in dry run (the gate is evaluated at
	// start), msgLogCloser closes it.
	msgLog       *slog.Logger
	msgLogCloser io.Closer
	lock         *lockfile.Lock
	profile      profile.Profile
	profiles     *profile.Manager
	store        *store.Store
	backups      string
	settings     *settings.Service
	vault        *vault.Vault
	auth         *auth.Service
	prompts      *prompt.Service
	bus          *event.Bus
	sup          *supervisor.Supervisor
	http         *httpserver.Server
	api          *api.Server
	ready        *readiness
	rights       *config.Live
	// file is the resolver of the configuration file; watcher applies
	// its changes at runtime (roadmap 6.5), nil without a file.
	file    *config.FileResolver
	watcher *config.Watcher
	// startedAt is when the core started; it is reported by the API's Status
	// (roadmap 6.2.1). shutdownCancel stops the core on request, e.g. the
	// API's Shutdown RPC; it is the cancel of the run context.
	startedAt time.Time
	// runCtx is the context of the whole core; it is set when Run starts and
	// ends with it. Long-running work the API starts (e.g. a login) is
	// derived from it.
	runCtx         context.Context
	shutdownOnce   sync.Once
	shutdownCancel context.CancelFunc
	// dryRun switches the dry run of the platforms at runtime (roadmap
	// 6.5); the watcher of the configuration file sets it.
	dryRun atomic.Bool
	// shutdownTimeout is the shutdown timeout; the watcher of the
	// configuration file changes it, the runnables read it at shutdown
	// time (roadmap 6.5).
	shutdownTimeout atomic.Int64
	// runtimeMu guards cfg: the configuration the core runs with, changed
	// by the hot applies of the watcher (roadmap 6.5).
	runtimeMu sync.RWMutex

	// The wiring of the command engine (roadmap 3.6): the platforms of the
	// profile and their lookup of users, the state of the stream, the
	// muted chat, and the template engine, the action types, the command
	// service, the engine and the event service.
	platforms  *platformSet
	chat       *chatsvc.Service
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
	moderation   *modsvc.Service
	timer        *timersvc.Service
	userSvc      *userservice.Service
	catalog      *event.Catalog
	// messages renders the localized texts of the core (ADR-0022).
	messages *i18n.Catalog
	// twitchHelix is the resilient HTTP client of the Helix API, shared by
	// the Twitch platform and the twitch action's operations (roadmap 4.4).
	twitchHelix *httpclient.Client
	// emotes is the emote catalog the Twitch platform resolves chat emotes
	// with (roadmap 4.4, task 9).
	emotes *emote.Catalog
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
	logger, dynLog, lc, err := newLogger(cfg, o.console)
	if err != nil {
		return nil, err
	}
	a = &App{
		cfg: cfg, version: buildinfo.Read().Version, logger: logger,
		dynLog: dynLog, console: o.console,
		ready: newReadiness(), startedAt: time.Now(),
	}
	a.dryRun.Store(cfg.DryRun)
	// verbose is on in the debug log level and in dry run: the user
	// messages go to the separate file and the API answers log their
	// bodies into the normal log. The gate is evaluated at start; a hot
	// change of the level or dry run does not open or close the file.
	verbose := cfg.DryRun
	if l, err := config.ParseLevel(cfg.Log.Level); err == nil && l <= slog.LevelDebug {
		verbose = true
	}
	if verbose && lc.File != "" {
		path := filepath.Join(filepath.Dir(lc.File), "messages.json")
		msgLog, closer, err := logging.NewFileLogger(path, lc.MaxSize, lc.MaxFiles)
		if err != nil {
			return a, fmt.Errorf("open the messages file: %w", err)
		}
		a.msgLog, a.msgLogCloser = msgLog, closer
		a.logger.InfoContext(ctx, "the user messages are logged to the separate file", "file", path)
	}
	a.shutdownTimeout.Store(int64(cfg.ShutdownTimeout))
	defer func() {
		if err != nil {
			err = errors.Join(err, a.close())
		}
	}()

	if a.lock, err = LockDataDir(cfg.DataDir); err != nil {
		return a, err
	}
	backups := BackupDir(cfg.DataDir)
	a.backups = backups
	storeOpts := []store.Option{
		store.WithLogger(component(logger, "store")),
		store.WithBeforeMigrate(PreMigrationBackup(backups, a.version, component(logger, "backup"))),
	}
	profiles := profile.NewManager(cfg.DataDir, storeOpts...)
	a.profiles = profiles
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
	// The log stream (roadmap 6.6): every record the level filter passes is
	// published as a "log" event beside the console and file output.
	a.dynLog.SetSink(logSinkHandler{a: a})
	// The prompt mechanism (Plan §6.14): prompts are published to the bus,
	// delivered through the live stream and answered with the API.
	a.prompts = prompt.New(func(ctx context.Context, p prompt.Prompt) {
		// A prompt that cannot be published is dropped; it is not fatal.
		_ = a.bus.Publish(ctx, event.New(event.Source{Kind: event.SourceSystem, Name: "prompt"}, prompt.Type, p))
	})

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
	// The token calls run through the resilient HTTP client (Code-ADR-0014)
	// with one breaker per API (Code-ADR-0007); the Helix endpoints of
	// phase 4.2 join the same instances (roadmap 4.2). The client rate of
	// 4/s (240/min) stays below the Twitch default of 800 points per
	// minute per (client id, user id); the Helix client serves the
	// streamer and the bot, so the 240/min is shared by both buckets,
	// and a 429 waits until the Ratelimit-Reset header (dev.twitch.tv,
	// "Twitch Rate Limits").
	twitchAuth := httpclient.New(httpclient.Options{
		Name:      "twitch.auth",
		Base:      authClient,
		Breaker:   breaker.New("twitch.auth", component(logger, "auth"), httpclient.Evaluator),
		Logger:    component(logger, "auth"),
		Responses: verbose,
	})
	twitchHelix := httpclient.New(httpclient.Options{
		Name:      "twitch.helix",
		Base:      authClient,
		Breaker:   breaker.New("twitch.helix", component(logger, "auth"), httpclient.Evaluator),
		Logger:    component(logger, "auth"),
		Responses: verbose,
	})
	a.twitchHelix = twitchHelix
	// The emote catalog (roadmap 4.4, task 9) resolves the emote IDs of
	// the chat messages: Twitch via Helix, BetterTTV and FrankerFaceZ over
	// their own resilient clients (own allow list names, Code-ADR-0019).
	emoteClient := func(name string) *httpclient.Client {
		return httpclient.New(httpclient.Options{
			Name:      name,
			Base:      authClient,
			Breaker:   breaker.New(name, component(logger, "emote"), httpclient.Evaluator),
			Logger:    component(logger, "emote"),
			Responses: verbose,
		})
	}
	a.emotes = emote.New([]emote.Source{
		emote.NewTwitchSource(func(ctx context.Context, _ string) ([]emote.Emote, error) {
			return a.twitchEmotes(ctx)
		}),
		emote.NewBTTVSource(emoteClient("emote.bttv"), ""),
		emote.NewFFZSource(emoteClient("emote.ffz"), ""),
	}, time.Hour, component(logger, "emote"))
	if a.auth, err = auth.New(auth.Ports{
		Store:     a.store,
		Vault:     func(repo vault.Repository) *vault.Vault { return vault.New(repo, ks) },
		Flows:     authFlows(authClient, twitchAuth, twitchHelix),
		Embedded:  EmbeddedApp,
		Publisher: a.bus,
		Logger:    component(logger, "auth"),
	}); err != nil {
		return a, err
	}

	// The platforms of the profile are built after the event service,
	// which is their receiver; until then the set is empty.
	a.platforms = newPlatformSet()
	// The user service is the implementation of the user ports of the core
	// (roadmap 5.2.6): it counts the user statistics and the watch time
	// from the events on the bus (5.2.1), evaluates the regular role and
	// keeps the follow and subscription data of the identities fresh
	// (5.2.2), and looks up users by login name, over the platform for
	// unknown ones (command-engine.md, B17; actions.md, B91), the channel's
	// accounts and the strikes.
	userOpts := []userservice.Option{userservice.WithLogger(component(logger, "users"))}
	if o.watchTick > 0 {
		userOpts = append(userOpts, userservice.WithWatchTick(o.watchTick))
	}
	if a.userSvc, err = userservice.New(userservice.Ports{
		Store:     userStore{a.store},
		Bus:       a.bus,
		Settings:  a.usersSettings,
		Platforms: a.platforms,
	}, userOpts...); err != nil {
		return a, err
	}
	a.chat, err = chatsvc.New(chatsvc.Ports{
		Platforms: a.platforms,
		Settings:  a.chatSettings,
		DataDir:   cfg.DataDir,
		Logger:    component(logger, "chat"),
	})
	if err != nil {
		return a, err
	}

	messageCatalog, err := i18n.Load()
	if err != nil {
		return a, err
	}
	a.messages = messageCatalog
	requirements, err := requirement.New(requirement.Ports{
		Catalog:   messageCatalog,
		Language:  localeLanguage{settings: a.settings},
		Platforms: a.platforms,
		Cooldowns: a.store,
		Streamer:  a.userSvc,
		General:   a.generalSettings,
		Chat:      a.chatSettings,
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
		template.UserFamily(a.userSvc),
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
		template.WithSources(globals, template.CounterSource(a.store), template.CommandSource(a.store)),
	)

	// The command service decodes commands with the types that have ports,
	// so that the engine runs the actions it loads (Code-ADR-0013, point 3).
	// The command action switches through the service, which the late port
	// names once it is built.
	late := &lateSwitches{}
	lateCmds := &lateCommands{}
	if a.actions, err = a.actionTypes(globals, late, lateCmds); err != nil {
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
		Channels: scopeChannels{store: a.store, settings: a.settings},
	})
	if err != nil {
		return a, err
	}
	late.set(a.commands)
	lateCmds.set(a.commands)
	if err = a.seedPresets(ctx); err != nil {
		return a, err
	}
	a.engine, err = engine.New(a.commands, a.actions,
		engine.WithLogger(component(logger, "engine")),
		engine.WithPublisher(a.bus),
		engine.WithConfig(a.engineConfig),
		engine.WithShutdownTimeoutFunc(a.shutdownTimeoutDuration),
		engine.WithRequirements(requirements),
		engine.WithUsers(a.userSvc),
	)
	if err != nil {
		return a, err
	}
	a.moderation, err = modsvc.New(modsvc.Ports{
		Settings: a.moderationSettings,
		Strikes:  a.userSvc,
		Commands: a.commands,
		Engine:   a.engine,
	}, modsvc.WithLogger(component(logger, "moderation")))
	if err != nil {
		return a, err
	}
	eventOpts := []eventservice.Option{
		eventservice.WithLogger(component(logger, "events")),
		eventservice.WithUnknownCommandGate(func() bool {
			// unknown commands are log noise: only in the debug log
			// level or in dry run (the gate stays a function so a hot
			// change of either can flip it without a restart)
			l, err := config.ParseLevel(a.cfg.Log.Level)
			return a.cfg.DryRun || err == nil && l <= slog.LevelDebug
		}),
	}
	if a.msgLog != nil {
		eventOpts = append(eventOpts, eventservice.WithMessagesLogger(a.msgLog))
	}
	a.events, err = eventservice.New(ctx, eventservice.Ports{
		Store:         a.store,
		Commands:      a.commands,
		Engine:        a.engine,
		Publisher:     a.bus,
		Chat:          a.chat,
		Moderation:    a.moderation,
		Settings:      a.eventsSettings,
		ChannelPoints: a.channelPointsSettings,
	}, eventOpts...)
	if err != nil {
		return a, err
	}
	a.timer, err = timersvc.New(timersvc.Ports{
		Commands:  a.commands,
		Engine:    a.engine,
		Platforms: timerPlatforms{set: a.platforms},
		Live:      a.events,
		Messages:  a.chat,
		Settings:  a.timersSettings,
	}, timersvc.WithLogger(component(logger, "timer")))
	if err != nil {
		return a, err
	}
	a.sup = supervisor.New(component(logger, "supervisor"),
		supervisor.WithStatusFunc(a.onStatus),
		supervisor.WithShutdownTimeoutFunc(a.shutdownTimeoutDuration),
	)
	a.http = httpserver.New(httpserver.Config{
		Addr:            cfg.Listen,
		Dev:             cfg.Dev,
		CorsAllow:       cfg.CorsAllow,
		TLSCert:         cfg.TLSCert,
		TLSKey:          cfg.TLSKey,
		ShutdownTimeout: httpShutdownTimeout(cfg.ShutdownTimeout),
	}, component(logger, "http"), a.ready.Ready)
	a.http.SetShutdownTimeoutFunc(func() time.Duration {
		return httpShutdownTimeout(a.shutdownTimeoutDuration())
	})
	scheduler := backup.NewScheduler(a.store, backups,
		backup.Request{ProfileID: a.profile.ID, ProfileName: a.profile.Name, AppVersion: a.version},
		a.backupSchedule, component(logger, "backup"))

	// The API (roadmap 6.2.1): ConnectRPC handlers over the application
	// services, mounted on the HTTP server as a catch-all. It reads the
	// platforms live, so it is set before they are filled; the handlers
	// see the current state at request time.
	a.api = api.New(api.ServerConfig{
		Version:   a.version,
		Mode:      string(a.cfg.Mode),
		Profile:   a.profile.Name,
		StartedAt: a.startedAt,
		Profiles:  apiProfiles{a: a},
		Platforms: apiPlatforms{a: a},
		Counters:  apiCounters{a: a},
		Settings:  a.settings,
		Users:     apiUsers{a: a},
		Chat:      apiChat{a: a},
		Backups:   apiBackup{a: a},
		Auth:      newAPIAuth(a),
		Commands:  apiCommands{a: a},
		Stream:    newAPIStream(a, cfg.Stream),
		Config:    apiConfig{a: a},
		Tokens:    apitoken.Open(cfg.DataDir),
		Shutdown:  a.Shutdown,
	})
	a.http.SetAPI(a.api.Handler())

	if cfg.Mode == config.ModeDesktop {
		if err := a.ensureDesktopToken(); err != nil {
			return a, fmt.Errorf("desktop token: %w", err)
		}
	}

	err = errors.Join(
		a.sup.Add("auth", a.auth),
		a.sup.Add("backup", scheduler),
		a.sup.Add("http", a.http, supervisor.WithCritical()),
		a.sup.Add("events", a.events),
		a.sup.Add("timer", a.timer),
		a.sup.Add("users", a.userSvc),
	)
	// The platform stops after the engine on shutdown, so that the engine
	// can still deliver its pending instances to it; on start the
	// supervisor runs the runnables at the same time, so the platforms are
	// connected when the core is ready and "app.started" goes out.
	// Unless the option gives a platform (the mock console), it is the
	// Twitch platform (ADR-0004: Twitch at the start); it connects when
	// the streamer account has a token (roadmap 4.3, task 5).
	if o.platform == nil {
		o.platform = func(ctx context.Context, b PlatformBuilder) (connector.Platform, error) {
			// The additional channels of the settings (roadmap 4.6); without
			// the section, the home channel is operated only. The chat
			// adapters are the start configuration (--twitch-eventsub,
			// --twitch-irc, plan 2026-10-22), not a setting of the profile.
			var channels []string
			if chs, err := settings.Load(ctx, a.settings, settings.DefaultChannels()); err != nil {
				logger.WarnContext(ctx, "the channels settings are unknown, the home channel is operated only", "error", err)
			} else {
				channels = chs.Additional
			}
			return twitch.NewPlatform(twitch.PlatformOptions{
				Receiver:       b.Receiver,
				Auth:           twitchStreamAuth{s: a.auth},
				Helix:          twitchHelix,
				DialClient:     authClient,
				Emotes:         a.emotes,
				Channels:       channels,
				EventSub:       cfg.Twitch.EventSub,
				IRC:            cfg.Twitch.IRC,
				BTTVSocketBase: emote.BTTVSocketBase,
				Logger:         component(b.Logger, "twitch"),
			}), nil
		}
	}
	if o.platform != nil {
		err = errors.Join(err, a.addPlatform(ctx, o.platform))
	}
	err = errors.Join(err, a.sup.Add("engine", a.engine))
	a.file = o.file
	if o.file != nil {
		a.watcher = config.NewWatcher(o.file, cfg, a.rights, a.hotApplies(), component(logger, "config"))
		err = errors.Join(err, a.sup.Add("config", a.watcher))
	}
	return a, err
}

// shutdownTimeoutDuration returns the current shutdown timeout; the
// runnables read it at shutdown time, so the watcher can change it at
// runtime (roadmap 6.5).
func (a *App) shutdownTimeoutDuration() time.Duration {
	return time.Duration(a.shutdownTimeout.Load())
}

// hotApplies are the hooks of the watcher that apply the hot settings of
// the configuration file at runtime (roadmap 6.5).
func (a *App) hotApplies() *config.ApplyFuncs {
	return &config.ApplyFuncs{
		Log: a.applyLog,
		DryRun: func(on bool) {
			a.dryRun.Store(on)
		},
		Dev:       a.HTTPServer().SetDev,
		CorsAllow: a.HTTPServer().SetCorsAllow,
		Listen:    a.applyListen,
		ShutdownTimeout: func(d time.Duration) {
			a.shutdownTimeout.Store(int64(d))
		},
	}
}

// applyLog applies the log settings of the configuration file to the
// logger tree (roadmap 6.5).
func (a *App) applyLog(lc config.LogConfig) error {
	a.runtimeMu.Lock()
	a.cfg.Log = lc
	a.runtimeMu.Unlock()
	lcfg, err := logConfig(a.cfgView(), a.console)
	if err != nil {
		return err
	}
	return a.dynLog.Update(lcfg)
}

// applyListen moves the HTTP server to the new address with the TLS
// certificate (roadmap 6.5); on error the previous binding stays active.
func (a *App) applyListen(addr, tlsCert, tlsKey string) error {
	a.runtimeMu.Lock()
	old := a.cfg
	a.cfg.Listen, a.cfg.TLSCert, a.cfg.TLSKey = addr, tlsCert, tlsKey
	a.runtimeMu.Unlock()
	if err := a.HTTPServer().Rebind(context.Background(), httpserver.Config{
		Addr:            a.cfg.Listen,
		Dev:             a.cfg.Dev,
		CorsAllow:       a.cfg.CorsAllow,
		TLSCert:         tlsCert,
		TLSKey:          tlsKey,
		ShutdownTimeout: httpShutdownTimeout(a.shutdownTimeoutDuration()),
	}); err != nil {
		a.runtimeMu.Lock()
		a.cfg = old
		a.runtimeMu.Unlock()
		return err
	}
	if a.cfg.Mode != config.ModeDesktop {
		a.writeRuntimeFile()
	}
	return nil
}

// Mode returns the mode the core runs in.
func (a *App) Mode() config.Mode {
	return a.cfgView().Mode
}

// Watcher returns the watcher of the configuration file, nil without a file.
func (a *App) Watcher() *config.Watcher {
	return a.watcher
}

// cfgView returns a copy of the configuration the core runs with.
func (a *App) cfgView() config.Config {
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	return a.cfg
}

// actionTypes returns the registry of the action types with their ports,
// so that the engine runs them (Code-ADR-0013, point 3); switches is the
// port of the command action, set once the command service is built.
func (a *App) actionTypes(globals *template.Globals, switches commands.Switches, cmds commandadmin.Commands) (*action.Registry, error) {
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
		Chat:      a.chat,
	}))
	add(commands.Descriptors(commands.Ports{
		Templates: a.templates,
		Switches:  switches,
		Logger:    component(a.logger, "action"),
	}))
	add(commandadmin.Descriptors(commandadmin.Ports{
		Commands: cmds,
		Logger:   component(a.logger, "action"),
	}))
	add(usercommand.Descriptors(usercommand.Ports{
		Templates: a.templates,
		Chat:      a.chat,
		Answers:   a.store,
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
		Lookup:    a.userSvc,
		Strikes:   a.userSvc,
		Mute:      a.chat,
		General:   a.generalSettings,
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
	add(streamaction.Descriptors(streamaction.Ports{
		Templates: a.templates,
		Platforms: a.platforms,
		General:   a.generalSettings,
		Logger:    component(a.logger, "action"),
	}))
	add(users.Descriptors(users.Ports{
		Templates: a.templates,
		Logger:    component(a.logger, "action"),
	}))
	add(values.Descriptors(values.Ports{
		Templates: a.templates,
		Counters:  a.store,
		Globals:   globals,
	}))
	add(twitchaction.Descriptors(twitchaction.Ports{
		Templates: a.templates,
		Ops:       a.twitchOps(),
		Logger:    component(a.logger, "action"),
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
	// The platform is always wrapped in the switchable dry-run wrapper
	// (roadmap 6.5); the dry run itself is switched at runtime.
	p = dryrun.NewSwitchablePlatform(p, component(a.logger, "dryrun"), &a.dryRun)
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

// MockPlatform returns the mock platform the core runs, or nil without
// one; the tests and the API use it.
func (a *App) MockPlatform() *mock.Platform {
	p, ok := a.platforms.Platform(platform.Mock)
	if !ok {
		return nil
	}
	if inner, ok := dryrun.AsInner(p); ok {
		p = inner
	}
	mp, ok := p.(*mock.Platform)
	if !ok {
		return nil
	}
	return mp
}

// ShutdownTimeout returns the current shutdown timeout (roadmap 6.5).
func (a *App) ShutdownTimeout() time.Duration {
	return a.shutdownTimeoutDuration()
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

	// runCtx is the context of the whole core: it ends when the caller's ctx
	// ends or when the core is stopped on request (e.g. the API's Shutdown
	// RPC, roadmap 6.2.1).
	runCtx, cancel := context.WithCancel(ctx)
	a.runCtx = runCtx
	defer cancel()
	a.shutdownCancel = cancel

	a.logger.InfoContext(ctx, "streamcrew starting",
		"version", a.version, "mode", a.cfg.Mode, "profile", a.profile.ID,
		"data_dir", a.cfg.DataDir, "pid", os.Getpid())
	if a.cfg.DryRun {
		a.logger.InfoContext(ctx, "dry run is on: the platforms record their side effects instead of causing them")
	}
	if a.cfg.Dev {
		a.logger.WarnContext(ctx, "developer mode is on: pprof is served under /debug/pprof/")
	}
	config.LogRights(ctx, a.logger, a.rights.Rights())

	supErr := make(chan error, 1)
	go func() { supErr <- a.sup.Run(runCtx) }()

	// The runtime file (ADR-0005): the desktop app and diagnostic tools
	// read the address, the PID and the version from the data directory;
	// it is removed when the core stops.
	go a.writeRuntimeFile()
	defer a.removeRuntimeFile()

	// "app.started" goes through the event service (events.md, B12), when
	// the core is ready for the first time: the engine takes instances and
	// the platforms are connected, so its event command runs and its output
	// reaches the platforms. publishStarted waits for and is seen by the
	// shutdown, so it is never published after "app.stopping".
	select {
	case <-a.ready.Done():
		// The engine reports to the supervisor when its goroutine is
		// started, but it takes instances only once Run ran: wait for
		// the engine, so the event commands of "app.started" are not
		// dropped while it is still idle.
		<-a.engine.Ready()
		if runCtx.Err() == nil {
			if err := a.ready.publishStarted(func() error {
				return a.events.Application(runCtx, eventtype.AppStarted, Started{Version: a.version, Mode: string(a.cfg.Mode), Profile: a.profile.ID})
			}); err != nil {
				a.logger.ErrorContext(ctx, "the started event failed", "error", err)
			}
		}
	case err := <-supErr:
		a.logger.ErrorContext(ctx, "streamcrew stopped with an error", "error", err)
		return err
	case <-runCtx.Done():
	}

	// The select returned when the core ran for the first time, so wait
	// for the context to end, or for a critical component to fail, and
	// only then does "app.stopping" go out (events.md, B12), while the
	// components still run, so that the engine takes it for its event
	// command and the bus still holds it for its subscribers.
	select {
	case <-runCtx.Done():
	case err := <-supErr:
		a.logger.ErrorContext(ctx, "streamcrew stopped with an error", "error", err)
		return err
	}
	a.stoppingEvent(runCtx)

	if err := <-supErr; err != nil {
		a.logger.ErrorContext(ctx, "streamcrew stopped with an error", "error", err)
		return err
	}
	a.logger.InfoContext(ctx, "streamcrew stopped")
	return nil
}

// stoppingEvent publishes "app.stopping" through the event service
// (events.md, B12) while the components still run; it gives up with a
// live context, because the core did not stop by request.
func (a *App) stoppingEvent(ctx context.Context) {
	if ctx.Err() == nil {
		return
	}
	stopCtx := context.WithoutCancel(ctx)
	if err := a.ready.publishStopping(func() error {
		return a.events.Application(stopCtx, eventtype.AppStopping, Stopping{})
	}); err != nil {
		a.logger.ErrorContext(stopCtx, "the stopping event failed", "error", err)
	}
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
	if a.chat != nil {
		errs = append(errs, a.chat.Close())
	}
	if a.store != nil {
		errs = append(errs, a.store.Close())
		a.store = nil
	}
	if a.lock != nil {
		errs = append(errs, a.lock.Release())
		a.lock = nil
	}
	if a.msgLogCloser != nil {
		errs = append(errs, a.msgLogCloser.Close())
		a.msgLogCloser = nil
	}
	if a.dynLog != nil {
		errs = append(errs, a.dynLog.Close())
		a.dynLog = nil
	}
	return errors.Join(errs...)
}

// Shutdown stops the core on request (roadmap 6.2.1). It ends the run context
// so the supervisor shuts the components down gracefully; the API's Shutdown
// RPC calls it. It is a no-op before Run and after the core has stopped.
func (a *App) Shutdown() {
	a.shutdownOnce.Do(func() {
		if a.shutdownCancel != nil {
			a.shutdownCancel()
		}
	})
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

// seedPresets adds the ready-made commands of a fresh profile (roadmap 5.6,
// P0): every preset that does not exist yet by name is saved, so a fresh
// profile gets them and a later start keeps them. An existing command with a
// preset's name is left as it is.
func (a *App) seedPresets(ctx context.Context) error {
	have := map[string]bool{}
	cmds, err := a.commands.Commands(ctx)
	if err != nil {
		return fmt.Errorf("seeding presets: %w", err)
	}
	for _, c := range cmds {
		have[strings.ToLower(c.Name)] = true
	}
	for _, c := range preset.Commands() {
		if have[strings.ToLower(c.Name)] {
			continue
		}
		if _, err := a.commands.Save(ctx, c); err != nil {
			return fmt.Errorf("seeding preset %q: %w", c.Name, err)
		}
		a.logger.InfoContext(ctx, "preset command added", "command", c.Name)
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

func newLogger(cfg config.Config, console io.Writer) (*slog.Logger, *logging.Dynamic, logging.Config, error) {
	lc, err := logConfig(cfg, console)
	if err != nil {
		return nil, nil, logging.Config{}, err
	}
	logger, dyn, err := logging.NewDynamic(lc)
	if err != nil {
		return nil, nil, logging.Config{}, fmt.Errorf("set up logging: %w", err)
	}
	return logger, dyn, lc, nil
}

// logConfig translates the log settings of the configuration to the
// logging.Config, resolving the log file in the data directory.
func logConfig(cfg config.Config, console io.Writer) (logging.Config, error) {
	level, err := config.ParseLevel(cfg.Log.Level)
	if err != nil {
		return logging.Config{}, fmt.Errorf("log level: %w", err)
	}
	levels := make(map[string]slog.Level, len(cfg.Log.ComponentLevel))
	for name, l := range cfg.Log.ComponentLevel {
		if levels[name], err = config.ParseLevel(l); err != nil {
			return logging.Config{}, fmt.Errorf("log level of %s: %w", name, err)
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
	switch cfg.Log.File {
	case "false":
		// no log file
	default:
		// true or an empty value keep the default location; anything else
		// is the path of the log file.
		if path := cfg.Log.File; path != "" && path != "true" {
			lc.File = path
		} else {
			lc.File = filepath.Join(cfg.LogDir(), logFileName)
		}
	}
	return lc, nil
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
