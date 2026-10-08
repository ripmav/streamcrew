// SPDX-License-Identifier: MIT

// Package requirement decides whether a triggered command runs (spec
// requirements.md). Its Service implements engine.Requirements: it checks
// the requirements of a command in a fixed order and stops at the first
// that is not met (B2), and it tells the user why a command did not run,
// in the language of the profile (B70 to B72, ADR-0022).
//
// So far it checks the role, the cooldown and the arguments, finds faulty
// requirements and deletes the triggering message after the decision if
// the settings say so (B61); thresholds follow (roadmap 3.4). A command
// with a threshold is not decided yet: its decision returns
// ErrNotSupported.
package requirement

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
	// Cooldowns store the running cooldowns.
	Cooldowns Cooldowns
	// Streamer finds the streamer for runs without a user (B4).
	Streamer Streamer
	// Logger records faulty requirements (B7, B8).
	Logger *slog.Logger
}

// Service checks the requirements of commands; it implements
// engine.Requirements.
type Service struct {
	ports Ports
	// mu makes the decisions one after another, so that two runs cannot
	// both pass a cooldown that only one may start (B3, B100). Prepare
	// does not hold it.
	mu sync.Mutex
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
	case p.Cooldowns == nil:
		return nil, errors.New("requirement service: no cooldowns")
	case p.Streamer == nil:
		return nil, errors.New("requirement service: no streamer")
	case p.Logger == nil:
		return nil, errors.New("requirement service: no logger")
	}
	return &Service{ports: p}, nil
}

// Prepare prepares the decision whether cmd runs for p (B1, B2), without
// the lock of the decisions, so that the engine can prepare many triggers
// at the same time (command-engine.md, B16): it finds faulty
// requirements (B7, B8, B40), checks the role (B10 to B12), finds the user
// a cooldown per user counts against (B4) and the users that arguments
// name (B30 to B35) through users, the lookup of the run, which the engine
// gives (command-engine.md, B17). The decision then checks the cooldown (B20 to B24) and
// takes the first requirement that is not met in the order of B2. If all
// are met, it starts the cooldown (B3, B21), which it can take back, and the
// run has the values of the arguments. Decisions are made one after another
// (B3).
func (s *Service) Prepare(ctx context.Context, cmd command.Command, p engine.Params, users engine.Users) (engine.Decide, error) {
	if r, ok, err := s.faulty(ctx, cmd); err != nil || ok {
		return decided(cmd, r, err)
	}
	if r, ok := checkRole(cmd, p); ok {
		return decided(cmd, r, nil)
	}
	pr := &prepared{s: s, cmd: cmd, p: p, run: p}
	pr.cooldown, pr.hasCooldown = find[command.CooldownRequirement](cmd)
	if pr.hasCooldown {
		var err error
		if pr.key, err = s.cooldownKey(ctx, cmd, pr.cooldown, p); err != nil {
			return decided(cmd, engine.Rejection{}, err)
		}
	}
	if args, ok := find[command.ArgumentsRequirement](cmd); ok {
		pr.run, pr.argsRejection, pr.argsRejected, pr.argsErr = s.checkArguments(ctx, cmd, args, p, users)
	}
	return pr.decide, nil
}

// decided returns the decision of a rejection r found while preparing, or
// the error err of preparing cmd.
func decided(cmd command.Command, r engine.Rejection, err error) (engine.Decide, error) {
	if err != nil {
		return nil, fmt.Errorf("prepare command %q: %w", cmd.Name, err)
	}
	return func(context.Context) (engine.Decision, error) { return engine.Rejected(r), nil }, nil
}

// prepared is a decision that Prepare prepared: what is left to check while
// the lock of the decisions is held, and what the arguments gave.
type prepared struct {
	s   *Service
	cmd command.Command
	p   engine.Params
	// cooldown is the cooldown requirement of cmd, if hasCooldown, with its
	// key for p.
	cooldown    command.CooldownRequirement
	hasCooldown bool
	key         command.CooldownKey
	// run is p with the values of the arguments; argsRejection or argsErr
	// say why the arguments do not fit, which counts only after the
	// cooldown (B2).
	run           engine.Params
	argsRejection engine.Rejection
	argsRejected  bool
	argsErr       error
}

