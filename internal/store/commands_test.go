// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/store"
)

func commandService(t *testing.T) (*commandSaver, *command.Codec) {
	t.Helper()
	codec, err := command.NewCodec()
	require.NoError(t, err)
	return newCommandService(t, openStore(t), codec), codec
}

// commandSaver is a command service whose Save returns the stored command
// and fails the test on warnings, which these tests do not expect.
type commandSaver struct {
	*command.Service
	t *testing.T
}

func newCommandService(t *testing.T, s *store.Store, codec *command.Codec) *commandSaver {
	t.Helper()
	svc, err := command.NewService(s, codec, command.Checks{Counters: s, Names: noNames{}, Types: noTypes{}, Roots: noRoots{}})
	require.NoError(t, err)
	return &commandSaver{Service: svc, t: t}
}

func (c *commandSaver) Save(ctx context.Context, cmd command.Command) (command.Command, error) {
	res, err := c.Service.Save(ctx, cmd)
	if err == nil {
		assert.Empty(c.t, res.Warnings)
	}
	return res.Command, err
}

// noNames reserves no names.
type noNames struct{}

func (noNames) Reserved(string) (string, bool) { return "", false }

// noTypes knows no action type that lacks a capability.
type noTypes struct{}

func (noTypes) Missing(string) []capability.Capability { return []capability.Capability{} }

// noRoots releases no root for files.
type noRoots struct{}

func (noRoots) HasRoot(string) bool { return false }

func chatCommand(name string, enabled bool, triggers ...string) command.Command {
	return command.Command{Name: name, Kind: command.KindChat, Enabled: enabled, Triggers: triggers, ErrorPolicy: command.ErrorContinue}
}

// TestCommandKeepsUnknownActions covers B1 and B4: a command with actions of
// unknown types is saved, loaded and written back unchanged.
func TestCommandKeepsUnknownActions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, codec := commandService(t)

	actions := jsontext.Value(`[{"type":"chat.send","schemaVersion":1,"message":"Hi $username!"},{"type":"obs.scene","schemaVersion":4,"scene":"Main"}]`)
	cmd, err := codec.Command(command.Record{
		Name: "hug", Kind: command.KindChat, Enabled: true, Unlocked: true, Triggers: []string{"hug"}, Wildcard: true,
		ErrorPolicy: command.ErrorContinue, Actions: actions,
	})
	require.NoError(t, err)
	cmd.Requirements = []command.Requirement{
		command.RoleRequirement{Role: role.Follower},
		command.CooldownRequirement{Scope: command.CooldownPerUser, Duration: polydoc.Duration(30 * time.Second)},
	}

	saved, err := svc.Save(ctx, cmd)
	require.NoError(t, err)
	assert.False(t, saved.ID.IsZero())
	assert.Equal(t, saved.CreatedAt, saved.UpdatedAt)
	assert.True(t, saved.Enabled)
	assert.True(t, saved.Unlocked)
	assert.True(t, saved.Wildcard)
	assert.Equal(t, command.ErrorContinue, saved.ErrorPolicy, "spec command-engine.md, B71")
	assert.Equal(t, cmd.Requirements, saved.Requirements)

	loaded, err := svc.Command(ctx, saved.ID)
	require.NoError(t, err)
	assert.Equal(t, saved, loaded)
	rec, err := codec.Record(loaded)
	require.NoError(t, err)
	assert.JSONEq(t, string(actions), string(rec.Actions))

	// Saving again keeps the creation time and the unknown actions.
	resaved, err := svc.Save(ctx, loaded)
	require.NoError(t, err)
	assert.Equal(t, saved.CreatedAt, resaved.CreatedAt)
	assert.Equal(t, loaded.Actions, resaved.Actions)

	loaded.ErrorPolicy = command.ErrorAbort
	aborting, err := svc.Save(ctx, loaded)
	require.NoError(t, err)
	assert.Equal(t, command.ErrorAbort, aborting.ErrorPolicy)
}

// TestTriggersAsEntered covers B11, B12 and B60: triggers are stored without
// "!", as a list and in the spelling they were entered in.
func TestTriggersAsEntered(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	cmd, err := svc.Save(ctx, chatCommand("hug", true, command.ParseTriggers("!Hug;good night; !umarmen")...))
	require.NoError(t, err)
	assert.Equal(t, []string{"Hug", "good night", "umarmen"}, cmd.Triggers)

	_, err = svc.Save(ctx, chatCommand("empty", true))
	require.ErrorIs(t, err, command.ErrInvalid, "B61")
}

