// SPDX-License-Identifier: Apache-2.0

package commandfile

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/event"
)

// Conflicts drops the commands of plan whose triggers or event type
// another command uses (commands.md, B14, B20): a command of the profile
// that the plan does not replace, or a command earlier in the plan. A
// trigger counts among the active chat commands, in the spelling a user
// types it (command.TriggerKey). It returns the plan without them and a
// problem at each. The profile has each trigger and event type once, and
// a command of the plan claims them only without a conflict, so each has
// one owner.
func Conflicts(plan Plan, existing Existing) (Plan, []Problem) {
	replaced := map[id.ID]bool{}
	for _, p := range plan.Commands {
		replaced[p.Value.ID] = true
	}
	triggers := map[string]string{}
	events := map[event.Type]string{}
	take := func(h command.Header) {
		if h.Kind == command.KindChat && h.Enabled {
			for _, t := range h.Triggers {
				triggers[command.TriggerKey(h.TriggerMode, t)] = h.Name
			}
		}
		if h.Kind == command.KindEvent {
			events[h.Event] = h.Name
		}
	}
	for _, h := range existing.Commands {
		if !replaced[h.ID] {
			take(h)
		}
	}
	var problems []Problem
	kept := plan.Commands[:0:0]
	for _, p := range plan.Commands {
		if prob, ok := conflict(p, triggers, events); ok {
			problems = append(problems, prob)
			continue
		}
		take(p.Value.Header)
		kept = append(kept, p)
	}
	plan.Commands = kept
	return plan, problems
}

// conflict returns the problem of p with a trigger or an event type that
// another command uses; ok is false if there is none.
func conflict(p Planned[command.Command], triggers map[string]string, events map[event.Type]string) (Problem, bool) {
	h := p.Value.Header
	if h.Kind == command.KindChat && h.Enabled {
		for i, t := range h.Triggers {
			if other, used := triggers[command.TriggerKey(h.TriggerMode, t)]; used {
				return p.Doc.problemAt(SeverityError, "spec.triggers["+strconv.Itoa(i)+"]",
					fmt.Sprintf("the trigger %q is used by the active chat command %q", h.TriggerMode.Typed(t), other)), true
			}
		}
	}
	if other, used := events[h.Event]; used {
		return p.Doc.problemAt(SeverityError, "spec.event", fmt.Sprintf("the event command %q reacts to this event type", other)), true
	}
	return Problem{}, false
}

// Saver saves the objects of a plan; *command.Service implements it.
type Saver interface {
	SaveCooldownGroup(ctx context.Context, g command.CooldownGroup) (command.CooldownGroup, error)
	SaveGroup(ctx context.Context, g command.Group) (command.Group, error)
	Save(ctx context.Context, cmd command.Command) (command.Saved, error)
}

// Apply saves the objects of plan with s in the order of an import (B34)
// and returns what saving rejects and warns about, at the place in the
// document (B31, B32): the checks of saving against the profile, e.g.
// names of result values that hide built-in identifiers, and the warnings
// of saving, e.g. a missing capability.
//
// Commands are saved twice: first switched off, without requirements and
// actions, then complete. So commands of the plan can call each other
// (B61), and triggers can move from one to another. A command whose first
// save fails is not saved again. err is only an error of the context.
func Apply(ctx context.Context, plan Plan, s Saver, types Types) (problems []Problem, err error) {
	descriptors := make(map[string]action.Descriptor, len(types.Actions))
	for _, d := range types.Actions {
		descriptors[d.Type] = d
	}
	report := func(doc Document, err error) {
		problems = append(problems, saveProblem(doc, err, descriptors))
	}
	for _, p := range plan.CooldownGroups {
		if _, err := s.SaveCooldownGroup(ctx, p.Value); err != nil {
			report(p.Doc, err)
		}
	}
	for _, p := range plan.Groups {
		if _, err := s.SaveGroup(ctx, p.Value); err != nil {
			report(p.Doc, err)
		}
	}
	first := make([]bool, len(plan.Commands))
	for i, p := range plan.Commands {
		stub := p.Value
		stub.Enabled, stub.Requirements, stub.Actions = false, []command.Requirement{}, []command.Action{}
		if _, err := s.Save(ctx, stub); err != nil {
			report(p.Doc, err)
			continue
		}
		first[i] = true
	}
	for i, p := range plan.Commands {
		if !first[i] {
			continue
		}
		saved, err := s.Save(ctx, p.Value)
		if err != nil {
			report(p.Doc, err)
			continue
		}
		for _, w := range saved.Warnings {
			problems = append(problems, warning(p.Doc, w, descriptors))
		}
	}
	return problems, ctx.Err()
}

// saveProblem returns the problem of an error of saving, at the action or
// requirement it is about, else at the spec of the document.
func saveProblem(doc Document, err error, descriptors map[string]action.Descriptor) Problem {
	if ae, ok := errors.AsType[*command.ActionError](err); ok {
		return doc.problemAt(SeverityError, actionPath(doc, ae.Path, descriptors), cleanMessage(ae.Err))
	}
	if re, ok := errors.AsType[*command.RequirementError](err); ok {
		return doc.problemAt(SeverityError, "spec.requirements."+re.Type, cleanMessage(re.Err))
	}
	return doc.problemAt(SeverityError, "spec", trimInvalid(err))
}

// warning returns the problem of a warning of saving (actions.md, B7;
// requirements.md, B81).
func warning(doc Document, w command.Warning, descriptors map[string]action.Descriptor) Problem {
	path := "spec"
	switch {
	case len(w.Path) > 0:
		path = actionPath(doc, w.Path, descriptors)
	case w.Requirement != "":
		path = "spec.requirements." + w.Requirement
	}
	var msg string
	switch w.Kind {
	case command.WarnCapability:
		msg = fmt.Sprintf("needs the capability %q, which this core does not have; the action fails when it runs", w.Subject)
	case command.WarnFileRoot:
		msg = fmt.Sprintf("the start configuration releases no root %q; the action fails when it runs", w.Subject)
	default: // command.WarnUnknownReference
		msg = fmt.Sprintf("refers to %s, which does not exist; the command does not run", w.Subject)
	}
	return doc.problemAt(SeverityWarning, path, msg)
}
