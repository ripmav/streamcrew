// SPDX-License-Identifier: MIT

// This file has the wiring of the command engine into the core (roadmap
// 3.6): the platforms of the profile and their lookup of users, the state
// of the stream, the muted chat and the settings the engine, the
// requirement service and the event service read.

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/template"
)

// PlatformBuilder is what the platform factory of WithPlatform gets: the
// receiver the adapters hand what they receive to, the bus and the logger
// of the profile.
type PlatformBuilder struct {
	// Receiver takes what the platform receives from its platform
	// (Code-ADR-0011, point 4).
	Receiver connector.Receiver
	// Publisher publishes the events the platform has of its own.
	Publisher *event.Bus
	// Logger is the logger of the profile.
	Logger *slog.Logger
}

// WithPlatform adds a platform the profile connects with, e.g. the mock
// platform of "streamcrew mock" (roadmap 3.6, plan §7.2). build is called
// with a PlatformBuilder during New, after the event service exists; it
// returns the platform, which the supervisor runs as long as the core
// runs. A platform whose event types the bus must know implements
// eventRegistrar.
func WithPlatform(build func(ctx context.Context, b PlatformBuilder) (connector.Platform, error)) Option {
	return func(o *options) { o.platform = build }
}

// eventRegistrar registers the event types of the platform it publishes,
// so that the bus of the core knows their payloads.
type eventRegistrar interface {
	RegisterEvents(c *event.Catalog) error
}

// platformSet is the set of the platforms of the profile. The composition
// root fills it after the platforms are built; the user lookup, the state
// of the stream, the requirement service and the actions read it while
// they run, never while the core builds.
type platformSet struct {
	mu  sync.RWMutex
	set *connector.Set
}

func newPlatformSet() *platformSet {
	set, err := connector.NewSet()
	if err != nil {
		panic(err) // impossible: a set without platforms
	}
	return &platformSet{set: set}
}

// fill replaces the set with ps.
func (p *platformSet) fill(ps ...connector.Platform) error {
	set, err := connector.NewSet(ps...)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.set = set
	p.mu.Unlock()
	return nil
}

// Platform returns the platform name; ok is false if the profile has none.
func (p *platformSet) Platform(name platform.Name) (connector.Platform, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.set.Platform(name)
}

// Connected returns the platforms that are connected now.
func (p *platformSet) Connected() []connector.Platform {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.set.Connected()
}

// userLookup is the lookup of the users the core knows, until the user
// service replaces it (roadmap 5.2): the users by their login name, and
// the streamer's and the bot's accounts of the platforms, which it stores
// when it sees them (spec users-and-roles.md, B4).
type userLookup struct {
	store *store.Store
	set   *platformSet
}

// UserByName implements template.Users and engine.Users (B81): the user
// the core knows by login name, and the streamer's and the bot's account
// with the login name, stored like a newly seen one.
func (l *userLookup) UserByName(ctx context.Context, p platform.Name, name string) (user.User, bool, error) {
	u, err := l.store.UserByLogin(ctx, p, name)
	switch {
	case err == nil:
		return u, true, nil
	case !errors.Is(err, store.ErrNotFound):
		return user.User{}, false, err
	}
	return l.accountByName(ctx, p, name)
}

// accountByName returns the streamer's or the bot's account with the
// login name on p, stored like a newly seen one.
func (l *userLookup) accountByName(ctx context.Context, p platform.Name, name string) (user.User, bool, error) {
	ident, ok := l.identityByLogin(ctx, p, name)
	if !ok {
		return user.User{}, false, nil
	}
	u, _, err := l.store.UpsertIdentity(ctx, ident)
	return u, err == nil, err
}

// identityByLogin returns the streamer's or the bot's account with the
// login name on p; ok is false if the platform does not report one.
func (l *userLookup) identityByLogin(_ context.Context, p platform.Name, name string) (user.Identity, bool) {
	pl, ok := l.set.Platform(p)
	if !ok {
		return user.Identity{}, false
	}
	idents, ok := pl.(connector.Identities)
	if !ok {
		return user.Identity{}, false
	}
	login := connector.Login(name)
	if login == "" {
		return user.Identity{}, false
	}
	for _, a := range []connector.Account{connector.AccountStreamer, connector.AccountBot} {
		ident, ok := idents.Identity(a)
		if ok && strings.EqualFold(ident.Login, login) {
			return ident, true
		}
	}
	return user.Identity{}, false
}

