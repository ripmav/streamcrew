// SPDX-License-Identifier: MIT

// Package preset is the ready-made chat commands of a fresh profile
// (roadmap 5.6, P0; plan appendix A.3). They are commands as data
// (Code-ADR-0013): each answers the channel with the matching template
// identifier through the chat action.
package preset

import (
	"github.com/ripmav/streamcrew/internal/action"
	chataction "github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/action/commandadmin"
	streamaction "github.com/ripmav/streamcrew/internal/action/stream"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/role"
)

// Commands returns the ready-made commands. The app seeds the ones that do
// not exist yet by name (roadmap 5.6, P0).
func Commands() []command.Command {
	return []command.Command{
		answer("uptime", "$streamuptimetotal"),
		answer("followage", "$followage"),
		answer("title", "$streamtitle"),
		answer("game", "$streamgamename"),
		answer("commands", "$commands"),
		setStream("settitle", streamaction.KindSetTitle),
		setStream("setgame", streamaction.KindSetGame),
		admin("addcmd", commandadmin.KindAdd),
		admin("updatecmd", commandadmin.KindUpdate),
		admin("disablecmd", commandadmin.KindDisable),
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

// setStream returns a command that sets the stream title or category to its
// arguments (roadmap 5.6, SetTitle, SetGame): !settitle <text>, !setgame <name>.
func setStream(name string, kind streamaction.Kind) command.Command {
	return command.Command{
		Name:        name,
		Kind:        command.KindChat,
		Enabled:     true,
		TriggerMode: command.TriggerExclamation,
		Triggers:    []string{name},
		ErrorPolicy: command.ErrorContinue,
		Actions: []command.Action{
			streamaction.Stream{
				Kind: kind,
				Text: action.Template("$allargs"),
			},
		},
	}
}

// admin returns a command that manages commands (roadmap 5.6, AddCommand,
// UpdateCommand, DisableCommand): !addcmd <name> <answer>,
// !updatecmd <name> <answer>, !disablecmd <name>. It needs the moderator
// role, so that the chat cannot change the commands of the profile.
func admin(name string, kind commandadmin.Kind) command.Command {
	return command.Command{
		Name:        name,
		Kind:        command.KindChat,
		Enabled:     true,
		TriggerMode: command.TriggerExclamation,
		Triggers:    []string{name},
		ErrorPolicy: command.ErrorContinue,
		Requirements: []command.Requirement{
			command.RoleRequirement{Role: role.Moderator},
		},
		Actions: []command.Action{
			commandadmin.CommandAdmin{Kind: kind},
		},
	}
}