// TestTriggerUniqueAmongEnabledChatCommands covers B14.
func TestTriggerUniqueAmongEnabledChatCommands(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	first, err := svc.Save(ctx, chatCommand("hug", true, "hug"))
	require.NoError(t, err)
	_, err = svc.Save(ctx, chatCommand("hug 2", true, "HUG"))
	require.ErrorIs(t, err, store.ErrConflict, "same trigger regardless of case")

	second, err := svc.Save(ctx, chatCommand("hug 2", false, "HUG"))
	require.NoError(t, err, "a disabled command may reuse the trigger")
	second.Enabled = true
	_, err = svc.Save(ctx, second)
	require.ErrorIs(t, err, store.ErrConflict, "enabling it collides")

	first.Enabled = false
	_, err = svc.Save(ctx, first)
	require.NoError(t, err)
	_, err = svc.Save(ctx, second)
	require.NoError(t, err, "free once the first one is disabled")

	all, err := svc.Commands(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, []string{"hug"}, all[0].Triggers)
	assert.Equal(t, []string{"HUG"}, all[1].Triggers)
}

// TestOneCommandPerEventType covers B20.
func TestOneCommandPerEventType(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	follow := command.Command{Name: "follow alert", Kind: command.KindEvent, Event: eventtype.ChannelFollow, ErrorPolicy: command.ErrorContinue}
	saved, err := svc.Save(ctx, follow)
	require.NoError(t, err)
	assert.Equal(t, eventtype.ChannelFollow, saved.Event)

	follow.Name = "second follow alert"
	_, err = svc.Save(ctx, follow)
	require.ErrorIs(t, err, store.ErrConflict)

	follow.Event = eventtype.TwitchChannelFollow
	_, err = svc.Save(ctx, follow)
	require.NoError(t, err, "the Twitch-specific type is a type of its own (B2)")
}

