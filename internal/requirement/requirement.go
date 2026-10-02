// SPDX-License-Identifier: Apache-2.0

// Package requirement decides whether a triggered command runs (spec
// requirements.md). Its Service implements engine.Requirements: it checks
// the requirements of a command in a fixed order and stops at the first
// that is not met (B2), and it tells the user why a command did not run,
// in the language of the profile (B70 to B72, ADR-0022).
//
// So far it checks the role and finds faulty requirements; cooldowns,
// arguments, settings and thresholds follow (roadmap 3.4). A command with
// one of these is not decided yet: Apply returns ErrNotSupported.
package requirement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
)

// ErrNotSupported is returned for a requirement type the service cannot
// check yet; the engine then runs nothing (B1).
var ErrNotSupported = errors.New("requirement type not supported yet")

// Language reads the language of the profile (ADR-0022, point 7); the
// composition root reads the settings section "locale" for it.
type Language interface {
	Language(ctx context.Context) (i18n.Language, error)
}

// Platforms finds the platforms of the profile; *connector.Set implements
// it.
type Platforms interface {
	Platform(name platform.Name) (connector.Platform, bool)
}

// Ports are what the service needs. All are required.
type Ports struct {
	// Catalog renders the messages.
	Catalog *i18n.Catalog
	// Language is the language of the messages.
	Language Language
	// Platforms send the messages.
	Platforms Platforms
	// Logger records faulty requirements (B7, B8).
	Logger *slog.Logger
}

// Service checks the requirements of commands; it implements
// engine.Requirements.
type Service struct {
	ports Ports
}

var _ engine.Requirements = (*Service)(nil)

// New returns a service with the ports p.
func New(p Ports) (*Service, error) {
	switch {
	case p.Catalog == nil:
		return nil, errors.New("requirement service: no message catalog")
	case p.Language == nil:
		return nil, errors.New("requirement service: no language")
	case p.Platforms == nil:
		return nil, errors.New("requirement service: no platforms")
	case p.Logger == nil:
		return nil, errors.New("requirement service: no logger")
	}
	return &Service{ports: p}, nil
}

// Apply decides whether cmd runs for p (B1, B2): faulty requirements first
// (B7, B8, B40), then the role (B10 to B12). The first requirement that is
// not met is the rejection.
func (s *Service) Apply(ctx context.Context, cmd command.Command, p engine.Params) (engine.Decision, error) {
	if r, ok := s.faulty(ctx, cmd); ok {
		return engine.Rejected(r), nil
	}
	if r, ok := checkRole(cmd, p); ok {
		return engine.Rejected(r), nil
	}
	for _, req := range cmd.Requirements {
		switch req.(type) {
		case command.CooldownRequirement, command.ArgumentsRequirement, command.ThresholdRequirement:
			return engine.Decision{}, fmt.Errorf("decide command %q: %w: %s", cmd.Name, ErrNotSupported, req.DocType())
		}
	}
	return engine.Met(p), nil
}

// Notify tells the user of p why cmd did not run (B70 to B72): the reason
// in the language of the profile, in the chat of the platform of the run,
// as a reply to the triggering message where the platform can, otherwise
// after "@" and the login name of the user, from the account the chat
// action sends from (actions.md, B61).
func (s *Service) Notify(ctx context.Context, cmd command.Command, p engine.Params, r engine.Rejection) error {
	lang, err := s.ports.Language.Language(ctx)
	if err != nil {
		return fmt.Errorf("tell why %q did not run: %w", cmd.Name, err)
	}
	text, err := s.ports.Catalog.Render(lang, r.Reason)
	if err != nil {
		return fmt.Errorf("tell why %q did not run: %w", cmd.Name, err)
	}
	target, ok := s.ports.Platforms.Platform(p.Platform)
	if !ok || !target.Status().Connected() {
		return fmt.Errorf("tell why %q did not run: %s: %w", cmd.Name, p.Platform, connector.ErrNotConnected)
	}
	m := connector.Message{Text: text, From: target.Status().Sender(false)}
	chat := target.Chat()
	if replier, ok := chat.(connector.Replier); ok && p.MessageID != "" {
		return replier.Reply(ctx, p.MessageID, m)
	}
	if p.User != nil {
		if ident, ok := p.User.Identity(p.Platform); ok {
			m.Text = "@" + ident.Login + " " + text
		}
	}
	return chat.Send(ctx, m)
}

// StartCooldown starts the cooldown of cmd for p (B25); without a cooldown
// requirement it does nothing.
func (s *Service) StartCooldown(_ context.Context, cmd command.Command, _ engine.Params) error {
	if _, ok := find[command.CooldownRequirement](cmd); !ok {
		return nil
	}
	return fmt.Errorf("start the cooldown of %q: %w: %s", cmd.Name, ErrNotSupported, command.TypeCooldown)
}

// find returns the requirement of type R of cmd; ok is false if it has
// none.
func find[R command.Requirement](cmd command.Command) (r R, ok bool) {
	for _, req := range cmd.Requirements {
		if r, ok := req.(R); ok {
			return r, true
		}
	}
	return r, false
}

// told reports whether a rejection of a requirement the user can be told
// about reaches them: the run has a triggering chat message (B71).
func told(p engine.Params) bool {
	return p.Message != ""
}

// faulty returns the rejection of a command whose requirements cannot be
// checked as they are (B7, B8, B40): one of a type this version does not
// know, or a currency, rank or item, which come with roadmap phase 8. It
// logs a warning; the user is not told.
func (s *Service) faulty(ctx context.Context, cmd command.Command) (engine.Rejection, bool) {
	for _, req := range cmd.Requirements {
		var (
			key i18n.Key
			why string
		)
		switch req.(type) {
		case command.UnknownRequirement:
			key, why = i18n.KeyRequirementUnknown, "this version does not know the requirement type"
		case command.CurrencyRequirement, command.RankRequirement, command.InventoryRequirement:
			key, why = i18n.KeyRequirementFaulty, "currencies, ranks and items come with roadmap phase 8"
		default:
			continue
		}
		s.ports.Logger.WarnContext(ctx, "command not run: faulty requirement",
			"command", cmd.Name, "requirement", req.DocType(), "reason", why)
		return engine.Rejection{Requirement: req.DocType(), Reason: i18n.Message{Key: key}}, true
	}
	return engine.Rejection{}, false
}
