// SPDX-License-Identifier: MIT

package timer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
)

// testNow is a fixed clock the tests advance.
func testNow() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }

func polyDur(d time.Duration) polydoc.Duration { return polydoc.Duration(d) }

func mustID(t *testing.T, s string) id.ID {
	t.Helper()
	i, err := id.Parse(s)
	require.NoError(t, err)
	return i
}

// fakeCommands is the Commands port with fixed commands and groups.
type fakeCommands struct {
	cmds   []command.Command
	groups []command.Group
}

func (f fakeCommands) Commands(context.Context) ([]command.Command, error) { return f.cmds, nil }
func (f fakeCommands) Groups(context.Context) ([]command.Group, error)     { return f.groups, nil }

// fakeEngine records the runs the timer starts.
type fakeEngine struct {
	started []engine.Params
}

func (f *fakeEngine) Start(_ context.Context, _ command.Command, p engine.Params) (id.ID, error) {
	f.started = append(f.started, p)
	return id.New(), nil
}

// fakePlatforms is the Platforms port.
type fakePlatforms struct{ list []platform.Name }

func (f fakePlatforms) Connected() []platform.Name { return f.list }

// fakeLive is the Live port.
type fakeLive struct{ live map[platform.Name]bool }

func (f fakeLive) IsLive(p platform.Name) bool { return f.live[p] }

// fakeMessages is the Messages port.
type fakeMessages struct{ count int }

func (f fakeMessages) MessagesSince(platform.Name, time.Time) int { return f.count }

func newService(t *testing.T, cfg settings.Timers, cmds []command.Command, groups []command.Group, msgs fakeMessages, live fakeLive) *Service {
	t.Helper()
	s, err := New(Ports{
		Commands:  fakeCommands{cmds: cmds, groups: groups},
		Engine:    &fakeEngine{},
		Platforms: fakePlatforms{list: []platform.Name{platform.Twitch}},
		Live:      live,
		Messages:  msgs,
		Settings:  func(context.Context) (settings.Timers, error) { return cfg, nil },
	})
	require.NoError(t, err)
	s.now = testNow
	return s
}

func timerCmd(t *testing.T, idStr, group string, enabled bool) command.Command {
	c := command.Command{ID: mustID(t, idStr), Kind: command.KindTimer, Enabled: enabled}
	if group != "" {
		c.GroupID = mustID(t, group)
	}
	return c
}

func (s *Service) setNow(tm time.Time) { s.now = func() time.Time { return tm } }

func TestGlobalTimerFiresAfterInterval(t *testing.T) {
	cfg := settings.Timers{Interval: polyDur(60 * time.Second), Order: settings.OrderFixed}
	cmds := []command.Command{
		timerCmd(t, "01900000-0000-7000-8000-000000000003", "", true),
		timerCmd(t, "01900000-0000-7000-8000-000000000001", "", true),
		timerCmd(t, "01900000-0000-7000-8000-000000000002", "", true),
	}
	s := newService(t, cfg, cmds, nil, fakeMessages{}, fakeLive{live: map[platform.Name]bool{}})
	eng := s.ports.Engine.(*fakeEngine)

	lastGlobal := testNow()
	s.setNow(testNow().Add(30 * time.Second))
	s.tick(context.Background(), &lastGlobal, map[id.ID]time.Time{})
	assert.Empty(t, eng.started, "before the interval: nothing")

	s.setNow(testNow().Add(60 * time.Second))
	s.tick(context.Background(), &lastGlobal, map[id.ID]time.Time{})
	require.Len(t, eng.started, 3, "at the interval: all three")
	assert.Equal(t, platform.Twitch, eng.started[0].Platform)
	assert.Equal(t, "", eng.started[0].Channel)
}

func TestGlobalTimerHonoursLastFire(t *testing.T) {
	cfg := settings.Timers{Interval: polyDur(60 * time.Second), Order: settings.OrderFixed}
	s := newService(t, cfg, []command.Command{timerCmd(t, "01900000-0000-7000-8000-000000000001", "", true)}, nil, fakeMessages{}, fakeLive{live: map[platform.Name]bool{}})
	eng := s.ports.Engine.(*fakeEngine)

	lastGlobal := testNow()
	s.setNow(testNow().Add(60 * time.Second))
	s.tick(context.Background(), &lastGlobal, map[id.ID]time.Time{})
	require.Len(t, eng.started, 1)
	assert.True(t, lastGlobal.Equal(testNow().Add(60*time.Second)), "last fire updated")

	s.setNow(testNow().Add(70 * time.Second))
	s.tick(context.Background(), &lastGlobal, map[id.ID]time.Time{})
	assert.Len(t, eng.started, 1, "not due again")
}

func TestLiveOnly(t *testing.T) {
	cfg := settings.Timers{Interval: polyDur(60 * time.Second), LiveOnly: true, Order: settings.OrderFixed}
	cmd := timerCmd(t, "01900000-0000-7000-8000-000000000001", "", true)
	live := fakeLive{live: map[platform.Name]bool{platform.Twitch: false}}
	s := newService(t, cfg, []command.Command{cmd}, nil, fakeMessages{}, live)
	eng := s.ports.Engine.(*fakeEngine)

	lastGlobal := testNow()
	s.setNow(testNow().Add(60 * time.Second))
	s.tick(context.Background(), &lastGlobal, map[id.ID]time.Time{})
	assert.Empty(t, eng.started, "not live: no fire")

	live.live[platform.Twitch] = true
	s.setNow(testNow().Add(120 * time.Second))
	s.tick(context.Background(), &lastGlobal, map[id.ID]time.Time{})
	assert.Len(t, eng.started, 1, "live: fires")
}