// Account implements template.Users: the streamer's or the bot's account
// on p, stored like a newly seen one.
func (l *userLookup) Account(ctx context.Context, p platform.Name, a template.Account) (user.User, bool, error) {
	pl, ok := l.set.Platform(p)
	if !ok {
		return user.User{}, false, nil
	}
	idents, ok := pl.(connector.Identities)
	if !ok {
		return user.User{}, false, nil
	}
	ident, ok := idents.Identity(connectorAccount(a))
	if !ok {
		return user.User{}, false, nil
	}
	u, _, err := l.store.UpsertIdentity(ctx, ident)
	return u, err == nil, err
}

// Chatters implements template.Users (B22): the users in the chat on p,
// stored like newly seen ones.
func (l *userLookup) Chatters(ctx context.Context, p platform.Name) ([]user.User, error) {
	pl, ok := l.set.Platform(p)
	if !ok {
		return nil, fmt.Errorf("chatters: %s: no platform", p)
	}
	ch, ok := pl.(connector.Chatters)
	if !ok {
		return nil, fmt.Errorf("chatters: %s: %w", p, connector.ErrNotConnected)
	}
	idents, err := ch.Chatters(ctx)
	if err != nil {
		return nil, fmt.Errorf("chatters: %s: %w", p, err)
	}
	out := make([]user.User, 0, len(idents))
	for _, ident := range idents {
		u, _, err := l.store.UpsertIdentity(ctx, ident)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// UserByPlatformID implements the Users port of the user action: the user
// with the account on p; ok is false if the core has not seen it.
func (l *userLookup) UserByPlatformID(ctx context.Context, p platform.Name, platformUserID string) (user.User, bool, error) {
	u, err := l.store.UserByIdentity(ctx, p, platformUserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return user.User{}, false, nil
		}
		return user.User{}, false, err
	}
	return u, true, nil
}

// UpsertIdentity implements the Users port of the user and the moderation
// action (users-and-roles.md, B4).
func (l *userLookup) UpsertIdentity(ctx context.Context, ident user.Identity) (user.User, bool, error) {
	return l.store.UpsertIdentity(ctx, ident)
}

// StreamerUser implements requirement.Streamer (requirements.md, B4): the
// user of the streamer's account on p, stored like a newly seen one; an
// error means it is not known.
func (l *userLookup) StreamerUser(ctx context.Context, p platform.Name) (id.ID, error) {
	pl, ok := l.set.Platform(p)
	if !ok {
		return id.ID{}, fmt.Errorf("streamer on %s: no platform", p)
	}
	idents, ok := pl.(connector.Identities)
	if !ok {
		return id.ID{}, fmt.Errorf("streamer on %s: no identities", p)
	}
	ident, ok := idents.Identity(connector.AccountStreamer)
	if !ok {
		return id.ID{}, fmt.Errorf("streamer on %s: no account", p)
	}
	u, _, err := l.store.UpsertIdentity(ctx, ident)
	if err != nil {
		return id.ID{}, err
	}
	return u.ID, nil
}

// connectorAccount returns the account of the platform the account of the
// template is.
func connectorAccount(a template.Account) connector.Account {
	if a == template.BotAccount {
		return connector.AccountBot
	}
	return connector.AccountStreamer
}

// streamStates reports the state of the stream from the channel of the
// platform (template.md, B43).
type streamStates struct {
	set *platformSet
}

// StreamState implements template.StreamStates.
func (s streamStates) StreamState(ctx context.Context, p platform.Name) (template.StreamState, error) {
	pl, ok := s.set.Platform(p)
	if !ok {
		return template.StreamState{}, fmt.Errorf("stream state: %s: no platform", p)
	}
	info, err := pl.Channel(ctx)
	if err != nil {
		return template.StreamState{}, fmt.Errorf("stream state: %s: %w", p, err)
	}
	return template.StreamState{
		Live:      info.Live,
		Title:     info.Title,
		Game:      info.Game,
		Viewers:   info.Viewers,
		Chatters:  info.Chatters,
		Followers: info.Followers,
		StartedAt: info.StartedAt,
	}, nil
}

// chatMute is the muted chat (actions.md, B85): while it is on, the chat
// service deletes every new chat message on the connected platforms
// (roadmap 5.1). It is not stored; a restart unmutes.
type chatMute struct {
	mu     sync.Mutex
	muted  bool
	logger *slog.Logger
}

func newChatMute(logger *slog.Logger) *chatMute {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &chatMute{logger: logger}
}

// Mute implements moderation.ChatMute.
func (m *chatMute) Mute(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.muted {
		return
	}
	m.muted = true
	m.logger.InfoContext(ctx, "chat muted")
}

// Unmute implements moderation.ChatMute.
func (m *chatMute) Unmute(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.muted {
		return
	}
	m.muted = false
	m.logger.InfoContext(ctx, "chat unmuted")
}

// Muted reports whether the chat is muted now.
func (m *chatMute) Muted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.muted
}

