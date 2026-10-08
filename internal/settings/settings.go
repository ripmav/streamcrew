// SPDX-License-Identifier: MIT

// Package settings holds the runtime settings of a profile as typed,
// versioned sections (roadmap phase 2.2). Each section is a polymorphic
// document (Code-ADR-0010) in the settings table of the profile database;
// a section that was never saved has its defaults.
//
// There are the sections "backups" and "time" (roadmap 2.2), "commands"
// (roadmap 3.2) and "locale" (roadmap 3.4, ADR-0022); the others (general,
// chat, moderation, overlay) follow with their features.
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

// unknown keeps a section this version cannot read.
type unknown struct{ polydoc.Unknown }

func (u unknown) DocType() string         { return u.Type }
func (u unknown) RawJSON() jsontext.Value { return u.Raw }
func (unknown) validate() error           { return errors.New("unknown settings section") }

const (
	sectionBackups  = "backups"
	sectionTime     = "time"
	sectionCommands = "commands"
	sectionLocale   = "locale"
	sectionEvents   = "events"
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