// TestCooldownGroups covers commands.md B33 and B64: names unique
// regardless of case, a positive duration, cooldowns that must name an
// existing cooldown group, and commands that stay readable when it is
// deleted.
func TestCooldownGroups(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	sounds, err := svc.SaveCooldownGroup(ctx, command.CooldownGroup{Name: "Sounds", Duration: 30 * time.Second})
	require.NoError(t, err)
	assert.False(t, sounds.ID.IsZero())
	assert.Equal(t, 30*time.Second, sounds.Duration)
	_, err = svc.SaveCooldownGroup(ctx, command.CooldownGroup{Name: "SOUNDS", Duration: time.Second})
	require.ErrorIs(t, err, store.ErrConflict, "names are unique regardless of case")
	_, err = svc.SaveCooldownGroup(ctx, command.CooldownGroup{Name: "zero"})
	require.ErrorIs(t, err, command.ErrInvalid)

	sounds.Duration = time.Minute
	updated, err := svc.SaveCooldownGroup(ctx, sounds)
	require.NoError(t, err)
	assert.Equal(t, time.Minute, updated.Duration)
	assert.Equal(t, sounds.CreatedAt, updated.CreatedAt)
	alerts, err := svc.SaveCooldownGroup(ctx, command.CooldownGroup{Name: "alerts", Duration: time.Second})
	require.NoError(t, err)
	all, err := svc.CooldownGroups(ctx)
	require.NoError(t, err)
	assert.Equal(t, []id.ID{alerts.ID, sounds.ID}, []id.ID{all[0].ID, all[1].ID}, "by name")

	cmd := chatCommand("boom", true, "boom")
	cmd.Requirements = []command.Requirement{command.CooldownRequirement{Scope: command.CooldownGrouped, Group: sounds.ID}}
	saved, err := svc.Save(ctx, cmd)
	require.NoError(t, err)

	unknown := chatCommand("bang", true, "bang")
	unknown.Requirements = []command.Requirement{command.CooldownRequirement{Scope: command.CooldownPerUserGrouped, Group: id.New()}}
	_, err = svc.Save(ctx, unknown)
	require.ErrorIs(t, err, command.ErrInvalid)
	require.ErrorContains(t, err, "unknown cooldown group")

	require.NoError(t, svc.DeleteCooldownGroup(ctx, sounds.ID))
	_, err = svc.CooldownGroup(ctx, sounds.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.ErrorIs(t, svc.DeleteCooldownGroup(ctx, sounds.ID), store.ErrNotFound)
	kept, err := svc.Command(ctx, saved.ID)
	require.NoError(t, err, "the command stays readable")
	assert.Equal(t, []command.Requirement{command.CooldownRequirement{Scope: command.CooldownGrouped, Group: sounds.ID}}, kept.Requirements)
}

// TestGroups covers B30, B31 and B62.
func TestGroups(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	fun, err := svc.SaveGroup(ctx, command.Group{Name: "Spaß", TimerInterval: 15 * time.Minute})
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, fun.TimerInterval)
	_, err = svc.SaveGroup(ctx, command.Group{Name: "SPASS"})
	require.NoError(t, err, "different letters")
	_, err = svc.SaveGroup(ctx, command.Group{Name: "spaß"})
	require.ErrorIs(t, err, store.ErrConflict, "names are unique regardless of case")

	cmd := chatCommand("hug", true, "hug")
	cmd.GroupID = fun.ID
	cmd, err = svc.Save(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, fun.ID, cmd.GroupID)

	missing := chatCommand("lost", true, "lost")
	missing.GroupID = id.New()
	_, err = svc.Save(ctx, missing)
	require.ErrorIs(t, err, store.ErrConflict, "the group must exist")

	groups, err := svc.Groups(ctx)
	require.NoError(t, err)
	require.Len(t, groups, 2)

	require.NoError(t, svc.DeleteGroup(ctx, fun.ID))
	cmd, err = svc.Command(ctx, cmd.ID)
	require.NoError(t, err, "B62: the command stays")
	assert.True(t, cmd.GroupID.IsZero())
	require.ErrorIs(t, svc.DeleteGroup(ctx, fun.ID), store.ErrNotFound)
	_, err = svc.Group(ctx, fun.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteCommand(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	cmd, err := svc.Save(ctx, chatCommand("hug", true, "hug"))
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, cmd.ID))
	_, err = svc.Command(ctx, cmd.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.ErrorIs(t, svc.Delete(ctx, cmd.ID), store.ErrNotFound)

	_, err = svc.Save(ctx, chatCommand("hug again", true, "hug"))
	require.NoError(t, err, "the trigger is free again")
}

// refAction is an action type for tests that refers to commands, groups,
// counters and roots, sets result values and holds child actions.
type refAction struct {
	Commands []id.ID          `json:"commands,omitempty"`
	Groups   []id.ID          `json:"groups,omitempty"`
	Counters []string         `json:"counters,omitempty"`
	Roots    []string         `json:"roots,omitempty"`
	Names    []string         `json:"names,omitempty"`
	Kids     []command.Action `json:"children,omitempty"`
}

func (refAction) DocType() string              { return "ref" }
func (refAction) Validate() error              { return nil }
func (a refAction) Children() []command.Action { return a.Kids }
func (a refAction) ResultNames() []string      { return a.Names }

func (a refAction) References() []command.Reference {
	var refs []command.Reference
	for _, cmdID := range a.Commands {
		refs = append(refs, command.Reference{Kind: command.RefCommand, ID: cmdID})
	}
	for _, groupID := range a.Groups {
		refs = append(refs, command.Reference{Kind: command.RefGroup, ID: groupID})
	}
	for _, name := range a.Counters {
		refs = append(refs, command.Reference{Kind: command.RefCounter, Name: name})
	}
	for _, name := range a.Roots {
		refs = append(refs, command.Reference{Kind: command.RefFileRoot, Name: name})
	}
	return refs
}

// fakeChecks are configurable checks for the command service.
type fakeChecks struct {
	reserved map[string]string
	missing  map[string][]capability.Capability
	roots    []string
}

func (f fakeChecks) Reserved(name string) (string, bool) {
	builtIn, ok := f.reserved[name]
	return builtIn, ok
}

func (f fakeChecks) Missing(actionType string) []capability.Capability {
	return append([]capability.Capability{}, f.missing[actionType]...)
}

func (f fakeChecks) HasRoot(name string) bool { return slices.Contains(f.roots, name) }

// TestSaveChecksActions covers Code-ADR-0013, point 7: saving checks the
// references and result names of actions, also of child actions, creates
// missing counters and warns about missing capabilities and unknown roots.
func TestSaveChecksActions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	codec, err := command.NewCodec(polydoc.Entry[command.Action]{
		Type: "ref", Version: 1,
		Decode: func(data []byte, opts json.Options) (command.Action, error) {
			return polydoc.Strict[refAction](data, opts)
		},
	})
	require.NoError(t, err)
	fake := fakeChecks{
		reserved: map[string]string{"username": "username"},
		missing:  map[string][]capability.Capability{"ref": {capability.HostFS}},
		roots:    []string{"overlays"},
	}
	svc, err := command.NewService(s, codec, command.Checks{Counters: s, Names: fake, Types: fake, Roots: fake})
	require.NoError(t, err)

	target, err := svc.Save(ctx, chatCommand("target", true, "target"))
	require.NoError(t, err)
	group, err := svc.SaveGroup(ctx, command.Group{Name: "fun"})
	require.NoError(t, err)
	_, err = s.CreateCounter(ctx, counter.New("Deaths"))
	require.NoError(t, err)

	caller := chatCommand("caller", true, "caller")
	caller.Actions = []command.Action{refAction{
		Commands: []id.ID{target.Command.ID},
		Groups:   []id.ID{group.ID},
		Counters: []string{"deaths", "wins"},
		Roots:    []string{"overlays", "secret"},
		Names:    []string{"answer"},
		Kids:     []command.Action{refAction{Counters: []string{"wins", "losses"}}},
	}}
	saved, err := svc.Save(ctx, caller)
	require.NoError(t, err)
	assert.Equal(t, []command.Warning{
		{Kind: command.WarnCapability, Path: []int{1}, ActionType: "ref", Subject: "host:fs"},
		{Kind: command.WarnFileRoot, Path: []int{1}, ActionType: "ref", Subject: "secret"},
		{Kind: command.WarnCapability, Path: []int{1, 1}, ActionType: "ref", Subject: "host:fs"},
	}, saved.Warnings)

	counters, err := s.Counters(ctx)
	require.NoError(t, err)
	var names []string
	for _, c := range counters {
		names = append(names, c.Name)
		assert.Zero(t, c.Value)
		assert.Equal(t, int64(counter.DefaultStep), c.Step, "counters-and-quotes.md B8")
	}
	assert.ElementsMatch(t, []string{"Deaths", "wins", "losses"}, names, "B41: missing counters are created once, regardless of case")

	for name, tc := range map[string]struct {
		action command.Action
		want   string
	}{
		"unknown command":       {refAction{Commands: []id.ID{id.New()}}, "action 1 (ref): unknown command"},
		"unknown group":         {refAction{Groups: []id.ID{id.New()}}, "unknown command group"},
		"hidden identifier":     {refAction{Names: []string{"username"}}, `result name "username" hides $username`},
		"invalid counter name":  {refAction{Counters: []string{"two words"}}, "action 1 (ref)"},
		"unknown in a child":    {refAction{Kids: []command.Action{refAction{}, refAction{Commands: []id.ID{id.New()}}}}, "action 1.2 (ref): unknown command"},
		"counter of a built-in": {refAction{Counters: []string{"username"}}, "action 1 (ref)"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := chatCommand("bad "+name, true, "bad")
			cmd.Actions = []command.Action{tc.action}
			_, err := svc.Save(ctx, cmd)
			require.ErrorIs(t, err, command.ErrInvalid)
			assert.ErrorContains(t, err, tc.want)
		})
	}
	all, err := svc.Commands(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 2, "a command that fails the checks is not stored")
}

