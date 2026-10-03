// SPDX-License-Identifier: Apache-2.0

package command

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/domain/id"
)

// RefKind says what an action refers to (Code-ADR-0013, point 7).
type RefKind string

// The kinds of references.
const (
	// RefCommand refers to a command by ID (spec actions.md, B31).
	RefCommand RefKind = "command"
	// RefGroup refers to a command group by ID (actions.md, B31).
	RefGroup RefKind = "group"
	// RefCounter refers to a counter by name (actions.md, B41).
	RefCounter RefKind = "counter"
	// RefFileRoot refers to a released root for files by name
	// (actions.md, B100).
	RefFileRoot RefKind = "file_root"
)

// Reference is something an action refers to.
type Reference struct {
	Kind RefKind
	// ID names a command or a group.
	ID id.ID
	// Name names a counter or a released root.
	Name string
}

// Referrer is an action that refers to commands, groups, counters or
// released roots; saving checks them (Code-ADR-0013, point 7).
type Referrer interface {
	References() []Reference
}

// ResultSetter is an action or a requirement that sets values of the run
// under names the streamer chooses: the result values of actions
// (actions.md, B5) and the identifiers of arguments (requirements.md, B36).
// Saving checks that the names hide no built-in identifier.
type ResultSetter interface {
	ResultNames() []string
}

// Counters are the counters of the profile; *store.Store implements it.
type Counters interface {
	Counters(ctx context.Context) ([]counter.Counter, error)
	CreateCounter(ctx context.Context, c counter.Counter) (counter.Counter, error)
}

// Names reports whether a name is taken by a built-in identifier or a fixed
// result name of an action type (actions.md, B5); the composition root
// joins the template registry and the action type registry for it.
type Names = counter.Reserver

// ActionTypes names the capabilities that actions of a type need and the
// core does not have; *action.Registry implements it.
type ActionTypes interface {
	Missing(actionType string) []capability.Capability
}

// Roots reports whether the start configuration releases a root for files
// with this name (ADR-0013).
type Roots interface {
	HasRoot(name string) bool
}

// Checks are what the service needs to check the actions of a command
// when it saves it (Code-ADR-0013, point 7). All fields are required.
type Checks struct {
	Counters Counters
	Names    Names
	Types    ActionTypes
	Roots    Roots
}

// validate reports a missing field.
func (c Checks) validate() error {
	switch {
	case c.Counters == nil:
		return errors.New("no counters")
	case c.Names == nil:
		return errors.New("no names")
	case c.Types == nil:
		return errors.New("no action types")
	case c.Roots == nil:
		return errors.New("no roots")
	}
	return nil
}

// WarningKind says what a warning of a save is about.
type WarningKind string

// The kinds of warnings.
const (
	// WarnCapability: the action needs a capability the core does not
	// have in its operating mode; it fails when it runs (actions.md, B7).
	WarnCapability WarningKind = "missing_capability"
	// WarnFileRoot: the start configuration releases no root of this name;
	// the action fails when it runs (actions.md, B102).
	WarnFileRoot WarningKind = "unknown_file_root"
	// WarnUnknownReference: the requirement refers to a currency, a rank
	// or an item that does not exist; the command is faulty and does not
	// run (requirements.md, B7, B81).
	WarnUnknownReference WarningKind = "unknown_reference"
)

// Warning is something about a saved command that will make an action fail
// or keep the command from running on this core, although the command is
// stored: the start configuration may differ between computers, and
// imports should be kept (actions.md, B7; requirements.md, B81).
type Warning struct {
	Kind WarningKind
	// Path is the place of the action, e.g. [3, 2] (actions.md, B9); empty
	// for a requirement.
	Path []int
	// ActionType is the type of the action; empty for a requirement.
	ActionType string
	// Requirement is the type of the requirement; empty for an action.
	Requirement string
	// Subject names the capability, the root, or the ID that the
	// requirement refers to.
	Subject string
}

// Saved is the outcome of a save: the command as stored and the warnings
// about it; the list is empty if there are none.
type Saved struct {
	Command  Command
	Warnings []Warning
}

