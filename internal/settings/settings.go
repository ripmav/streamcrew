// SPDX-License-Identifier: MIT

// Package settings holds the runtime settings of a profile as typed,
// versioned sections (roadmap phase 2.2). Each section is a polymorphic
// document (Code-ADR-0010) in the settings table of the profile database;
// a section that was never saved has its defaults.
//
// There are the sections "backups" and "time" (roadmap 2.2), "commands"
// (roadmap 3.2), "locale" (roadmap 3.4, ADR-0022), "events" (roadmap 3.6),
// "channelPoints" (roadmap 4.4), "channels" (roadmap 4.6), and "general"
// and "chat" (roadmap 5.1); "moderation" and "overlay" follow with their
// features.
package settings

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/template"
)

// Section is a settings section. Its DocType is the section name.
type Section interface {
	polydoc.Document
	validate() error
}

// Repository stores section documents; *store.Store implements it.
type Repository interface {
	Settings(ctx context.Context, section string) (doc []byte, found bool, err error)
	PutSettings(ctx context.Context, section string, doc []byte) error
}

// Service loads and saves sections.
type Service struct {
	repo     Repository
	registry *polydoc.Registry[Section]
}

// New returns a service with all known sections registered.
func New(repo Repository) (*Service, error) {
	r := polydoc.NewRegistry("settings", func(u polydoc.Unknown) Section { return unknown{u} })
	for _, e := range []polydoc.Entry[Section]{
		{Type: sectionBackups, Version: 1, Decode: decode[Backups]},
		{Type: sectionTime, Version: 2, Decode: decode[Time], Migrations: []polydoc.Migration{migrateTimeV1}},
		{Type: sectionCommands, Version: 4, Decode: decode[Commands], Migrations: []polydoc.Migration{migrateCommandsV1, migrateCommandsV2, migrateCommandsV3}},
		{Type: sectionLocale, Version: 2, Decode: decode[Locale], Migrations: []polydoc.Migration{migrateLocaleV1}},
		{Type: sectionEvents, Version: 1, Decode: decode[Events]},
		{Type: sectionChannelPoints, Version: 1, Decode: decode[ChannelPoints]},
		{Type: sectionChannels, Version: 2, Decode: decode[Channels], Migrations: []polydoc.Migration{migrateChannelsV1}},
		{Type: sectionGeneral, Version: 1, Decode: decode[General]},
		{Type: sectionChat, Version: 1, Decode: decode[Chat]},
		{Type: sectionUsers, Version: 1, Decode: decode[Users]},
		{Type: sectionTimers, Version: 1, Decode: decode[Timers]},
		{Type: sectionModeration, Version: 1, Decode: decode[Moderation]},
	} {
		if err := r.Register(e); err != nil {
			return nil, err
		}
	}
	return &Service{repo: repo, registry: r}, nil
}

