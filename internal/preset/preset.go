// SPDX-License-Identifier: MIT

// Package preset is the ready-made chat commands of a fresh profile
// (roadmap 5.6, P0; plan appendix A.3). They are commands as data
// (Code-ADR-0013): each answers the channel with the matching template
// identifier through the chat action.
package preset

import (
	"github.com/ripmav/streamcrew/internal/action"
	chataction "github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/domain/command"
)

// Commands returns the ready-made commands. The app seeds the ones that do
// not exist yet by name (roadmap 5.6, P0).
func Commands() []command.Command {
	return []command.Command{
		answer("uptime", "$streamuptimetotal"),
		answer("followage", "$followage"),
		answer("title", "$streamtitle"),
		answer("game", "$streamgamename"),
	}
}

// answer returns a chat command that answers with the identifier message.
func answer(name, message string) command.Command {
	return command.Command{
		Name:        name,
		Kind:        command.KindChat,
		Enabled:     true,
		TriggerMode: command.TriggerExclamation,
		Triggers:    []string{name},
		ErrorPolicy: command.ErrorContinue,
		Actions: []command.Action{
			chataction.Chat{
				Kind:    chataction.KindMessage,
				Message: action.Template(message),
				Chat:    &chataction.MessageOptions{},
			},
		},
	}
}
