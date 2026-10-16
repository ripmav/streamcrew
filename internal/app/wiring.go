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

// ConnectedNames returns the names of the platforms that are connected now.
func (p *platformSet) ConnectedNames() []platform.Name {
	conns := p.Connected()
	names := make([]platform.Name, 0, len(conns))
	for _, pl := range conns {
		names = append(names, pl.Name())
	}
	return names
}

// All returns the platforms of the profile, in the order of the set.
func (p *platformSet) All() []connector.Platform {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.set.All()
}

// userStore adapts *store.Store to the Store port of the user service:
// the store reports a missing user with an error, the service wants ok.
type userStore struct{ *store.Store }

// UserByLogin implements the Store port of the user service.
func (a userStore) UserByLogin(ctx context.Context, p platform.Name, login string) (user.User, bool, error) {
	u, err := a.Store.UserByLogin(ctx, p, login)
	if errors.Is(err, store.ErrNotFound) {
		return user.User{}, false, nil
	}
	return u, err == nil, err
}

// UserByPlatformID implements the Store port of the user service.
func (a userStore) UserByPlatformID(ctx context.Context, p platform.Name, platformUserID string) (user.User, bool, error) {
	u, err := a.UserByIdentity(ctx, p, platformUserID)
	if errors.Is(err, store.ErrNotFound) {
		return user.User{}, false, nil
	}
	return u, err == nil, err
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

// lateCommands is the Commands port of the command-admin action: it resolves
// the command service once it is built, like lateSwitches.
type lateCommands struct {
	mu sync.RWMutex
	s  *command.Service
}

// set names the command service.
func (l *lateCommands) set(s *command.Service) {
	l.mu.Lock()
	l.s = s
	l.mu.Unlock()
}

func (l *lateCommands) service() (*command.Service, error) {
	l.mu.RLock()
	s := l.s
	l.mu.RUnlock()
	if s == nil {
		return nil, errSwitchesNotSet
	}
	return s, nil
}

// Commands implements commandadmin.Commands.
func (l *lateCommands) Commands(ctx context.Context) ([]command.Command, error) {
	s, err := l.service()
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	return s.Commands(ctx)
}

// Save implements commandadmin.Commands.
func (l *lateCommands) Save(ctx context.Context, cmd command.Command) (command.Saved, error) {
	s, err := l.service()
	if err != nil {
		return command.Saved{}, fmt.Errorf("save command: %w", err)
	}
	return s.Save(ctx, cmd)
}

// SwitchCommand implements commandadmin.Commands.
func (l *lateCommands) SwitchCommand(ctx context.Context, commandID id.ID, sw command.Switch) (command.Command, error) {
	s, err := l.service()
	if err != nil {
		return command.Command{}, fmt.Errorf("switch command: %w", err)
	}
	return s.SwitchCommand(ctx, commandID, sw)
}

// timerPlatforms adapts *platformSet to the Platforms port of the timer:
// it names the connected platforms.
type timerPlatforms struct{ set *platformSet }

// Connected implements the Platforms port of the timer.
func (t timerPlatforms) Connected() []platform.Name { return t.set.ConnectedNames() }

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
	// The title rules and the "no title" text of the templates
	// (users-and-roles.md, B5); the text is rendered in the language of the
	// profile.
	u, err := settings.Load(ctx, a.settings, settings.DefaultUsers())
	if err != nil {
		return engine.Config{}, fmt.Errorf("settings users: %w", err)
	}
	noTitle, err := a.messages.Render(l.Language, i18n.Message{Key: i18n.KeyNoTitle})
	if err != nil {
		return engine.Config{}, fmt.Errorf("render no title: %w", err)
	}
	return engine.Config{
		Commands:   c,
		Location:   loc,
		Locale:     a.formatLocale(ctx, l.Locale),
		TitleRules: u.TitleRules,
		NoTitle:    noTitle,
	}, nil
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

// chatSettings reads the section "chat" (spec chat-service.md, B15, B17, B30).
func (a *App) chatSettings(ctx context.Context) (settings.Chat, error) {
	return settings.Load(ctx, a.settings, settings.DefaultChat())
}

// generalSettings reads the section "general" (spec chat-service.md, B26).
func (a *App) generalSettings(ctx context.Context) (settings.General, error) {
	return settings.Load(ctx, a.settings, settings.DefaultGeneral())
}

// channelPointsSettings reads the section "channelPoints" (roadmap 4.4).
func (a *App) channelPointsSettings(ctx context.Context) (settings.ChannelPoints, error) {
	return settings.Load(ctx, a.settings, settings.DefaultChannelPoints())
}

// usersSettings reads the section "users" (roadmap 5.2.2, users-and-roles.md, B26).
func (a *App) usersSettings(ctx context.Context) (settings.Users, error) {
	return settings.Load(ctx, a.settings, settings.DefaultUsers())
}

// timersSettings reads the section "timers" (roadmap 5.4, commands.md, B21, B31).
func (a *App) timersSettings(ctx context.Context) (settings.Timers, error) {
	return settings.Load(ctx, a.settings, settings.DefaultTimers())
}

// moderationSettings reads the section "moderation" (roadmap 5.5, spec
// moderation.md, B13).
func (a *App) moderationSettings(ctx context.Context) (settings.Moderation, error) {
	return settings.Load(ctx, a.settings, settings.DefaultModeration())
}
