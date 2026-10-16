// SPDX-License-Identifier: MIT

package template_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/template"
)

// fakeCommands is the CommandLister for the tests.
type fakeCommands struct {
	calls *atomic.Int64
	list  []command.Record
}

func (f fakeCommands) Commands(context.Context) ([]command.Record, error) {
	f.calls.Add(1)
	return f.list, nil
}

func TestCommandSource(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	cmds := fakeCommands{
		calls: &calls,
		list: []command.Record{
			{Kind: command.KindChat, Enabled: true, TriggerMode: command.TriggerExclamation, Triggers: []string{"uptime"}},
			{Kind: command.KindChat, Enabled: true, TriggerMode: command.TriggerExclamation, Triggers: []string{"tip", "donate"}},
			{Kind: command.KindChat, Enabled: false, TriggerMode: command.TriggerExclamation, Triggers: []string{"off"}},
			{Kind: command.KindEvent, Enabled: true, Triggers: []string{"event"}},
		},
	}
	e := template.New(nil, template.WithSources(template.CommandSource(cmds)))
	// enabled chat commands only, exclamation triggers prefixed with "!"
	assert.Equal(t, "!uptime, !tip, !donate", render(t, e, "$commands", new(scope())))
	assert.Equal(t, 1, int(calls.Load()), "one call per render")
	// two references in one render read the list once
	assert.Equal(t, "!uptime, !tip, !donate !uptime, !tip, !donate", render(t, e, "$commands $commands", new(scope())))
	assert.Equal(t, 2, int(calls.Load()), "one call per render")
}

func TestCommandSource_NotMatched(t *testing.T) {
	t.Parallel()
	cmds := fakeCommands{calls: new(atomic.Int64), list: []command.Record{
		{Kind: command.KindChat, Enabled: true, TriggerMode: command.TriggerExclamation, Triggers: []string{"uptime"}},
	}}
	e := template.New(nil, template.WithSources(template.CommandSource(cmds)))
	// a longer token that only starts with "commands" is not the identifier
	assert.Equal(t, "$commandsfollowage", render(t, e, "$commandsfollowage", new(scope())))
	assert.Zero(t, cmds.calls.Load(), "no list read without the identifier")
}

func TestCommandSource_Empty(t *testing.T) {
	t.Parallel()
	cmds := fakeCommands{calls: new(atomic.Int64)}
	e := template.New(nil, template.WithSources(template.CommandSource(cmds)))
	assert.Equal(t, "[]", render(t, e, "[$commands]", new(scope())))
}