// eachAction calls fn for the actions of list and, depth first, their
// child actions, with their paths.
func eachAction(list []Action, path []int, fn func(path []int, a Action) error) error {
	for i, a := range list {
		at := append(slices.Clone(path), i+1)
		if err := fn(at, a); err != nil {
			return err
		}
		if p, ok := a.(Parent); ok {
			if err := eachAction(p.Children(), at, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkActions checks the references and result names of the actions of
// cmd, creates the counters they name that do not exist yet and returns
// the warnings (Code-ADR-0013, point 7). Counters it created stay if the
// command is not stored after all; they start at 0 and do no harm.
func (s *Service) checkActions(ctx context.Context, cmd Command) ([]Warning, error) {
	known, err := s.known(ctx)
	if err != nil {
		return nil, err
	}
	warnings := []Warning{}
	err = eachAction(cmd.Actions, nil, func(path []int, a Action) error {
		at := func(err error) error {
			return fmt.Errorf("%w: action %s (%s): %w", ErrInvalid, position(path), a.DocType(), err)
		}
		for _, c := range s.checks.Types.Missing(a.DocType()) {
			warnings = append(warnings, Warning{Kind: WarnCapability, Path: path, ActionType: a.DocType(), Subject: string(c)})
		}
		if r, ok := a.(ResultSetter); ok {
			for _, name := range r.ResultNames() {
				if builtIn, taken := s.checks.Names.Reserved(name); taken {
					return at(fmt.Errorf("result name %q hides $%s", name, builtIn))
				}
			}
		}
		r, ok := a.(Referrer)
		if !ok {
			return nil
		}
		for _, ref := range r.References() {
			unknownRoot, err := s.checkReference(ctx, known, ref)
			if err != nil {
				return at(err)
			}
			if unknownRoot {
				warnings = append(warnings, Warning{Kind: WarnFileRoot, Path: path, ActionType: a.DocType(), Subject: ref.Name})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return warnings, nil
}

// checkRequirements checks the requirements of cmd against the profile
// and returns the warnings (requirements.md, B80, B81): a grouped cooldown
// must name a cooldown group that exists (B33), and the identifiers of
// arguments must not hide a built-in identifier (B36). A currency, a rank
// or an item a requirement refers to that does not exist is a warning.
// Until roadmap phase 8 there are none, so every such reference is one
// (B40).
func (s *Service) checkRequirements(ctx context.Context, cmd Command) ([]Warning, error) {
	warnings := []Warning{}
	for _, r := range cmd.Requirements {
		at := func(err error) error {
			return fmt.Errorf("%w: requirement %q: %w", ErrInvalid, r.DocType(), err)
		}
		if setter, ok := r.(ResultSetter); ok {
			for _, name := range setter.ResultNames() {
				if builtIn, taken := s.checks.Names.Reserved(name); taken {
					return nil, at(fmt.Errorf("identifier %q hides $%s", name, builtIn))
				}
			}
		}
		var ref id.ID
		switch r := r.(type) {
		case CooldownRequirement:
			if !r.Scope.Grouped() {
				continue
			}
			groups, err := s.repo.CooldownGroups(ctx)
			if err != nil {
				return nil, fmt.Errorf("list cooldown groups: %w", err)
			}
			if !slices.ContainsFunc(groups, func(g CooldownGroup) bool { return g.ID == r.Group }) {
				return nil, at(fmt.Errorf("unknown cooldown group %s", r.Group))
			}
			continue
		case CurrencyRequirement:
			ref = r.Currency
		case RankRequirement:
			ref = r.Rank
		case InventoryRequirement:
			ref = r.Item
		default:
			continue
		}
		warnings = append(warnings, Warning{Kind: WarnUnknownReference, Requirement: r.DocType(), Subject: ref.String()})
	}
	return warnings, nil
}

// knownObjects are the commands, groups and counters at the start of a
// save.
type knownObjects struct {
	commands map[id.ID]bool
	groups   map[id.ID]bool
	counters []string
}

// known lists the commands, groups and counters.
func (s *Service) known(ctx context.Context) (*knownObjects, error) {
	k := &knownObjects{commands: map[id.ID]bool{}, groups: map[id.ID]bool{}}
	recs, err := s.repo.Commands(ctx)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	for _, rec := range recs {
		k.commands[rec.ID] = true
	}
	groups, err := s.repo.Groups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	for _, g := range groups {
		k.groups[g.ID] = true
	}
	counters, err := s.checks.Counters.Counters(ctx)
	if err != nil {
		return nil, fmt.Errorf("list counters: %w", err)
	}
	for _, c := range counters {
		k.counters = append(k.counters, c.Name)
	}
	return k, nil
}

// checkReference checks one reference: unknown commands and groups are
// errors (actions.md, B31), and missing counters are created with the
// value 0 and the default step (B41; counters-and-quotes.md, B8).
// unknownRoot reports a root that the start configuration does not release,
// which is a warning (B102).
func (s *Service) checkReference(ctx context.Context, k *knownObjects, ref Reference) (unknownRoot bool, err error) {
	switch ref.Kind {
	case RefCommand:
		if !k.commands[ref.ID] {
			return false, fmt.Errorf("unknown command %s", ref.ID)
		}
	case RefGroup:
		if !k.groups[ref.ID] {
			return false, fmt.Errorf("unknown command group %s", ref.ID)
		}
	case RefCounter:
		if slices.ContainsFunc(k.counters, func(name string) bool { return counter.Key(name) == counter.Key(ref.Name) }) {
			return false, nil
		}
		c := counter.New(ref.Name)
		if err := c.Validate(); err != nil {
			return false, err
		}
		if err := c.CheckReserved(s.checks.Names); err != nil {
			return false, err
		}
		if _, err := s.checks.Counters.CreateCounter(ctx, c); err != nil {
			return false, fmt.Errorf("create counter %q: %w", ref.Name, err)
		}
		k.counters = append(k.counters, ref.Name)
	case RefFileRoot:
		return !s.checks.Roots.HasRoot(ref.Name), nil
	default:
		return false, fmt.Errorf("unknown kind of reference %q", ref.Kind)
	}
	return false, nil
}
