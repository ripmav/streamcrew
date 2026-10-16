// SPDX-License-Identifier: MIT

package template

import (
	"context"
	"fmt"
	"strings"

	"github.com/ripmav/streamcrew/internal/domain/command"
)

// commandsIdentifier is the name of the command list identifier
// (roadmap 5.6, Commands-Liste).
const commandsIdentifier = "commands"

// CommandLister lists the commands of the profile; *store.Store implements it.
type CommandLister interface {
	Commands(ctx context.Context) ([]command.Record, error)
}

// CommandSource returns the source of the identifier $commands: the triggers
// of all enabled chat commands, joined by ", " (roadmap 5.6, Commands-Liste).
// A trigger of an exclamation command is prefixed with "!" as entered. The
// source lists the commands once per render, so a new, renamed or toggled
// command applies from the next render on.
func CommandSource(lister CommandLister) Source {
	return commandSource{lister: lister}
}

// commandSource implements CommandSource.
type commandSource struct {
	lister CommandLister
}

// Match resolves the $commands identifier; every other token is not of this
// source.
func (c commandSource) Match(ctx context.Context, s *Scope, token string) (int, Resolver, error) {
	if token != commandsIdentifier {
		return 0, nil, nil
	}
	text, err := s.Memo(commandsIdentifier+".list", func() (string, error) {
		recs, err := c.lister.Commands(ctx)
		if err != nil {
			return "", fmt.Errorf("list commands: %w", err)
		}
		var b strings.Builder
		first := true
		for _, rec := range recs {
			if rec.Kind != command.KindChat || !rec.Enabled {
				continue
			}
			for _, trigger := range rec.Triggers {
				if !first {
					b.WriteString(", ")
				}
				first = false
				if rec.TriggerMode == command.TriggerExclamation {
					b.WriteByte('!')
				}
				b.WriteString(trigger)
			}
		}
		return b.String(), nil
	})
	if err != nil {
		return 0, nil, err
	}
	return len(commandsIdentifier), constant(TextValue(text)), nil
}