// decide implements engine.Decide.
func (pr *prepared) decide(ctx context.Context) (engine.Decision, error) {
	s, cmd := pr.s, pr.cmd
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if pr.hasCooldown {
		if r, ok, err := s.checkCooldown(ctx, pr.cooldown, pr.key, pr.p, now); err != nil || ok {
			return rejectedOrError(cmd, r, err)
		}
	}
	if pr.argsErr != nil || pr.argsRejected {
		return rejectedOrError(cmd, pr.argsRejection, pr.argsErr)
	}
	if _, ok := find[command.ThresholdRequirement](cmd); ok {
		return engine.Decision{}, fmt.Errorf("decide command %q: %w: %s", cmd.Name, ErrNotSupported, command.TypeThreshold)
	}
	d := engine.Met(pr.run)
	if pr.hasCooldown {
		var err error
		if d.Revert, err = s.startCooldown(ctx, pr.cooldown, pr.key, now); err != nil {
			return rejectedOrError(cmd, engine.Rejection{}, err)
		}
	}
	return d, nil
}

// rejectedOrError returns the rejection r, or the error err of deciding
// about cmd.
func rejectedOrError(cmd command.Command, r engine.Rejection, err error) (engine.Decision, error) {
	if err != nil {
		return engine.Decision{}, fmt.Errorf("decide command %q: %w", cmd.Name, err)
	}
	return engine.Rejected(r), nil
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

// StartCooldown starts the cooldown of cmd for p as if cmd had just been
// queued (B25; actions.md, B37); without a cooldown requirement it does
// nothing. The scopes per user need the user of p: unlike a check of the
// requirements, a run without a user does not count as the streamer here.
func (s *Service) StartCooldown(ctx context.Context, cmd command.Command, p engine.Params) error {
	r, ok := find[command.CooldownRequirement](cmd)
	if !ok {
		return nil
	}
	var userID id.ID
	if p.User != nil {
		userID = p.User.ID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := r.Key(cmd.ID, userID)
	if err != nil {
		return fmt.Errorf("start the cooldown of %q: %w", cmd.Name, err)
	}
	if _, err := s.startCooldown(ctx, r, key, time.Now()); err != nil {
		return fmt.Errorf("start the cooldown of %q: %w", cmd.Name, err)
	}
	return nil
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
// checked as they are (B7, B8, B40, B103): one of a type this version does
// not know, a currency, rank or item, which come with roadmap phase 8, or a
// grouped cooldown without a cooldown group or with one that does not
// exist. It logs a warning; the user is not told. An error means it could
// not check.
func (s *Service) faulty(ctx context.Context, cmd command.Command) (engine.Rejection, bool, error) {
	for _, req := range cmd.Requirements {
		var (
			key i18n.Key
			why string
		)
		switch r := req.(type) {
		case command.UnknownRequirement:
			key, why = i18n.KeyRequirementUnknown, "this version does not know the requirement type"
		case command.CurrencyRequirement, command.RankRequirement, command.InventoryRequirement:
			key, why = i18n.KeyRequirementFaulty, "currencies, ranks and items come with roadmap phase 8"
		case command.CooldownRequirement:
			if !r.Scope.Grouped() {
				continue
			}
			if r.Group.IsZero() {
				key, why = i18n.KeyRequirementFaulty, "the cooldown names no cooldown group"
				break
			}
			_, ok, err := s.cooldownGroup(ctx, r.Group)
			if err != nil {
				return engine.Rejection{}, false, err
			}
			if ok {
				continue
			}
			key, why = i18n.KeyRequirementFaulty, "the cooldown group does not exist"
		default:
			continue
		}
		s.ports.Logger.WarnContext(ctx, "command not run: faulty requirement",
			"command", cmd.Name, "requirement", req.DocType(), "reason", why)
		return engine.Rejection{Requirement: req.DocType(), Reason: i18n.Message{Key: key}}, true, nil
	}
	return engine.Rejection{}, false, nil
}
