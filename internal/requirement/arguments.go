// SPDX-License-Identifier: MIT

package requirement

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/expr"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/template"
)

// checkArguments assigns the arguments of the run p to the arguments r
// defines, in order (B30 to B35): the last one takes the remaining words if
// it is text, and each value must fit its type. It returns p with the
// values of the arguments that have an identifier name, or the rejection.
// An empty word counts as missing.
func (s *Service) checkArguments(ctx context.Context, cmd command.Command, r command.ArgumentsRequirement, p engine.Params, users engine.Users) (run engine.Params, rej engine.Rejection, rejected bool, err error) {
	values := make(map[string]template.Value, len(r.Arguments))
	for i, a := range r.Arguments {
		word := ""
		if i < len(p.Args) {
			word = p.Args[i]
		}
		last := i == len(r.Arguments)-1
		if last && a.Type == command.ArgumentText && i < len(p.Args) {
			word = strings.Join(p.Args[i:], " ")
		}
		if word == "" {
			if a.Required {
				return p, usageRejection(cmd, r, p), true, nil
			}
			continue
		}
		v, why, fits, verr := argumentValue(ctx, a, word, p, users)
		if verr != nil || !fits {
			return p, why, verr == nil, verr
		}
		if a.Identifier != "" {
			values[a.Identifier] = v
		}
	}
	if len(values) > 0 {
		merged := maps.Clone(p.Values)
		if merged == nil {
			merged = make(map[string]template.Value, len(values))
		}
		maps.Copy(merged, values)
		p.Values = merged
	}
	return p, engine.Rejection{}, false, nil
}

// argumentValue returns the value of word for the argument a (B33, B35); ok
// is false with the rejection if word does not fit the type of a.
func argumentValue(ctx context.Context, a command.Argument, word string, p engine.Params, users engine.Users) (v template.Value, rej engine.Rejection, ok bool, err error) {
	switch a.Type {
	case command.ArgumentText:
		return template.TextValue(word), engine.Rejection{}, true, nil
	case command.ArgumentNumber:
		f, ok := expr.ParseNumber(word)
		if !ok {
			return template.Value{}, typeRejection(a, p), false, nil
		}
		return template.Value{Text: word, Number: f, IsNumber: true}, engine.Rejection{}, true, nil
	case command.ArgumentInteger:
		n, ok := parseInteger(word)
		if !ok {
			return template.Value{}, typeRejection(a, p), false, nil
		}
		return template.Value{Text: word, Number: decimal.New(n), IsNumber: true}, engine.Rejection{}, true, nil
	case command.ArgumentUser:
		return userValue(ctx, a, word, p, users)
	default:
		return template.Value{}, engine.Rejection{}, false, fmt.Errorf("argument %q: unknown type %q", a.Name, a.Type)
	}
}

// parseInteger returns the whole number word stands for (B33): decimal
// digits with an optional sign, in the range of 64 bits.
func parseInteger(word string) (int64, bool) {
	n, err := strconv.ParseInt(word, 10, 64)
	return n, err == nil
}

// userValue returns the login name of the user word names on the platform
// of the run, with or without "@", without a platform on the default
// platform (B33, B35, B105). A name with "@" that the core does not know
// stands for itself (B113); "@" alone is no user.
func userValue(ctx context.Context, a command.Argument, word string, p engine.Params, users engine.Users) (template.Value, engine.Rejection, bool, error) {
	name, at := strings.CutPrefix(word, "@")
	if name == "" {
		return template.Value{}, typeRejection(a, p), false, nil
	}
	if users == nil {
		return template.Value{}, engine.Rejection{}, false, fmt.Errorf("argument %q: no lookup of users", a.Name)
	}
	on := cmp.Or(p.Platform, platform.Default)
	u, found, err := users.UserByName(ctx, on, name)
	if err != nil {
		return template.Value{}, engine.Rejection{}, false, fmt.Errorf("argument %q: find user %q on %s: %w", a.Name, name, on, err)
	}
	if found {
		if ident, ok := u.Identity(on); ok {
			name = ident.Login
		}
		return template.TextValue(name), engine.Rejection{}, true, nil
	}
	if at {
		return template.TextValue(name), engine.Rejection{}, true, nil
	}
	return template.Value{}, engine.Rejection{
		Requirement: command.TypeArguments,
		Reason: i18n.Message{Key: i18n.KeyRequirementArgumentsUser, Args: map[string]i18n.Value{
			"argument": i18n.Text(a.Name), "name": i18n.Text(name),
		}},
		Tell: told(p),
	}, false, nil
}

// typeRejection returns the rejection of a value that does not fit the type
// of the argument a (B33).
func typeRejection(a command.Argument, p engine.Params) engine.Rejection {
	return engine.Rejection{
		Requirement: command.TypeArguments,
		Reason: i18n.Message{Key: i18n.KeyRequirementArgumentsType, Args: map[string]i18n.Value{
			"argument": i18n.Text(a.Name), "type": i18n.Text(string(a.Type)),
		}},
		Tell: told(p),
	}
}

// usageRejection returns the rejection of a missing argument, which shows
// the usage (B32): the trigger, required arguments in angle brackets and
// optional ones in square brackets, e.g. "!hug <user> [reason]".
func usageRejection(cmd command.Command, r command.ArgumentsRequirement, p engine.Params) engine.Rejection {
	parts := []string{usedTrigger(cmd, p)}
	for _, a := range r.Arguments {
		if a.Required {
			parts = append(parts, "<"+a.Name+">")
		} else {
			parts = append(parts, "["+a.Name+"]")
		}
	}
	return engine.Rejection{
		Requirement: command.TypeArguments,
		Reason: i18n.Message{Key: i18n.KeyRequirementArgumentsUsage, Args: map[string]i18n.Value{
			"usage": i18n.Text(strings.Join(parts, " ")),
		}},
		Tell: told(p),
	}
}

// usedTrigger returns the trigger of cmd as the user writes it
// (commands.md, B11, B13): the one the message of p matches, otherwise the
// first. If several match, an exact match counts before one regardless of
// case, then the longest (B16). A command without triggers is shown by its
// name.
func usedTrigger(cmd command.Command, p engine.Params) string {
	if len(cmd.Triggers) == 0 {
		return cmd.Name
	}
	trigger, best := cmd.Triggers[0], command.NoMatch
	for _, t := range cmd.Triggers {
		m := command.MatchTrigger(p.Message, t, cmd.TriggerMode)
		if m > best || m == best && m != command.NoMatch && len(t) > len(trigger) {
			trigger, best = t, m
		}
	}
	return cmd.TriggerMode.Typed(trigger)
}
