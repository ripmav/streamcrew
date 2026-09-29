// SPDX-License-Identifier: Apache-2.0

// Package settings holds the runtime settings of a profile as typed,
// versioned sections (roadmap phase 2.2). Each section is a polymorphic
// document (Code-ADR-0010) in the settings table of the profile database;
// a section that was never saved has its defaults.
//
// Phase 2 has the sections "backups" and "time"; the others (general, chat,
// commands, moderation, overlay) follow with their features.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
		{Type: sectionTime, Version: 1, Decode: decode[Time]},
	} {
		if err := r.Register(e); err != nil {
			return nil, err
		}
	}
	return &Service{repo: repo, registry: r}, nil
}

func decode[S Section](data []byte) (Section, error) {
	s, err := polydoc.Strict[S](data)
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

func (u unknown) DocType() string          { return u.Type }
func (u unknown) RawJSON() json.RawMessage { return u.Raw }
func (unknown) validate() error            { return errors.New("unknown settings section") }

const (
	sectionBackups = "backups"
	sectionTime    = "time"
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

// Time holds the time zone of the profile (Code-ADR-0009).
type Time struct {
	// TimeZone is an IANA name such as "Europe/Berlin"; empty means the
	// time zone of the system.
	TimeZone string `json:"timeZone"`
}

// DefaultTime returns the system time zone.
func DefaultTime() Time {
	return Time{}
}

// DocType implements polydoc.Document.
func (Time) DocType() string { return sectionTime }

// Location returns the time zone; the system zone if none is set.
func (t Time) Location() (*time.Location, error) {
	if t.TimeZone == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(t.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("time zone %q: %w", t.TimeZone, err)
	}
	return loc, nil
}

func (t Time) validate() error {
	_, err := t.Location()
	return err
}
