// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"
	"strings"
)

// completionCmd prints the shell completion scripts of streamcrew.
type completionCmd struct {
	Shell completionShell `arg:"" required:"" help:"The shell to print the script for."`
}

type completionShell string

const (
	completionBash completionShell = "bash"
	completionZsh  completionShell = "zsh"
	completionFish completionShell = "fish"
)

// commandTree is the command tree of the completion scripts; keep it in
// sync with Root (TestCompletionCoversTheTree checks it).
//
//nolint:gochecknoglobals // the static command tree, never mutated
var commandTree = map[string][]string{
	"profile":    {"list", "create", "rename", "use", "delete", "channels"},
	"backup":     {"create", "list", "restore"},
	"secret":     {"rotate"},
	"config":     {"show", "path"},
	"schema":     {"export"},
	"command":    {"validate", "import", "export"},
	"auth":       {"login", "status", "logout"},
	"user":       {"merge"},
	"token":      {"create", "list", "revoke"},
	"chat":       {"send"},
	"event":      {"simulate"},
	"completion": {"bash", "zsh", "fish"},
}

// topLevel are the commands without subcommands, in the order of Root.
//
//nolint:gochecknoglobals // the static command order, never mutated
var topLevel = []string{"serve", "mock", "profile", "backup", "secret", "version", "config", "doctor", "schema", "command", "auth", "user", "token", "chat", "event", "completion"}

// Run prints the script of the shell.
func (c completionCmd) Run(_ context.Context, e *Env) error {
	switch c.Shell {
	case completionBash:
		fmt.Fprint(e.Stdout, bashScript())
	case completionZsh:
		fmt.Fprint(e.Stdout, zshScript())
	case completionFish:
		fmt.Fprint(e.Stdout, fishScript())
	default:
		return &usageError{err: fmt.Errorf("unknown shell %q", c.Shell)}
	}
	return nil
}

// topLevelList is the command names as a space-separated word list.
func topLevelList() string {
	return strings.Join(topLevel, " ")
}

// bashScript is the bash completion script.
func bashScript() string {
	var b strings.Builder
	b.WriteString(`# streamcrew bash completion
_streamcrew() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local prev="${COMP_WORDS[COMP_CWORD-1]}"
  local cmds="`)
	b.WriteString(topLevelList())
	b.WriteString(`"
  case "$prev" in
`)
	for cmd, subs := range commandTree {
		fmt.Fprintf(&b, "    %s) COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") ); return ;;\n", cmd, strings.Join(subs, " "))
	}
	b.WriteString(`  esac
  COMPREPLY=( $(compgen -W "$cmds" -- "$cur") )
}
complete -F _streamcrew streamcrew
`)
	return b.String()
}

// zshScript is the zsh completion script.
func zshScript() string {
	var b strings.Builder
	b.WriteString(`#compdef streamcrew
# streamcrew zsh completion
_streamcrew() {
  local prev="${words[-2]}"
  local -a cmds
  cmds=(`)
	for _, cmd := range topLevel {
		fmt.Fprintf(&b, "\n    '%s'", cmd)
	}
	b.WriteString("\n  )\n  case \"$prev\" in\n")
	for cmd, subs := range commandTree {
		fmt.Fprintf(&b, "    %s) compadd %s ;;\n", cmd, strings.Join(subs, " "))
	}
	b.WriteString(`    *) compadd -- ${cmds} ;;
  esac
}
`)
	return b.String()
}

// fishScript is the fish completion script.
func fishScript() string {
	var b strings.Builder
	b.WriteString(`# streamcrew fish completion
`)
	fmt.Fprintf(&b, "complete -c streamcrew -f -a \"%s\"\n", topLevelList())
	for cmd, subs := range commandTree {
		fmt.Fprintf(&b, "complete -c streamcrew -f -n \"__fish_seen_subcommand_from %s\" -a \"%s\"\n", cmd, strings.Join(subs, " "))
	}
	return b.String()
}