func TestNewServiceNeedsChecks(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	codec, err := command.NewCodec()
	require.NoError(t, err)
	full := command.Checks{Counters: s, Names: noNames{}, Types: noTypes{}, Roots: noRoots{}}
	for name, change := range map[string]func(*command.Checks){
		"counters": func(c *command.Checks) { c.Counters = nil },
		"names":    func(c *command.Checks) { c.Names = nil },
		"types":    func(c *command.Checks) { c.Types = nil },
		"roots":    func(c *command.Checks) { c.Roots = nil },
	} {
		checks := full
		change(&checks)
		_, err := command.NewService(s, codec, checks)
		assert.Error(t, err, name)
	}
	_, err = command.NewService(s, nil, full)
	require.Error(t, err, "codec")
}

// TestSwitchCommand covers actions.md B34 with commands.md B14: the switch
// changes for good, a command in the target state stays unchanged, and the
// triggers follow the switch.
func TestSwitchCommand(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)
	hug, err := svc.Save(ctx, chatCommand("hug", true, "hug"))
	require.NoError(t, err)

	off, err := svc.SwitchCommand(ctx, hug.ID, command.SwitchOff)
	require.NoError(t, err)
	assert.False(t, off.Enabled)
	assert.False(t, off.UpdatedAt.Before(hug.UpdatedAt))

	again, err := svc.SwitchCommand(ctx, hug.ID, command.SwitchOff)
	require.NoError(t, err, "the target state is no error")
	assert.Equal(t, off, again, "a command in the target state stays unchanged")

	on, err := svc.SwitchCommand(ctx, hug.ID, command.SwitchToggle)
	require.NoError(t, err)
	assert.True(t, on.Enabled)
	toggled, err := svc.SwitchCommand(ctx, hug.ID, command.SwitchToggle)
	require.NoError(t, err)
	assert.False(t, toggled.Enabled)

	other, err := svc.Save(ctx, chatCommand("hug 2", true, "HUG"))
	require.NoError(t, err, "the trigger of a disabled command is free")
	_, err = svc.SwitchCommand(ctx, hug.ID, command.SwitchOn)
	require.ErrorIs(t, err, store.ErrConflict, "B14: enabling it collides")
	stored, err := svc.Command(ctx, hug.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)

	_, err = svc.SwitchCommand(ctx, other.ID, command.SwitchOff)
	require.NoError(t, err)
	_, err = svc.SwitchCommand(ctx, hug.ID, command.SwitchOn)
	require.NoError(t, err, "free once the other one is disabled")

	_, err = svc.SwitchCommand(ctx, id.New(), command.SwitchOn)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = svc.SwitchCommand(ctx, hug.ID, "flip")
	require.ErrorIs(t, err, command.ErrInvalid)
}