// errSwitchesNotSet is returned by lateSwitches before the command service
// is set, which the composition root does once during New.
var errSwitchesNotSet = errors.New("the command service is not set")

// lateSwitches is the Switches port of the command action: it resolves the
// command service once it is built, because the registry that decodes the
// commands is built with the port first (Code-ADR-0013, point 3).
type lateSwitches struct {
	mu sync.RWMutex
	s  *command.Service
}

// set names the command service.
func (l *lateSwitches) set(s *command.Service) {
	l.mu.Lock()
	l.s = s
	l.mu.Unlock()
}

// SwitchCommand implements commands.Switches.
func (l *lateSwitches) SwitchCommand(ctx context.Context, commandID id.ID, sw command.Switch) (command.Command, error) {
	l.mu.RLock()
	s := l.s
	l.mu.RUnlock()
	if s == nil {
		return command.Command{}, fmt.Errorf("switch command: %w", errSwitchesNotSet)
	}
	return s.SwitchCommand(ctx, commandID, sw)
}

// SwitchGroup implements commands.Switches.
func (l *lateSwitches) SwitchGroup(ctx context.Context, groupID id.ID, sw command.Switch) error {
	l.mu.RLock()
	s := l.s
	l.mu.RUnlock()
	if s == nil {
		return fmt.Errorf("switch group: %w", errSwitchesNotSet)
	}
	return s.SwitchGroup(ctx, groupID, sw)
}

// engineConfig reads the settings the engine reads for every instance: the
// sections "commands" and "locale", and the time zone of the profile
// (command-engine.md, B28, B90; Code-ADR-0009; spec template.md, B41).
func (a *App) engineConfig(ctx context.Context) (engine.Config, error) {
	c, err := settings.Load(ctx, a.settings, settings.DefaultCommands())
	if err != nil {
		return engine.Config{}, fmt.Errorf("settings commands: %w", err)
	}
	t, err := settings.Load(ctx, a.settings, settings.DefaultTime())
	if err != nil {
		return engine.Config{}, fmt.Errorf("settings time: %w", err)
	}
	loc, err := t.Location()
	if err != nil {
		// A stored zone this build cannot load: UTC instead of refusing
		// the instance (Code-ADR-0009).
		a.logger.WarnContext(ctx, "invalid profile time zone", "error", err)
		loc = time.UTC
	}
	l, err := settings.Load(ctx, a.settings, settings.DefaultLocale())
	if err != nil {
		return engine.Config{}, fmt.Errorf("settings locale: %w", err)
	}
	return engine.Config{Commands: c, Location: loc, Locale: a.formatLocale(ctx, l.Locale)}, nil
}

// formatLocale resolves the locale of the section to a locale of the core
// (B41): a name of the list as is, and LocaleSystem as the locale of the
// environment, with a warning for an environment locale outside the list.
func (a *App) formatLocale(ctx context.Context, name string) template.Locale {
	if name != settings.LocaleSystem {
		l, _ := template.ResolveLocale(name)
		return l
	}
	if a.systemLocale == "" {
		return template.LocaleUSEnglish
	}
	l, known := template.ResolveLocale(a.systemLocale)
	if !known {
		a.logger.WarnContext(ctx, "the locale of the environment is outside the list", "locale", a.systemLocale, "using", string(l))
	}
	return l
}

// localeLanguage is the language of the profile: it reads the section
// "locale" (ADR-0022, point 7).
type localeLanguage struct {
	settings *settings.Service
}

// Language implements requirement.Language.
func (l localeLanguage) Language(ctx context.Context) (i18n.Language, error) {
	sec, err := settings.Load(ctx, l.settings, settings.DefaultLocale())
	if err != nil {
		return "", fmt.Errorf("settings locale: %w", err)
	}
	return sec.Language, nil
}

// eventsSettings reads the section "events" (events.md, B10).
func (a *App) eventsSettings(ctx context.Context) (settings.Events, error) {
	return settings.Load(ctx, a.settings, settings.DefaultEvents())
}

// channelPointsSettings reads the section "channelPoints" (roadmap 4.4).
func (a *App) channelPointsSettings(ctx context.Context) (settings.ChannelPoints, error) {
	return settings.Load(ctx, a.settings, settings.DefaultChannelPoints())
}