func decode[S Section](data []byte, opts json.Options) (Section, error) {
	s, err := polydoc.Strict[S](data, opts)
	if err != nil {
		return nil, err
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Load returns a section, or its defaults if it was never saved.
func Load[S Section](ctx context.Context, svc *Service, defaults S) (S, error) {
	doc, found, err := svc.repo.Settings(ctx, defaults.DocType())
	if err != nil || !found {
		return defaults, err
	}
	v, err := svc.registry.Decode(doc)
	if err != nil {
		return defaults, fmt.Errorf("settings %q: %w", defaults.DocType(), err)
	}
	s, ok := v.(S)
	if !ok {
		return defaults, fmt.Errorf("settings %q: stored document cannot be read by this version", defaults.DocType())
	}
	return s, nil
}

// Save validates and stores a section.
func Save(ctx context.Context, svc *Service, s Section) error {
	if err := s.validate(); err != nil {
		return fmt.Errorf("settings %q: %w", s.DocType(), err)
	}
	doc, err := svc.registry.Encode(s)
	if err != nil {
		return err
	}
	return svc.repo.PutSettings(ctx, s.DocType(), doc)
}

// Sections returns the names of the known settings sections.
func (s *Service) Sections() []string {
	return s.registry.Types()
}

// Document returns the stored document of the section; found is false if the
// section was never saved, so its defaults apply (roadmap 6.2.1, API).
func (s *Service) Document(ctx context.Context, section string) (doc []byte, found bool, err error) {
	return s.repo.Settings(ctx, section)
}

// DefaultDocument returns the encoded defaults of the section.
func (s *Service) DefaultDocument(section string) ([]byte, error) {
	d, ok := defaultSection(section)
	if !ok {
		return nil, fmt.Errorf("%w: unknown settings section %q", ErrInvalid, section)
	}
	return s.registry.Encode(d)
}

// ErrInvalid is wrapped when a settings document cannot be applied: a
// decode failure, a document type that does not match the section, or an
// unknown section.
var ErrInvalid = errors.New("invalid settings document")

// SetDocument validates doc against the section and stores it (roadmap
// 6.2.1, API). The document must name the section as its type.
func (s *Service) SetDocument(ctx context.Context, section string, doc []byte) error {
	v, err := s.registry.Decode(doc)
	if err != nil {
		return fmt.Errorf("%w: settings %q: %w", ErrInvalid, section, err)
	}
	if v.DocType() != section {
		return fmt.Errorf("%w: settings %q: the document has type %q", ErrInvalid, section, v.DocType())
	}
	return s.repo.PutSettings(ctx, section, doc)
}

// defaultSection returns the defaults of the section, computed on the call so
// the time defaults follow the system time zone.
func defaultSection(section string) (Section, bool) {
	switch section {
	case sectionBackups:
		return DefaultBackups(), true
	case sectionTime:
		return DefaultTime(), true
	case sectionCommands:
		return DefaultCommands(), true
	case sectionLocale:
		return DefaultLocale(), true
	case sectionEvents:
		return DefaultEvents(), true
	case sectionChannelPoints:
		return DefaultChannelPoints(), true
	case sectionChannels:
		return DefaultChannels(), true
	case sectionGeneral:
		return DefaultGeneral(), true
	case sectionChat:
		return DefaultChat(), true
	case sectionUsers:
		return DefaultUsers(), true
	case sectionTimers:
		return DefaultTimers(), true
	case sectionModeration:
		return DefaultModeration(), true
	}
	return nil, false
}

// unknown keeps a section this version cannot read.
type unknown struct{ polydoc.Unknown }

func (u unknown) DocType() string         { return u.Type }
func (u unknown) RawJSON() jsontext.Value { return u.Raw }
func (unknown) validate() error           { return errors.New("unknown settings section") }

const (
	sectionBackups       = "backups"
	sectionTime          = "time"
	sectionCommands      = "commands"
	sectionLocale        = "locale"
	sectionEvents        = "events"
	sectionChannelPoints = "channelPoints"
	sectionChannels      = "channels"
	sectionGeneral       = "general"
	sectionChat          = "chat"
	sectionUsers         = "users"
	sectionTimers        = "timers"
	sectionModeration    = "moderation"
)

// Limits of the section "events" (spec events.md, B5, B8, B10).
const (
	// DefaultMassGiftThreshold is the threshold of a profile that never set
	// one.
	DefaultMassGiftThreshold = 2
	// MinMassGiftThreshold and MaxMassGiftThreshold limit the threshold.
	MinMassGiftThreshold = 2
	MaxMassGiftThreshold = 1000
	// DefaultStreamGracePeriod is the grace period of a profile that never
	// set one.
	DefaultStreamGracePeriod = 10 * time.Minute
	// MaxStreamGracePeriod is the longest grace period; 0 is the shortest,
	// a stream session without grace period.
	MaxStreamGracePeriod = time.Hour
)

// Events configures the event service (spec events.md, B10).
type Events struct {
	// MassGiftThreshold is the number of gifts of a mass gift from which
	// only the mass gift fires, and below which only the single gifts do
	// (B5), from MinMassGiftThreshold to MaxMassGiftThreshold. A change
	// applies from the next mass gift.
	MassGiftThreshold int `json:"massGiftThreshold"`
	// StreamGracePeriod is how long a stream may stay offline without
	// ending its session (B8), from 0 to MaxStreamGracePeriod. A change
	// applies from the next time the stream goes offline.
	StreamGracePeriod polydoc.Duration `json:"streamGracePeriod"`
}

// DefaultEvents returns the defaults of B10.
func DefaultEvents() Events {
	return Events{
		MassGiftThreshold: DefaultMassGiftThreshold,
		StreamGracePeriod: polydoc.Duration(DefaultStreamGracePeriod),
	}
}

// DocType implements polydoc.Document.
func (Events) DocType() string { return sectionEvents }

// Validate checks the section, e.g. before the event service uses it.
func (e Events) Validate() error {
	return e.validate()
}

func (e Events) validate() error {
	if e.MassGiftThreshold < MinMassGiftThreshold || e.MassGiftThreshold > MaxMassGiftThreshold {
		return fmt.Errorf("mass gift threshold %d is not between %d and %d", e.MassGiftThreshold, MinMassGiftThreshold, MaxMassGiftThreshold)
	}
	if grace := e.StreamGracePeriod.Std(); grace < 0 || grace > MaxStreamGracePeriod {
		return fmt.Errorf("stream grace period %s is not between 0 and %s", grace, MaxStreamGracePeriod)
	}
	return nil
}

// ChannelPoints maps the channel points rewards of the channel to the
// commands that run when they are redeemed (roadmap 4.4). A reward
// without an entry is left to the platform.
type ChannelPoints struct {
	// Rewards are the mappings, in the order they were added.
	Rewards []ChannelPointReward `json:"rewards"`
}

// ChannelPointReward maps one reward to one command.
type ChannelPointReward struct {
	// RewardID is the ID of the reward on the platform; not empty.
	RewardID string `json:"reward"`
	// Command is the name of the command that runs on a redemption; not
	// empty.
	Command string `json:"command"`
}

// DefaultChannelPoints returns the default of the section: no mapping.
func DefaultChannelPoints() ChannelPoints {
	return ChannelPoints{}
}

// DocType implements polydoc.Document.
func (ChannelPoints) DocType() string { return sectionChannelPoints }

// Validate checks the section.
func (c ChannelPoints) Validate() error {
	return c.validate()
}

func (c ChannelPoints) validate() error {
	rewards := make(map[string]string, len(c.Rewards))
	for _, r := range c.Rewards {
		if r.RewardID == "" {
			return errors.New("a channel points reward without an ID")
		}
		if r.Command == "" {
			return fmt.Errorf("the channel points reward %s has no command", r.RewardID)
		}
		if other, ok := rewards[r.RewardID]; ok {
			return fmt.Errorf("the channel points reward %s maps to both %q and %q", r.RewardID, other, r.Command)
		}
		rewards[r.RewardID] = r.Command
	}
	return nil
}

// General holds the profile-wide settings without a more specific section
// (spec chat-service.md, B26).
type General struct {
	// DefaultPlatform is the platform the rules use where the run names
	// none (B26, B27); a change applies from the next read.
	DefaultPlatform platform.Name `json:"defaultPlatform"`
}

// DefaultGeneral returns the defaults of B26: the platform of ADR-0004.
func DefaultGeneral() General {
	return General{DefaultPlatform: platform.Default}
}

// DocType implements polydoc.Document.
func (General) DocType() string { return sectionGeneral }

// Validate checks the section.
func (g General) Validate() error { return g.validate() }

func (g General) validate() error { return g.DefaultPlatform.Validate() }

// Limits of the section "chat" (spec chat-service.md, B15).
const (
	// DefaultChatHistorySize is the history size of a profile that never
	// set one, like the command history of the engine.
	DefaultChatHistorySize = 200
	// MinChatHistorySize and MaxChatHistorySize limit the size.
	MinChatHistorySize = 1
	MaxChatHistorySize = 10_000
)

// Chat configures the chat service (spec chat-service.md, B15, B17, B30).
type Chat struct {
	// HistorySize is how many messages the history keeps per platform
	// (B15); a change applies from the next save.
	HistorySize int `json:"historySize"`
	// Protocol switches the chat protocol on disk on (B17).
	Protocol bool `json:"protocol"`
	// DeleteTriggerMessage deletes the triggering message of every chat
	// command, in addition to (OR) the switch of the command (B30).
	DeleteTriggerMessage bool `json:"deleteTriggerMessage"`
}

// DefaultChat returns the defaults of B15, B17 and B30.
func DefaultChat() Chat {
	return Chat{HistorySize: DefaultChatHistorySize}
}

// DocType implements polydoc.Document.
func (Chat) DocType() string { return sectionChat }

// Validate checks the section.
func (c Chat) Validate() error { return c.validate() }

func (c Chat) validate() error {
	if c.HistorySize < MinChatHistorySize || c.HistorySize > MaxChatHistorySize {
		return fmt.Errorf("history size %d is not between %d and %d", c.HistorySize, MinChatHistorySize, MaxChatHistorySize)
	}
	return nil
}

// Users configures the user service (roadmap 5.2). The section holds the
// knobs the user service reads on its watch tick, so a change applies from
// the next tick, and the title rules the templates apply at once.
type Users struct {
	// RegularHours is the whole hours of watch time a user needs for the
	// regular role (users-and-roles.md, B26); 0 turns the role off.
	RegularHours int `json:"regularHours"`
	// TitleRules are the streamer's title rules (users-and-roles.md, B5),
	// in the order the streamer set them; empty for none. A document
	// without the key decodes with the empty list, so the section stayed
	// at version 1.
	TitleRules []user.TitleRule `json:"titleRules"`
}

// DefaultUsers returns the defaults: no regular role and no title rules
// (users-and-roles.md, B5, B26).
func DefaultUsers() Users {
	return Users{}
}

// DocType implements polydoc.Document.
func (Users) DocType() string { return sectionUsers }

// Validate checks the section.
func (u Users) Validate() error { return u.validate() }

func (u Users) validate() error {
	if u.RegularHours < 0 {
		return fmt.Errorf("regular hours %d must be 0 or more", u.RegularHours)
	}
	for i, r := range u.TitleRules {
		if r.Name == "" {
			return fmt.Errorf("title rule %d: empty title", i)
		}
		if strings.IndexFunc(r.Name, unicode.IsControl) >= 0 {
			return fmt.Errorf("title rule %d: the title contains a control character", i)
		}
		if !r.MinRole.Valid() {
			return fmt.Errorf("title rule %d: unknown role %q", i, r.MinRole)
		}
		if r.MinMonths < 0 {
			return fmt.Errorf("title rule %d: months %d must be 0 or more", i, r.MinMonths)
		}
	}
	return nil
}

// TimerOrder is the order the timer runs its commands in one tick (roadmap
// 5.4).
type TimerOrder string

// Orders of a timer tick (roadmap 5.4).
const (
	// OrderFixed runs the timer commands in their stored order (by ID).
	OrderFixed TimerOrder = "fixed"
	// OrderRandom runs them in a new random order every tick.
	OrderRandom TimerOrder = "random"
)

// Valid reports whether o is a known order.
func (o TimerOrder) Valid() bool {
	switch o {
	case OrderFixed, OrderRandom:
		return true
	default:
		return false
	}
}

// Timers is the global timer schedule of timer commands (roadmap 5.4,
// spec commands.md, B21, B31). It applies to timer commands that have no
// group with its own interval; such a group runs on its own schedule (B31).
type Timers struct {
	// Interval is how often the global timer fires; 0 turns the timer off.
	Interval polydoc.Duration `json:"interval"`
	// MinChatMessages is how many chat messages a platform needs in the
	// last Interval before the timer fires there; 0 for no check.
	MinChatMessages int `json:"minChatMessages"`
	// LiveOnly keeps the timer from firing while a platform's stream is
	// not live.
	LiveOnly bool `json:"liveOnly"`
	// Order is the order of the commands in one tick (OrderFixed or
	// OrderRandom).
	Order TimerOrder `json:"order"`
}

// DefaultTimers returns the defaults: the timer is off (roadmap 5.4).
func DefaultTimers() Timers {
	return Timers{Order: OrderFixed}
}

// DocType implements polydoc.Document.
func (Timers) DocType() string { return sectionTimers }

// Validate checks the section.
func (t Timers) Validate() error { return t.validate() }

func (t Timers) validate() error {
	if t.Interval.Std() < 0 {
		return fmt.Errorf("timer interval %s must be 0 or more", t.Interval.Std())
	}
	if t.MinChatMessages < 0 {
		return fmt.Errorf("timer min chat messages %d must be 0 or more", t.MinChatMessages)
	}
	if !t.Order.Valid() {
		return fmt.Errorf("timer order %q is unknown", t.Order)
	}
	return nil
}

// FilterKind is what a moderation filter checks (spec moderation.md, B4).
type FilterKind string

// Kinds of a moderation filter (spec moderation.md, B4).
const (
	// FilterWord is a banned word or pattern with wildcards (B5).
	FilterWord FilterKind = "word"
	// FilterLink is a link in the message (B6).
	FilterLink FilterKind = "link"
	// FilterUppercase is the amount of uppercase (B7).
	FilterUppercase FilterKind = "uppercase"
	// FilterPunctuation is the amount of punctuation (B7).
	FilterPunctuation FilterKind = "punctuation"
	// FilterEmote is the amount of emotes (B7).
	FilterEmote FilterKind = "emote"
)

// Valid reports whether k is a known kind.
func (k FilterKind) Valid() bool {
	switch k {
	case FilterWord, FilterLink, FilterUppercase, FilterPunctuation, FilterEmote:
		return true
	default:
		return false
	}
}

// Filter is one rule the moderation checks a message against (spec
// moderation.md, B4 to B7). A threshold filter (uppercase, punctuation,
// emote) is off with a threshold of 0 (B7).
type Filter struct {
	// Kind is what the filter checks.
	Kind FilterKind `json:"kind"`
	// Word is the banned word or pattern for FilterWord; `*` at the
	// start, the end or both is a wildcard (B5).
	Word string `json:"word"`
	// Threshold is the limit of a threshold filter (B7).
	Threshold int `json:"threshold"`
	// Percent makes Threshold a percentage of the letters or words instead
	// of a count (B7).
	Percent bool `json:"percent"`
	// Exempt are the roles that are exempt from the filter (B4, B22).
	Exempt []role.Role `json:"exempt"`
}

// StrikeLevel assigns the follow-up command to a strike level (spec
// moderation.md, B10). The levels run from 1; a level without a command
// raises the counter only (B10, B25).
type StrikeLevel struct {
	// Level is the strike count the level is for, from 1.
	Level int `json:"level"`
	// Command is the name of the follow-up command; empty for none.
	Command string `json:"command"`
}

// Moderation is the settings of the chat moderation stage (spec
// moderation.md, B3, B4, B10, B13): the participation rules, the filters
// and the strike levels.
type Moderation struct {
	// MinFollowDays is the whole days a user must have followed before
	// chatting (B3); 0 for no requirement.
	MinFollowDays int `json:"minFollowDays"`
	// MinWatchMinutes is the whole minutes of watch time a user needs
	// before chatting (B3); 0 for no requirement.
	MinWatchMinutes int `json:"minWatchMinutes"`
	// MinRole is the role a user needs before chatting (B3); empty for no
	// requirement.
	MinRole role.Role `json:"minRole"`
	// Filters are the rules, in the order they are checked; the first
	// violated one counts (B8, B13).
	Filters []Filter `json:"filters"`
	// StrikeLevels are the follow-up commands per strike count (B10, B13).
	StrikeLevels []StrikeLevel `json:"strikeLevels"`
}

// DefaultModeration returns the defaults: no participation rules, no
// filters, no strike levels — every message runs through (spec
// moderation.md, B3, B20, B24).
func DefaultModeration() Moderation {
	return Moderation{}
}

// DocType implements polydoc.Document.
func (Moderation) DocType() string { return sectionModeration }

// Validate checks the section.
func (m Moderation) Validate() error { return m.validate() }

func (m Moderation) validate() error {
	if m.MinFollowDays < 0 {
		return fmt.Errorf("min follow days %d must be 0 or more", m.MinFollowDays)
	}
	if m.MinWatchMinutes < 0 {
		return fmt.Errorf("min watch minutes %d must be 0 or more", m.MinWatchMinutes)
	}
	if m.MinRole != "" && !m.MinRole.Valid() {
		return fmt.Errorf("min role %q is unknown", m.MinRole)
	}
	for i, f := range m.Filters {
		if err := f.validate(); err != nil {
			return fmt.Errorf("filter %d: %w", i, err)
		}
	}
	seen := make(map[int]struct{}, len(m.StrikeLevels))
	for i, l := range m.StrikeLevels {
		if l.Level < 1 {
			return fmt.Errorf("strike level %d: %d must be 1 or more", i, l.Level)
		}
		if _, dup := seen[l.Level]; dup {
			return fmt.Errorf("strike level %d: level %d is set twice", i, l.Level)
		}
		seen[l.Level] = struct{}{}
	}
	return nil
}

func (f Filter) validate() error {
	if !f.Kind.Valid() {
		return fmt.Errorf("kind %q is unknown", f.Kind)
	}
	switch f.Kind {
	case FilterWord:
		if f.Word == "" {
			return errors.New("empty word")
		}
	case FilterUppercase, FilterPunctuation, FilterEmote:
		if f.Threshold < 0 {
			return fmt.Errorf("threshold %d must be 0 or more", f.Threshold)
		}
		if f.Percent && (f.Threshold < 0 || f.Threshold > 100) {
			return fmt.Errorf("threshold %d is not between 0 and 100", f.Threshold)
		}
	default:
		// FilterLink has no further fields.
	}
	for _, r := range f.Exempt {
		if !r.Valid() {
			return fmt.Errorf("exempt role %q is unknown", r)
		}
	}
	return nil
}

// Channels configures the channels the bot operates in besides the home
// channel (spec twitch-multi-channel.md, B1, roadmap 4.6). The home
// channel — the channel of the streamer account — is operated implicitly
// and must not be listed; an entry that names it is not an additional
// channel, so the adapter leaves it out with a warning (B3).
type Channels struct {
	// Additional is the list of the logins of the additional channels,
	// in the order they were added; empty for none.
	Additional []string `json:"additional"`
}

// DefaultChannels returns the default of the section: no additional
// channel, only the home channel is operated.
func DefaultChannels() Channels {
	return Channels{}
}

// DocType implements polydoc.Document.
func (Channels) DocType() string { return sectionChannels }

// Validate checks the section: no empty entries and no duplicates; the
// logins compare case-insensitively, as on the platform. Each login has
// the shape of a platform login: 1 to 25 letters, digits, or underscores.
func (c Channels) Validate() error { return c.validate() }

func (c Channels) validate() error {
	seen := make(map[string]bool, len(c.Additional))
	for _, login := range c.Additional {
		if err := validLogin(login); err != nil {
			return err
		}
		key := strings.ToLower(login)
		if seen[key] {
			return fmt.Errorf("the channel %q is listed more than once", login)
		}
		seen[key] = true
	}
	return nil
}

// validLogin checks the shape of a login name: 1 to 25 characters of
// letters, digits, or underscores (as the platform allows).
func validLogin(login string) error {
	if login == "" || len(login) > 25 {
		return fmt.Errorf("the channel login %q: want 1 to 25 characters", login)
	}
	for _, r := range login {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return fmt.Errorf("the channel login %q: only letters, digits, and underscores are allowed", login)
		}
	}
	return nil
}

// Contains reports whether login is one of the additional channels; the
// logins compare case-insensitively, as on the platform.
func (c Channels) Contains(login string) bool {
	for _, l := range c.Additional {
		if strings.EqualFold(l, login) {
			return true
		}
	}
	return false
}

// migrateChannelsV1 drops the chat adapter of version 1: the adapters
// are the start configuration (--twitch-eventsub, --twitch-irc, plan
// 2026-10-22), not a setting of the profile.
func migrateChannelsV1(doc map[string]jsontext.Value) error {
	delete(doc, "chatAdapter")
	return nil
}

// LocaleSystem names the locale of the environment the core runs in, as
// TimeZoneSystem names the time zone of the system (B41).
const LocaleSystem = "system"

// Locale holds the language of the profile (ADR-0022, point 7) and the
// locale of the formats of dates and times in the templates (B41). Version
// 1 of the section had no format locale.
type Locale struct {
	// Language is a language with a catalog, e.g. "de".
	Language i18n.Language `json:"language"`
	// Locale is the locale of the formats of dates and times: LocaleSystem
	// or a locale the core knows (B41).
	Locale string `json:"locale"`
}

// DefaultLocale returns English, the source language of the catalogs, and
// the locale of the environment.
func DefaultLocale() Locale {
	return Locale{Language: i18n.English, Locale: LocaleSystem}
}

// DocType implements polydoc.Document.
func (Locale) DocType() string { return sectionLocale }

// migrateLocaleV1 adds the format locale of version 2 with its default.
func migrateLocaleV1(doc map[string]jsontext.Value) error {
	if _, ok := doc["locale"]; ok {
		return errors.New("format locale in version 1")
	}
	v, err := json.Marshal(LocaleSystem)
	if err != nil {
		return err
	}
	doc["locale"] = v
	return nil
}

func (l Locale) validate() error {
	if !l.Language.Valid() {
		return fmt.Errorf("language %q has no catalog; known are %v", l.Language, i18n.Languages())
	}
	if l.Locale != LocaleSystem && !template.KnownLocale(l.Locale) {
		return fmt.Errorf("locale %q is unknown; known are system and %v", l.Locale, template.Locales())
	}
	return nil
}

// Backups configures the automatic backups (ADR-0012).
type Backups struct {
	// Enabled switches the daily backup on.
	Enabled bool `json:"enabled"`
	// At is the time of day in the profile's time zone, "15:04".
	At string `json:"at"`
	// KeepDaily, KeepWeekly and KeepMonthly are the numbers of days, weeks
	// and months for which the newest automatic backup is kept.
	KeepDaily   int `json:"keepDaily"`
	KeepWeekly  int `json:"keepWeekly"`
	KeepMonthly int `json:"keepMonthly"`
}

// DefaultBackups returns the defaults of ADR-0012.
func DefaultBackups() Backups {
	return Backups{Enabled: true, At: "04:00", KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12}
}

// DocType implements polydoc.Document.
func (Backups) DocType() string { return sectionBackups }

// Clock parses At.
func (b Backups) Clock() (hour, minute int, err error) {
	t, err := time.Parse("15:04", b.At)
	if err != nil {
		return 0, 0, fmt.Errorf("backup time %q: want HH:MM", b.At)
	}
	return t.Hour(), t.Minute(), nil
}

func (b Backups) validate() error {
	if _, _, err := b.Clock(); err != nil {
		return err
	}
	if b.KeepDaily < 0 || b.KeepWeekly < 0 || b.KeepMonthly < 0 {
		return errors.New("backup retention must not be negative")
	}
	return nil
}

// TimeZoneSystem names the time zone of the system at run time (Code-ADR-0009,
// as decided by the project owner on 2026-09-29 and 2026-09-30).
const TimeZoneSystem = "system"

// Time holds the time zone of the profile (Code-ADR-0009): TimeZoneSystem or
// an IANA name. UTC is the fallback when neither can be used. Version 1 of
// the section wrote an empty name for the system zone; version 2 writes
// TimeZoneSystem (Code-ADR-0017).
type Time struct {
	// TimeZone is TimeZoneSystem or an IANA name such as "Europe/Berlin".
	TimeZone string `json:"timeZone"`
}

// DefaultTime returns the system time zone.
func DefaultTime() Time {
	return Time{TimeZone: TimeZoneSystem}
}

// migrateTimeV1 turns the empty name of version 1 into TimeZoneSystem.
func migrateTimeV1(doc map[string]jsontext.Value) error {
	raw, ok := doc["timeZone"]
	if !ok {
		return errors.New("time zone missing")
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return fmt.Errorf("time zone: %w", err)
	}
	if name != "" {
		return nil
	}
	system, err := jsontext.AppendQuote(nil, TimeZoneSystem)
	if err != nil {
		return err
	}
	doc["timeZone"] = system
	return nil
}

// DocType implements polydoc.Document.
func (Time) DocType() string { return sectionTime }

// Location returns the time zone: the system zone for TimeZoneSystem, else
// the named one. Go uses UTC as the system zone when it cannot determine
// one. If the named zone cannot be loaded, Location returns UTC together
// with the error, so that callers can warn and carry on; an empty name is
// such an error.
func (t Time) Location() (*time.Location, error) {
	switch t.TimeZone {
	case TimeZoneSystem:
		return time.Local, nil
	case "":
		return time.UTC, errors.New("no time zone, using UTC")
	}
	loc, err := time.LoadLocation(t.TimeZone)
	if err != nil {
		return time.UTC, fmt.Errorf("time zone %q, using UTC: %w", t.TimeZone, err)
	}
	return loc, nil
}

func (t Time) validate() error {
	_, err := t.Location()
	return err
}

// LockMode says which locks an instance of a command needs (spec
// command-engine.md, B20 to B25).
type LockMode string

// Lock modes.
const (
	// LockPerCommandType has one lock per command kind (B21).
	LockPerCommandType LockMode = "per_command_type"
	// LockPerActionType has one lock per action type; an instance needs the
	// locks of all action types of its command (B22).
	LockPerActionType LockMode = "per_action_type"
	// LockVisualAudio has one lock for the commands with a visual or audio
	// action; the others need none (B23).
	LockVisualAudio LockMode = "visual_audio"
	// LockSingular has one lock for all commands (B24).
	LockSingular LockMode = "singular"
	// LockNone has no locks (B25).
	LockNone LockMode = "none"
)

// Valid reports whether m is a known lock mode.
func (m LockMode) Valid() bool {
	switch m {
	case LockPerCommandType, LockPerActionType, LockVisualAudio, LockSingular, LockNone:
		return true
	default:
		return false
	}
}

// ErrorCooldown says how the error messages of unmet requirements are held
// back (spec command-engine.md, B12).
type ErrorCooldown string

// Error cooldown modes.
const (
	// ErrorCooldownPerCommand holds back a message per requirement and
	// command.
	ErrorCooldownPerCommand ErrorCooldown = "per_command"
	// ErrorCooldownGlobal holds back all messages for one shared duration.
	ErrorCooldownGlobal ErrorCooldown = "global"
	// ErrorCooldownOff sends a message for every rejection.
	ErrorCooldownOff ErrorCooldown = "off"
	// ErrorCooldownSilent sends no message at all; rejections are only
	// logged.
	ErrorCooldownSilent ErrorCooldown = "silent"
)

// Valid reports whether c is a known error cooldown mode.
func (c ErrorCooldown) Valid() bool {
	switch c {
	case ErrorCooldownPerCommand, ErrorCooldownGlobal, ErrorCooldownOff, ErrorCooldownSilent:
		return true
	default:
		return false
	}
}

// Limits of Commands.EntranceMediaGap (spec command-engine.md, B43, B90).
const (
	// DefaultEntranceMediaGap is the gap of a profile that never set one.
	DefaultEntranceMediaGap = 5 * time.Second
	// MinEntranceMediaGap is the shortest gap.
	MinEntranceMediaGap = time.Second
	// MaxEntranceMediaGap is the longest gap.
	MaxEntranceMediaGap = time.Minute
)

// Limits of Commands.QueueSize (spec command-engine.md, B15, B90).
const (
	// DefaultQueueSize is the queue size of a profile that never set one.
	DefaultQueueSize = 1000
	// MinQueueSize is the smallest queue.
	MinQueueSize = 1
	// MaxQueueSize is the largest queue; each waiting instance holds a
	// goroutine and its data.
	MaxQueueSize = 10_000
)

// Limits of Commands.UserLookupAttempts and UserLookupTimeout (spec
// command-engine.md, B17, B90).
const (
	// DefaultUserLookupAttempts is how often a lookup of a user is tried in
	// a profile that never set it.
	DefaultUserLookupAttempts = 3
	// MinUserLookupAttempts and MaxUserLookupAttempts limit the attempts.
	MinUserLookupAttempts = 1
	MaxUserLookupAttempts = 10
	// DefaultUserLookupTimeout is the time limit of one attempt in a
	// profile that never set it.
	DefaultUserLookupTimeout = 2 * time.Second
	// MinUserLookupTimeout and MaxUserLookupTimeout limit the time limit of
	// one attempt.
	MinUserLookupTimeout = 100 * time.Millisecond
	MaxUserLookupTimeout = 30 * time.Second
)

// Commands configures the command engine (spec command-engine.md, B90).
// Changes apply to the instances queued afterwards. Version 1 of the section
// had no EntranceMediaGap, version 2 no QueueSize, version 3 no
// UserLookupAttempts and UserLookupTimeout; the migrations set their
// defaults.
type Commands struct {
	// LockMode is the lock mode of all commands (B20).
	LockMode LockMode `json:"lockMode"`
	// ErrorCooldown is the mode of the error cooldown (B12).
	ErrorCooldown ErrorCooldown `json:"errorCooldown"`
	// ErrorCooldownDuration is how long an error message holds back the next
	// one; a duration of 0 holds back nothing.
	ErrorCooldownDuration polydoc.Duration `json:"errorCooldownDuration"`
	// ArgDelimiter separates the delimited arguments of templates (spec
	// template.md, $argdelimited...).
	ArgDelimiter string `json:"argDelimiter"`
	// EntranceMediaGap is how long a picture or sound of a greeting waits
	// after the playback of the one before (B43), from MinEntranceMediaGap
	// to MaxEntranceMediaGap.
	EntranceMediaGap polydoc.Duration `json:"entranceMediaGap"`
	// QueueSize is how many instances may wait at the same time (B15),
	// from MinQueueSize to MaxQueueSize. A size below the number of
	// waiting instances takes no new ones until enough of them have
	// started.
	QueueSize int `json:"queueSize"`
	// UserLookupAttempts is how often a lookup of a user by name is tried
	// before it fails, and UserLookupTimeout how long one attempt may take
	// (B17), e.g. when the platform answers slowly.
	UserLookupAttempts int              `json:"userLookupAttempts"`
	UserLookupTimeout  polydoc.Duration `json:"userLookupTimeout"`
}

// DefaultCommands returns the defaults of B90.
func DefaultCommands() Commands {
	return Commands{
		LockMode:              LockPerCommandType,
		ErrorCooldown:         ErrorCooldownPerCommand,
		ErrorCooldownDuration: polydoc.Duration(10 * time.Second),
		ArgDelimiter:          "|",
		EntranceMediaGap:      polydoc.Duration(DefaultEntranceMediaGap),
		QueueSize:             DefaultQueueSize,
		UserLookupAttempts:    DefaultUserLookupAttempts,
		UserLookupTimeout:     polydoc.Duration(DefaultUserLookupTimeout),
	}
}

// migrateCommandsV1 adds the gap of version 2 with its default.
func migrateCommandsV1(doc map[string]jsontext.Value) error {
	if _, ok := doc["entranceMediaGap"]; ok {
		return errors.New("entrance media gap in version 1")
	}
	gap, err := json.Marshal(polydoc.Duration(DefaultEntranceMediaGap))
	if err != nil {
		return err
	}
	doc["entranceMediaGap"] = gap
	return nil
}

// migrateCommandsV2 adds the queue size of version 3 with its default.
func migrateCommandsV2(doc map[string]jsontext.Value) error {
	if _, ok := doc["queueSize"]; ok {
		return errors.New("queue size in version 2")
	}
	size, err := json.Marshal(DefaultQueueSize)
	if err != nil {
		return err
	}
	doc["queueSize"] = size
	return nil
}

// migrateCommandsV3 adds the attempts and the time limit of user lookups
// of version 4 with their defaults.
func migrateCommandsV3(doc map[string]jsontext.Value) error {
	for _, field := range []string{"userLookupAttempts", "userLookupTimeout"} {
		if _, ok := doc[field]; ok {
			return fmt.Errorf("%s in version 3", field)
		}
	}
	attempts, err := json.Marshal(DefaultUserLookupAttempts)
	if err != nil {
		return err
	}
	timeout, err := json.Marshal(polydoc.Duration(DefaultUserLookupTimeout))
	if err != nil {
		return err
	}
	doc["userLookupAttempts"], doc["userLookupTimeout"] = attempts, timeout
	return nil
}

// DocType implements polydoc.Document.
func (Commands) DocType() string { return sectionCommands }

// Validate checks the section, e.g. before the command engine uses it.
func (c Commands) Validate() error {
	return c.validate()
}

func (c Commands) validate() error {
	if !c.LockMode.Valid() {
		return fmt.Errorf("unknown lock mode %q", c.LockMode)
	}
	if !c.ErrorCooldown.Valid() {
		return fmt.Errorf("unknown error cooldown mode %q", c.ErrorCooldown)
	}
	if c.ErrorCooldownDuration < 0 {
		return errors.New("the error cooldown must not be negative")
	}
	if c.ArgDelimiter == "" {
		return errors.New("empty argument delimiter")
	}
	if strings.IndexFunc(c.ArgDelimiter, unicode.IsControl) >= 0 {
		return errors.New("the argument delimiter contains a control character")
	}
	if gap := c.EntranceMediaGap.Std(); gap < MinEntranceMediaGap || gap > MaxEntranceMediaGap {
		return fmt.Errorf("entrance media gap %s is not between %s and %s", gap, MinEntranceMediaGap, MaxEntranceMediaGap)
	}
	if c.QueueSize < MinQueueSize || c.QueueSize > MaxQueueSize {
		return fmt.Errorf("queue size %d is not between %d and %d", c.QueueSize, MinQueueSize, MaxQueueSize)
	}
	if c.UserLookupAttempts < MinUserLookupAttempts || c.UserLookupAttempts > MaxUserLookupAttempts {
		return fmt.Errorf("user lookup attempts %d are not between %d and %d", c.UserLookupAttempts, MinUserLookupAttempts, MaxUserLookupAttempts)
	}
	if t := c.UserLookupTimeout.Std(); t < MinUserLookupTimeout || t > MaxUserLookupTimeout {
		return fmt.Errorf("user lookup timeout %s is not between %s and %s", t, MinUserLookupTimeout, MaxUserLookupTimeout)
	}
	return nil
}
