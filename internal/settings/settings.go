// SPDX-License-Identifier: MIT

// Package settings holds the runtime settings of a profile as typed,
// versioned sections (roadmap phase 2.2). Each section is a polymorphic
// document (Code-ADR-0010) in the settings table of the profile database;
// a section that was never saved has its defaults.
//
// There are the sections "backups" and "time" (roadmap 2.2) and "commands"
// (roadmap 3.2); the others (general, chat, moderation, overlay, locale)
// follow with their features.
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

	"github.com/ripmav/streamcrew/internal/polydoc"
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
		{Type: sectionCommands, Version: 2, Decode: decode[Commands], Migrations: []polydoc.Migration{migrateCommandsV1}},
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
)

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
)

// Valid reports whether c is a known error cooldown mode.
func (c ErrorCooldown) Valid() bool {
	switch c {
	case ErrorCooldownPerCommand, ErrorCooldownGlobal, ErrorCooldownOff:
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

// Commands configures the command engine (spec command-engine.md, B90).
// Changes apply to the instances queued afterwards. Version 1 of the section
// had no EntranceMediaGap; version 2 has it, and the migration sets
// DefaultEntranceMediaGap.
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
}

// DefaultCommands returns the defaults of B90.
func DefaultCommands() Commands {
	return Commands{
		LockMode:              LockPerCommandType,
		ErrorCooldown:         ErrorCooldownPerCommand,
		ErrorCooldownDuration: polydoc.Duration(10 * time.Second),
		ArgDelimiter:          "|",
		EntranceMediaGap:      polydoc.Duration(DefaultEntranceMediaGap),
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
	return nil
}