// TestSwitchGroup covers actions.md B34: all commands of the group, all or
// none.
func TestSwitchGroup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)
	fun, err := svc.SaveGroup(ctx, command.Group{Name: "fun"})
	require.NoError(t, err)
	inGroup := func(name string, enabled bool, triggers ...string) command.Command {
		cmd := chatCommand(name, enabled, triggers...)
		cmd.GroupID = fun.ID
		cmd, err := svc.Save(ctx, cmd)
		require.NoError(t, err)
		return cmd
	}
	a := inGroup("a", true, "a")
	b := inGroup("b", false, "b")
	outside, err := svc.Save(ctx, chatCommand("c", true, "c"))
	require.NoError(t, err)

	require.NoError(t, svc.SwitchGroup(ctx, fun.ID, command.SwitchOn))
	enabled := func(cmd command.Command) bool {
		stored, err := svc.Command(ctx, cmd.ID)
		require.NoError(t, err)
		return stored.Enabled
	}
	assert.True(t, enabled(a))
	assert.True(t, enabled(b))

	require.NoError(t, svc.SwitchGroup(ctx, fun.ID, command.SwitchOff))
	assert.False(t, enabled(a))
	assert.False(t, enabled(b))
	assert.True(t, enabled(outside), "only the commands of the group")

	_, err = svc.Save(ctx, chatCommand("b elsewhere", true, "B"))
	require.NoError(t, err)
	require.ErrorIs(t, svc.SwitchGroup(ctx, fun.ID, command.SwitchOn), store.ErrConflict)
	assert.False(t, enabled(a), "all or none")
	assert.False(t, enabled(b))

	empty, err := svc.SaveGroup(ctx, command.Group{Name: "empty"})
	require.NoError(t, err)
	require.NoError(t, svc.SwitchGroup(ctx, empty.ID, command.SwitchOn), "a group without commands")
	require.ErrorIs(t, svc.SwitchGroup(ctx, id.New(), command.SwitchOn), store.ErrNotFound)
	require.ErrorIs(t, svc.SwitchGroup(ctx, fun.ID, ""), command.ErrInvalid)
}