func TestMinChatMessages(t *testing.T) {
	cfg := settings.Timers{Interval: polyDur(60 * time.Second), MinChatMessages: 5, Order: settings.OrderFixed}
	cmd := timerCmd(t, "01900000-0000-7000-8000-000000000001", "", true)
	s := newService(t, cfg, []command.Command{cmd}, nil, fakeMessages{count: 3}, fakeLive{live: map[platform.Name]bool{}})
	eng := s.ports.Engine.(*fakeEngine)

	lastGlobal := testNow()
	s.setNow(testNow().Add(60 * time.Second))
	s.tick(context.Background(), &lastGlobal, map[id.ID]time.Time{})
	assert.Empty(t, eng.started, "3 < 5: no fire")
}

func TestMinChatMessagesMet(t *testing.T) {
	cfg := settings.Timers{Interval: polyDur(60 * time.Second), MinChatMessages: 5, Order: settings.OrderFixed}
	cmd := timerCmd(t, "01900000-0000-7000-8000-000000000001", "", true)
	s := newService(t, cfg, []command.Command{cmd}, nil, fakeMessages{count: 5}, fakeLive{live: map[platform.Name]bool{}})
	eng := s.ports.Engine.(*fakeEngine)

	lastGlobal := testNow()
	s.setNow(testNow().Add(60 * time.Second))
	s.tick(context.Background(), &lastGlobal, map[id.ID]time.Time{})
	assert.Len(t, eng.started, 1, "5 >= 5: fires")
}

func TestDisabledCommandSkipped(t *testing.T) {
	cfg := settings.Timers{Interval: polyDur(60 * time.Second), Order: settings.OrderFixed}
	cmds := []command.Command{
		timerCmd(t, "01900000-0000-7000-8000-000000000001", "", true),
		timerCmd(t, "01900000-0000-7000-8000-000000000002", "", false),
	}
	s := newService(t, cfg, cmds, nil, fakeMessages{}, fakeLive{live: map[platform.Name]bool{}})
	eng := s.ports.Engine.(*fakeEngine)

	lastGlobal := testNow()
	s.setNow(testNow().Add(60 * time.Second))
	s.tick(context.Background(), &lastGlobal, map[id.ID]time.Time{})
	assert.Len(t, eng.started, 1, "the disabled command is skipped")
}

func TestGroupTimerOwnSchedule(t *testing.T) {
	// The global interval is long and the minimum high; the group has its
	// own short interval and fires in its target channel without the minimum
	// (B31).
	cfg := settings.Timers{Interval: polyDur(time.Hour), MinChatMessages: 100, Order: settings.OrderFixed}
	groupID := "01900000-0000-7000-8000-0000000000a0"
	groups := []command.Group{{ID: mustID(t, groupID), TimerInterval: 30 * time.Second, TimerChannel: "other"}}
	cmds := []command.Command{
		timerCmd(t, "01900000-0000-7000-8000-000000000001", groupID, true),
		timerCmd(t, "01900000-0000-7000-8000-000000000002", "", true), // global: interval not due
	}
	s := newService(t, cfg, cmds, groups, fakeMessages{count: 0}, fakeLive{live: map[platform.Name]bool{}})
	eng := s.ports.Engine.(*fakeEngine)

	lastGlobal := testNow()
	lastGroup := map[id.ID]time.Time{}

	// First tick registers the group (no fire yet).
	s.setNow(testNow().Add(1 * time.Second))
	s.tick(context.Background(), &lastGlobal, lastGroup)
	assert.Empty(t, eng.started, "group first tick: registered, not due")

	// After the group interval: the group command fires in "other" with 0
	// messages and the global interval not due (B31).
	s.setNow(testNow().Add(31 * time.Second))
	s.tick(context.Background(), &lastGlobal, lastGroup)
	require.Len(t, eng.started, 1)
	assert.Equal(t, "other", eng.started[0].Channel)
	assert.Equal(t, platform.Twitch, eng.started[0].Platform)
}

func TestOrderFixedByID(t *testing.T) {
	cmds := []command.Command{
		timerCmd(t, "01900000-0000-7000-8000-000000000003", "", true),
		timerCmd(t, "01900000-0000-7000-8000-000000000001", "", true),
		timerCmd(t, "01900000-0000-7000-8000-000000000002", "", true),
	}
	fixed := ordered(cmds, settings.OrderFixed)
	assert.Equal(t, cmds[1].ID, fixed[0].ID, "sorted ascending by ID")
	assert.Equal(t, cmds[2].ID, fixed[1].ID)
	assert.Equal(t, cmds[0].ID, fixed[2].ID)
}

func TestOrderRandomDiffers(t *testing.T) {
	cmds := []command.Command{
		timerCmd(t, "01900000-0000-7000-8000-000000000001", "", true),
		timerCmd(t, "01900000-0000-7000-8000-000000000002", "", true),
		timerCmd(t, "01900000-0000-7000-8000-000000000003", "", true),
	}
	shuffled := false
	for i := 0; i < 200 && !shuffled; i++ {
		r := ordered(cmds, settings.OrderRandom)
		if r[0].ID != cmds[0].ID {
			shuffled = true
		}
	}
	assert.True(t, shuffled, "a random order should differ from the sorted one sometimes")
}
