// SPDX-License-Identifier: MIT

// The behavior tests of the widget service (roadmap 7.2, spec
// overlays.md, B24-B28, B50): the goal with its persistence and events,
// the timer operations, the event list, the chat and the labels, as the
// runtime and the bus see them.
package overlaywidgets_test

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/discord"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/feed"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/emote"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/overlaywidgets"
	"github.com/ripmav/streamcrew/internal/poll"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
)

// effect is one bus event published by the service (B23, A2).
type effect struct {
	typ     event.Type
	payload eventtype.Payload
}

// fixture wires the service with the real overlay service and the fakes of
// the other ports, so the test reads what the runtime receives.
type fixture struct {
	t       *testing.T
	svc     *overlaywidgets.Service
	overlay *overlay.Service
	bus     *event.Bus
	ctx     context.Context
	cancel  context.CancelFunc

	runWG   sync.WaitGroup
	drainWG sync.WaitGroup

	mu         sync.Mutex
	loc        *time.Location // nil for the profile default (UTC)
	sec        settings.Overlay
	msgs       []overlay.Message
	effects    []effect
	state      map[string][]byte
	feed       []feed.Event
	streams    map[platform.Name]template.StreamState
	sessions   map[platform.Name]feed.SessionStats
	top        []user.Ranked
	emoteList  []emote.Emote
	discordOcc []discord.Occupant
}

// newFixture returns the fixture with the section and starts the service;
// it runs inside synctest.Test.
func newFixture(t *testing.T, sec settings.Overlay) *fixture {
	t.Helper()
	f := &fixture{
		t:        t,
		bus:      event.NewBus(nil),
		state:    map[string][]byte{},
		streams:  map[platform.Name]template.StreamState{},
		sessions: map[platform.Name]feed.SessionStats{},
	}
	f.ctx, f.cancel = context.WithCancel(t.Context())
	f.setSection(sec)
	ov, err := overlay.New(f.ctx, overlay.Ports{Section: f.section})
	require.NoError(t, err)
	f.overlay = ov
	reg, err := template.NewRegistry()
	require.NoError(t, err)
	f.svc, err = overlaywidgets.New(overlaywidgets.Ports{
		Overlay:          ov,
		Bus:              f.bus,
		Section:          f.section,
		Feed:             f,
		State:            f,
		Templates:        template.New(reg),
		Scope:            f.scope,
		StreamStates:     f,
		SessionStats:     f,
		Locale:           f.locale,
		TopUsers:         f.topUsers,
		Emotes:           f.emotes,
		DiscordOccupants: f.discordOccupants,
	}, overlaywidgets.WithLabelTick(time.Second))
	require.NoError(t, err)
	ov.SetStateProvider(f.svc.StateForSync)
	f.runWG.Go(func() {
		_ = f.svc.Run(f.ctx)
	})
	synctest.Wait() // Run registers its subscription before the test publishes
	sub := f.bus.Subscribe(f.ctx,
		event.WithName("fixture"),
		event.WithTypes(
			eventtype.OverlayGoalProgress, eventtype.OverlayGoalSegmentCompleted,
			eventtype.OverlayTimerAdjusted, eventtype.OverlayTimerCompleted,
			eventtype.OverlayBossDamage, eventtype.OverlayBossHeal, eventtype.OverlayBossNew,
			eventtype.OverlayWheelSpun,
			eventtype.GameQueueJoined, eventtype.GameQueueLeft, eventtype.GameQueuePosition,
			eventtype.DiscordVoiceJoined, eventtype.DiscordVoiceLeft,
		))
	f.drainWG.Go(func() {
		for env := range sub.C() {
			p, _ := event.Payload[eventtype.Payload](env)
			f.mu.Lock()
			f.effects = append(f.effects, effect{typ: env.Type, payload: p})
			f.mu.Unlock()
		}
	})
	return f
}

// stop ends the service run and the effect subscription.
func (f *fixture) stop() {
	f.cancel()
	f.runWG.Wait()
	f.drainWG.Wait()
}

// section returns the overlay section on demand.
func (f *fixture) section(context.Context) (settings.Overlay, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sec, nil
}

func (f *fixture) setSection(sec settings.Overlay) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sec = sec
}

// sectionNow returns the current section for the tests.
func (f *fixture) sectionNow() settings.Overlay {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sec
}

// scope returns the profile scope for the label render and the goal reset.
func (f *fixture) scope(context.Context) (*template.Scope, error) {
	f.mu.Lock()
	loc := f.loc
	f.mu.Unlock()
	if loc == nil {
		loc = time.UTC
	}
	return &template.Scope{Platform: platform.Twitch, Location: loc, ArgDelimiter: " "}, nil
}

// locale returns the locale of the profile.
func (f *fixture) locale(context.Context) (template.Locale, error) {
	return template.LocaleUSEnglish, nil
}

// LatestFeed implements the Feed port; it returns the last n events,
// oldest first.
func (f *fixture) LatestFeed(_ context.Context, n int) ([]feed.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.feed) > n {
		return f.feed[len(f.feed)-n:], nil
	}
	return f.feed, nil
}

// OverlayWidgetState implements the State port.
func (f *fixture) OverlayWidgetState(_ context.Context, widgetID string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.state[widgetID]
	return doc, ok, nil
}

// SetOverlayWidgetState implements the State port.
func (f *fixture) SetOverlayWidgetState(_ context.Context, widgetID string, state []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[widgetID] = state
	return nil
}

// StreamState implements the StreamStates port.
func (f *fixture) StreamState(_ context.Context, p platform.Name) (template.StreamState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.streams[p]
	if !ok {
		return template.StreamState{}, nil
	}
	return st, nil
}

// CurrentSessionStats implements the SessionStats port.
func (f *fixture) CurrentSessionStats(_ context.Context, p platform.Name) (feed.SessionStats, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.sessions[p]
	return st, ok, nil
}

// topUsers implements the TopUsers port; a positive count caps the list.
func (f *fixture) topUsers(_ context.Context, _ user.Stat, n int) ([]user.Ranked, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n > 0 && len(f.top) > n {
		return f.top[:n], nil
	}
	return f.top, nil
}

// emotes implements the Emotes port.
func (f *fixture) emotes(context.Context) []emote.Emote {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.emoteList
}

// discordOccupants implements the DiscordOccupants port.
func (f *fixture) discordOccupants(context.Context) []discord.Occupant {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discordOcc
}

// attach connects the fake runtime of the default endpoint and records
// the messages the runtime receives (B4, B22).
func (f *fixture) attach(t *testing.T) {
	t.Helper()
	_, err := f.overlay.Attach(f.ctx, settings.DefaultEndpointID, func(m overlay.Message) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.msgs = append(f.msgs, m)
	})
	require.NoError(t, err)
}

// updates returns the states of the widget.update messages of the widget,
// as JSON.
func (f *fixture) updates(t *testing.T, widgetID string) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.msgs {
		d, ok := m.Data.(overlay.WidgetUpdateData)
		if !ok || m.Type != overlay.TypeWidgetUpdate || d.ID != widgetID {
			continue
		}
		data, err := json.Marshal(d.State)
		require.NoError(t, err)
		out = append(out, string(data))
	}
	return out
}

// handshakeStates returns the widget states of the state.widgets
// handshake, by widget ID, as JSON (B4).
func (f *fixture) handshakeStates(t *testing.T) map[string]string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for _, m := range f.msgs {
		d, ok := m.Data.(overlay.StateWidgetsData)
		if !ok || m.Type != overlay.TypeStateWidgets {
			continue
		}
		for _, w := range d.Widgets {
			data, err := json.Marshal(w.State)
			require.NoError(t, err)
			out[w.ID] = string(data)
		}
	}
	return out
}

func (f *fixture) effectsList() []effect {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.effects)
}

// stateDoc returns the persisted state document of the goal widget.
func (f *fixture) stateDoc(t *testing.T) []byte {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.state["g1"]
	require.True(t, ok, "no state for the goal widget")
	return doc
}

// stateDocFor returns the persisted state document of the key.
func (f *fixture) stateDocFor(t *testing.T, key string) []byte {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.state[key]
	require.True(t, ok, "no state for %s", key)
	return doc
}

func (f *fixture) seedState(widgetID string, doc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[widgetID] = []byte(doc)
}

func (f *fixture) setFeed(events []feed.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feed = events
}

func (f *fixture) setStream(p platform.Name, st template.StreamState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams[p] = st
}

func (f *fixture) setSession(p platform.Name, st feed.SessionStats) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[p] = st
}

// publish sends one event of the twitch platform to the bus.
func (f *fixture) publish(t *testing.T, typ event.Type, u *user.User, details eventtype.Details) {
	t.Helper()
	payload := eventtype.Payload{Platform: platform.Twitch, Details: details}
	if u != nil {
		payload.User = u
	}
	env := event.New(event.Source{Kind: event.SourcePlatform, Name: string(platform.Twitch)}, typ, payload)
	require.NoError(t, f.bus.Publish(f.ctx, env))
}

// twitchUser returns a user with the login on twitch.
func twitchUser(t *testing.T, login string) *user.User {
	t.Helper()
	return &user.User{
		ID:         id.New(),
		Identities: []user.Identity{{Platform: platform.Twitch, PlatformUserID: login, Login: login, DisplayName: login}},
	}
}

// widget returns a visible widget with the kind and the configuration.
func widget(id, kind string, config any) settings.OverlayWidget {
	return settings.OverlayWidget{
		ID:       id,
		Kind:     kind,
		Position: settings.Position{Model: settings.PositionSimple, Point: "top-left"},
		Visible:  true,
		Config:   config,
	}
}

func goalConfig() map[string]any {
	return map[string]any{"segments": []any{
		map[string]any{"name": "s1", "amount": 100},
		map[string]any{"name": "s2", "amount": 50},
	}}
}

// TestWidgetGoal covers B25, B20, B23, A2 and B50: the goal grows by the
// events and the operation add_to_goal, clamps at zero, publishes the
// effects and persists the progress.
func TestWidgetGoal(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("g1", overlaywidgets.KindGoal, goalConfig()),
		}})
		defer f.stop()
		f.attach(t)
		ada := twitchUser(t, "ada")

		// A follow counts one (B25).
		f.publish(t, eventtype.ChannelFollow, ada, eventtype.Details{})
		synctest.Wait()
		ups := f.updates(t, "g1")
		require.Len(t, ups, 1)
		assert.JSONEq(t, `{"total":150,"current":1,"segments":[{"name":"s1","amount":100},{"name":"s2","amount":50}]}`, ups[0])

		// The effect is published (B23, A2).
		eff := f.effectsList()
		require.Len(t, eff, 1)
		assert.Equal(t, eventtype.OverlayGoalProgress, eff[0].typ)
		assert.Equal(t, ada, eff[0].payload.User)
		assert.Equal(t, platform.Twitch, eff[0].payload.Platform)
		assert.Equal(t, &eventtype.GoalProgress{Total: 150, Current: 1, Amount: 1, SegmentName: "s1", SegmentAmount: 100}, eff[0].payload.Details.GoalProgress)

		// The progress persists in the profile (B20).
		assert.JSONEq(t, `{"current":1,"segment":0,"reset_at":"0001-01-01T00:00:00Z"}`, string(f.stateDoc(t)))

		// A negative amount clamps the progress at zero (B50).
		require.NoError(t, f.svc.AddToGoal(f.ctx, "g1", -5, platform.Twitch, nil))
		synctest.Wait()
		ups = f.updates(t, "g1")
		require.Len(t, ups, 2)
		assert.JSONEq(t, `{"total":150,"current":0,"segments":[{"name":"s1","amount":100},{"name":"s2","amount":50}]}`, ups[1])
		eff = f.effectsList()
		require.Len(t, eff, 2)
		assert.Equal(t, eventtype.OverlayGoalProgress, eff[1].typ)
		assert.Nil(t, eff[1].payload.User)
		assert.Equal(t, &eventtype.GoalProgress{Total: 150, Current: 0, Amount: -5, SegmentName: "s1", SegmentAmount: 100}, eff[1].payload.Details.GoalProgress)
		assert.JSONEq(t, `{"current":0,"segment":0,"reset_at":"0001-01-01T00:00:00Z"}`, string(f.stateDoc(t)))

		// An amount that reaches the first segment publishes the segment
		// effect (B25); the progress carries over to the next segment.
		require.NoError(t, f.svc.AddToGoal(f.ctx, "g1", 105, platform.Twitch, ada))
		synctest.Wait()
		ups = f.updates(t, "g1")
		require.Len(t, ups, 3)
		assert.JSONEq(t, `{"total":150,"current":105,"segments":[{"name":"s1","amount":100},{"name":"s2","amount":50}]}`, ups[2])
		eff = f.effectsList()
		require.Len(t, eff, 3)
		assert.Equal(t, eventtype.OverlayGoalSegmentCompleted, eff[2].typ)
		assert.Equal(t, &eventtype.GoalSegment{SegmentName: "s1", Current: 105, NextSegmentName: "s2", NextSegmentAmount: 50, Remaining: 45}, eff[2].payload.Details.GoalSegment)
		assert.JSONEq(t, `{"current":5,"segment":1,"reset_at":"0001-01-01T00:00:00Z"}`, string(f.stateDoc(t)))
	})
}

// TestGoalCumulativeSegmentsCoversB25 covers B25: in the mode cumulative,
// a segment completes at its own amount on top of the segments before it,
// not on top of their sum (Review 344-4).
func TestGoalCumulativeSegmentsCoversB25(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cfg := map[string]any{"mode": "cumulative", "segments": []any{
			map[string]any{"name": "s1", "amount": 10},
			map[string]any{"name": "s2", "amount": 10},
		}}
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("g1", overlaywidgets.KindGoal, cfg),
		}})
		defer f.stop()
		f.attach(t)
		ada := twitchUser(t, "ada")

		// The first segment completes at its own amount.
		require.NoError(t, f.svc.AddToGoal(f.ctx, "g1", 10, platform.Twitch, ada))
		synctest.Wait()
		eff := f.effectsList()
		require.Len(t, eff, 1)
		assert.Equal(t, eventtype.OverlayGoalSegmentCompleted, eff[0].typ)
		assert.Equal(t, &eventtype.GoalSegment{SegmentName: "s1", Current: 10, NextSegmentName: "s2", NextSegmentAmount: 10, Remaining: 10}, eff[0].payload.Details.GoalSegment)

		// The second segment completes after a total of 20, not after 30
		// (Review 344-4).
		require.NoError(t, f.svc.AddToGoal(f.ctx, "g1", 10, platform.Twitch, ada))
		synctest.Wait()
		eff = f.effectsList()
		require.Len(t, eff, 2)
		assert.Equal(t, eventtype.OverlayGoalSegmentCompleted, eff[1].typ)
		assert.Equal(t, &eventtype.GoalSegment{SegmentName: "s2", Current: 20, NextSegmentName: "", NextSegmentAmount: 0, Remaining: 0}, eff[1].payload.Details.GoalSegment)
		// The progress has crossed both segments.
		assert.JSONEq(t, `{"current":0,"segment":2,"reset_at":"0001-01-01T00:00:00Z"}`, string(f.stateDoc(t)))
	})
}

// TestWidgetGoalReset covers B25: the automatic daily reset runs before
// the step, in the time zone of the profile.
func TestWidgetGoalReset(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cfg := goalConfig()
		cfg["reset"] = "daily"
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("g1", overlaywidgets.KindGoal, cfg),
		}})
		defer f.stop()
		f.attach(t)
		f.seedState("g1", `{"current":10,"segment":0,"reset_at":"2026-01-01T00:00:00Z"}`)

		f.publish(t, eventtype.ChannelFollow, twitchUser(t, "ada"), eventtype.Details{})
		synctest.Wait()
		eff := f.effectsList()
		require.Len(t, eff, 1)
		assert.Equal(t, &eventtype.GoalProgress{Total: 150, Current: 1, Amount: 1, SegmentName: "s1", SegmentAmount: 100}, eff[0].payload.Details.GoalProgress)
		var st map[string]any
		require.NoError(t, json.Unmarshal(f.stateDoc(t), &st))
		assert.Equal(t, float64(1), st["current"])
		assert.Equal(t, float64(0), st["segment"])
		// The reset ran now, so the next one is due on the next day.
		assert.True(t, strings.HasPrefix(st["reset_at"].(string), time.Now().Format("2006-01-02T")))
	})
}

// TestWidgetGoalLoaded covers B20: a new service reads the persisted
// progress, in the overall amount of the kind cumulative.
func TestWidgetGoalLoaded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("g1", overlaywidgets.KindGoal, goalConfig()),
		}})
		defer f.stop()
		f.seedState("g1", `{"current":5,"segment":1}`)
		data, err := json.Marshal(f.svc.StateForSync(f.ctx, widget("g1", overlaywidgets.KindGoal, goalConfig())))
		require.NoError(t, err)
		assert.JSONEq(t, `{"total":150,"current":105,"segments":[{"name":"s1","amount":100},{"name":"s2","amount":50}]}`, string(data))
	})
}

// TestWidgetTimer covers B26 and B38: the operations update,
// add_to_timer and pause_timer on the timer widget.
func TestWidgetTimer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("t1", overlaywidgets.KindTimer, map[string]any{"duration": "2m"}),
			widget("t2", overlaywidgets.KindTimer, nil),
		}})
		defer f.stop()
		f.attach(t)
		ctx := f.ctx

		state := func(t *testing.T) map[string]any {
			t.Helper()
			ups := f.updates(t, "t1")
			require.NotEmpty(t, ups)
			var st map[string]any
			require.NoError(t, json.Unmarshal([]byte(ups[len(ups)-1]), &st))
			return st
		}

		// update sets the new duration and runs the timer (B26).
		now := time.Now()
		require.NoError(t, f.svc.UpdateTimer(ctx, "t1", 5*time.Minute, false))
		synctest.Wait()
		// json/v2 decodes the numbers of a state into float64.
		millis := func(t *testing.T, st map[string]any, key string) float64 {
			t.Helper()
			v, ok := st[key].(float64)
			require.True(t, ok, "no %s in the state", key)
			return v
		}

		st := state(t)
		assert.Equal(t, float64(now.Add(5*time.Minute).UnixMilli()), millis(t, st, "deadline"))
		assert.Equal(t, false, st["paused"])
		assert.Equal(t, float64(0), millis(t, st, "pausedMs"))
		assert.Equal(t, "MM:SS", st["format"])

		// add_to_timer adds to the remaining time; negative is allowed
		// (B38).
		require.NoError(t, f.svc.AddToTimer(ctx, "t1", -time.Minute))
		synctest.Wait()
		assert.Equal(t, float64(now.Add(4*time.Minute).UnixMilli()), millis(t, state(t), "deadline"))

		// pause_timer switches to paused with the remaining time.
		require.NoError(t, f.svc.PauseTimer(ctx, "t1"))
		synctest.Wait()
		st = state(t)
		assert.Equal(t, true, st["paused"])
		assert.Equal(t, float64((4 * time.Minute).Milliseconds()), millis(t, st, "pausedMs"))

		// add_to_timer while paused adds to the remaining time, clamped
		// at zero.
		require.NoError(t, f.svc.AddToTimer(ctx, "t1", -10*time.Minute))
		synctest.Wait()
		assert.Equal(t, float64(0), millis(t, state(t), "pausedMs"))

		// pause_timer resumes the timer.
		require.NoError(t, f.svc.PauseTimer(ctx, "t1"))
		synctest.Wait()
		st = state(t)
		assert.Equal(t, false, st["paused"])
		assert.Equal(t, float64(now.UnixMilli()), millis(t, st, "deadline"))

		// update with reset returns to the configured duration.
		require.NoError(t, f.svc.UpdateTimer(ctx, "t1", 0, true))
		synctest.Wait()
		assert.Equal(t, float64(now.Add(2*time.Minute).UnixMilli()), millis(t, state(t), "deadline"))

		// The operations fail for a wrong duration, a widget without a
		// duration and a widget of another kind.
		assert.Error(t, f.svc.UpdateTimer(ctx, "t1", -time.Minute, false))
		assert.ErrorIs(t, f.svc.UpdateTimer(ctx, "t2", 0, true), overlaywidgets.ErrUnknownWidget)
		assert.ErrorIs(t, f.svc.AddToGoal(ctx, "t1", 1, platform.Twitch, nil), overlaywidgets.ErrUnknownWidget)
		assert.ErrorIs(t, f.svc.AddToGoal(ctx, "nope", 1, platform.Twitch, nil), overlaywidgets.ErrUnknownWidget)
	})
}

// TestWidgetEventList covers B27: the dynamic entries show before the
// feed entries, capped at the maximum.
func TestWidgetEventList(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("l1", overlaywidgets.KindEventList, map[string]any{"max": 2}),
		}})
		defer f.stop()
		f.attach(t)
		now := time.Now()
		f.setFeed([]feed.Event{
			{Platform: platform.Twitch, Type: eventtype.ChannelFollow, Login: "bob", Time: now.Add(-2 * time.Minute)},
			{Platform: platform.Twitch, Type: eventtype.TwitchBitsCheer, Login: "carol", Text: "hey", Time: now.Add(-time.Minute)},
		})

		require.NoError(t, f.svc.AddToEventList(f.ctx, "l1", "ada", "hello"))
		synctest.Wait()
		var st map[string]any
		ups := f.updates(t, "l1")
		require.Len(t, ups, 1)
		require.NoError(t, json.Unmarshal([]byte(ups[0]), &st))
		entries := st["entries"].([]any)
		require.Len(t, entries, 2)
		first, second := entries[0].(map[string]any), entries[1].(map[string]any)
		assert.Equal(t, "ada", first["user"])
		assert.Equal(t, "hello", first["text"])
		assert.Equal(t, float64(now.UnixMilli()), first["time"])
		assert.Equal(t, "carol", second["user"])
		assert.Equal(t, "twitch.bits.cheer", second["type"])
		assert.Equal(t, "hey", second["text"])

		// A new dynamic entry pushes the feed entries out at the maximum.
		require.NoError(t, f.svc.AddToEventList(f.ctx, "l1", "ada", "bye"))
		synctest.Wait()
		ups = f.updates(t, "l1")
		require.Len(t, ups, 2)
		require.NoError(t, json.Unmarshal([]byte(ups[1]), &st))
		entries = st["entries"].([]any)
		require.Len(t, entries, 2)
		assert.Equal(t, "bye", entries[0].(map[string]any)["text"])
		assert.Equal(t, "hello", entries[1].(map[string]any)["text"])
	})
}

// TestWidgetEventListTypes covers B27: the list shows only the feed types
// of its configuration.
func TestWidgetEventListTypes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("l2", overlaywidgets.KindEventList, map[string]any{"types": []any{"channel.follow"}}),
		}})
		defer f.stop()
		f.attach(t)
		now := time.Now()
		f.setFeed([]feed.Event{
			{Platform: platform.Twitch, Type: eventtype.ChannelFollow, Login: "bob", Time: now.Add(-2 * time.Minute)},
			{Platform: platform.Twitch, Type: eventtype.TwitchBitsCheer, Login: "carol", Time: now.Add(-90 * time.Second)},
			{Platform: platform.Twitch, Type: eventtype.ChannelFollow, Login: "ada", Time: now.Add(-time.Minute)},
		})

		// A follow re-reads the feed and pushes the list.
		f.publish(t, eventtype.ChannelFollow, twitchUser(t, "ada"), eventtype.Details{})
		synctest.Wait()
		ups := f.updates(t, "l2")
		require.Len(t, ups, 1)
		var st map[string]any
		require.NoError(t, json.Unmarshal([]byte(ups[0]), &st))
		entries := st["entries"].([]any)
		require.Len(t, entries, 2)
		assert.Equal(t, "ada", entries[0].(map[string]any)["user"])
		assert.Equal(t, "bob", entries[1].(map[string]any)["user"])
		for _, e := range entries {
			assert.Equal(t, "channel.follow", e.(map[string]any)["type"])
		}
	})
}

// TestWidgetChat covers B28: the chat shows the messages, the delete
// removes one by the platform message ID, and a timeout or a ban removes
// the messages of the user.
func TestWidgetChat(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("c1", overlaywidgets.KindChat, nil),
		}})
		defer f.stop()
		f.attach(t)
		ada := twitchUser(t, "ada")
		bob := twitchUser(t, "bob")

		// A message shows; the platform IDs stay out of the state.
		f.publish(t, eventtype.ChatMessage, ada, eventtype.Details{Message: &eventtype.Message{ID: "m1", Text: "hi"}})
		synctest.Wait()
		ups := f.updates(t, "c1")
		require.Len(t, ups, 1)
		assert.JSONEq(t, `{"messages":[{"user":"ada","text":"hi","platform":"twitch"}]}`, ups[0])

		// The delete removes the message by the platform ID (B28).
		f.publish(t, eventtype.ChatMessageDelete, nil, eventtype.Details{Message: &eventtype.Message{ID: "m1"}})
		synctest.Wait()
		ups = f.updates(t, "c1")
		require.Len(t, ups, 2)
		assert.JSONEq(t, `{"messages":[]}`, ups[1])

		// A timeout removes the messages of the user (B28).
		f.publish(t, eventtype.ChatMessage, ada, eventtype.Details{Message: &eventtype.Message{ID: "m2", Text: "again"}})
		f.publish(t, eventtype.ChatMessage, bob, eventtype.Details{Message: &eventtype.Message{ID: "m3", Text: "yo"}})
		f.publish(t, eventtype.ChatUserTimeout, ada, eventtype.Details{})
		synctest.Wait()
		ups = f.updates(t, "c1")
		require.Len(t, ups, 5)
		assert.JSONEq(t, `{"messages":[{"user":"bob","text":"yo","platform":"twitch"}]}`, ups[4])
	})
}

// TestWidgetLabels covers B24 and A5: the labels render on the tick, the
// value kinds resolve the sources, the date and time go to the runtime
// with the locale and the zone, and an unchanged render pushes nothing.
func TestWidgetLabels(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("lb1", overlaywidgets.KindLabel, map[string]any{"text": "hello"}),
			widget("lb2", overlaywidgets.KindLabel, map[string]any{"type": "viewer_count"}),
			widget("lb3", overlaywidgets.KindLabel, map[string]any{"type": "total_followers"}),
			widget("lb4", overlaywidgets.KindLabel, map[string]any{"type": "date"}),
			widget("lb5", overlaywidgets.KindLabel, map[string]any{"type": "latest_follower"}),
		}})
		defer f.stop()
		f.attach(t)
		viewers := int64(42)
		f.setStream(platform.Twitch, template.StreamState{Viewers: &viewers})
		f.setSession(platform.Twitch, feed.SessionStats{Followers: 7})
		f.setFeed([]feed.Event{
			{Platform: platform.Twitch, Type: eventtype.ChannelFollow, Login: "bob", Time: time.Now()},
		})

		synctest.Sleep(time.Second) // the tick re-renders the labels
		synctest.Wait()
		assert.Equal(t, []string{`{"text":"hello"}`}, f.updates(t, "lb1"))
		assert.Equal(t, []string{`{"text":"42"}`}, f.updates(t, "lb2"))
		// The platforms report no total followers; the session count
		// stands in (B24).
		assert.Equal(t, []string{`{"text":"7"}`}, f.updates(t, "lb3"))
		ups := f.updates(t, "lb4")
		require.Len(t, ups, 1)
		assert.JSONEq(t, `{"kind":"date","locale":"en-US","timeZone":"UTC"}`, ups[0])
		assert.Equal(t, []string{`{"text":"bob"}`}, f.updates(t, "lb5"))

		// A change re-renders; the unchanged labels push nothing (B24).
		viewers = 43
		f.setStream(platform.Twitch, template.StreamState{Viewers: &viewers})
		synctest.Sleep(time.Second)
		synctest.Wait()
		assert.Equal(t, []string{`{"text":"hello"}`}, f.updates(t, "lb1"))
		ups = f.updates(t, "lb2")
		require.Len(t, ups, 2)
		assert.Equal(t, `{"text":"43"}`, ups[1])
	})
}

// TestLabelOmitsTheLocalTimeZoneCoversB24: the profile time zone
// time.Local (the system zone) must not go to the runtime as
// timeZone "Local": the browser rejects the name and every throwing
// render would stop the shared per-second loop.
func TestLabelOmitsTheLocalTimeZoneCoversB24(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("lb1", overlaywidgets.KindLabel, map[string]any{"type": "time"}),
		}})
		defer f.stop()
		f.mu.Lock()
		f.loc = time.Local
		f.mu.Unlock()
		f.attach(t)

		synctest.Sleep(time.Second) // the tick re-renders the labels
		synctest.Wait()
		ups := f.updates(t, "lb1")
		require.Len(t, ups, 1)
		var st map[string]any
		require.NoError(t, json.Unmarshal([]byte(ups[0]), &st))
		assert.Equal(t, "time", st["kind"])
		_, hasZone := st["timeZone"]
		assert.False(t, hasZone, "without a zone the browser takes its own time zone")
	})
}

// TestWidgetPoll covers B34: the twitch action feeds the active vote, and
// the poll widget shows it live, in the update and in the state sync.
func TestWidgetPoll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("p1", overlaywidgets.KindPoll, nil),
		}})
		defer f.stop()
		f.attach(t)

		st := poll.State{Kind: poll.KindPoll, Name: "The vote", Outcomes: []poll.Outcome{{Name: "Yes", Votes: 3}}}
		require.NoError(t, f.svc.SetPollState(f.ctx, st))
		synctest.Wait()
		ups := f.updates(t, "p1")
		require.Len(t, ups, 1)
		// The poll state is a struct on the wire, and the toolchain's
		// json/v2 keeps the zero points of a poll outcome.
		assert.JSONEq(t, `{"kind":"poll","name":"The vote","outcomes":[{"name":"Yes","votes":3,"points":0}],"ended":false}`, ups[0])

		// The state sync carries the active vote (B4).
		assert.JSONEq(t, `{"kind":"poll","name":"The vote","outcomes":[{"name":"Yes","votes":3,"points":0}],"ended":false}`,
			marshalState(t, f.svc.StateForSync(f.ctx, widget("p1", overlaywidgets.KindPoll, nil))))
	})
}

// marshalState marshals the state of the state sync to JSON.
func marshalState(t *testing.T, state any) string {
	t.Helper()
	data, err := json.Marshal(state)
	require.NoError(t, err)
	return string(data)
}

// TestWidgetPersistentTimer covers B29: the timer starts from the initial
// duration, the event amounts and add_to_timer change it, the hidden timer
// is frozen, reset_on_enable resets, and the completion disables and
// publishes.
func TestWidgetPersistentTimer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		timerWidget := func(id string, extra map[string]any) settings.OverlayWidget {
			cfg := map[string]any{
				"initial":       "10m",
				"event_amounts": map[string]int64{"channel.follow": 60},
				"format":        "MM:SS",
			}
			maps.Copy(cfg, extra)
			return widget(id, overlaywidgets.KindPersistentTimer, cfg)
		}
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			timerWidget("t1", nil),
			timerWidget("t2", map[string]any{"reset_on_enable": true}),
			timerWidget("t3", map[string]any{"disable_on_completion": true}),
		}})
		defer f.stop()
		// The third timer is seeded with a passed deadline, before the
		// attach's state sync loads it; it completes on the first tick.
		f.seedState("t3", fmt.Sprintf(`{"deadline_ms":%d,"paused":false,"completed":false,"was_visible":true}`, time.Now().Add(-time.Minute).UnixMilli()))
		f.attach(t)

		state := func(t *testing.T, widgetID string) map[string]any {
			t.Helper()
			ups := f.updates(t, widgetID)
			require.NotEmpty(t, ups)
			var st map[string]any
			require.NoError(t, json.Unmarshal([]byte(ups[len(ups)-1]), &st))
			return st
		}
		millis := func(t *testing.T, st map[string]any, key string) float64 {
			t.Helper()
			v, ok := st[key].(float64)
			require.True(t, ok, "no %s in the state", key)
			return v
		}
		between := func(t *testing.T, got, low, high float64) {
			t.Helper()
			assert.GreaterOrEqual(t, got, low)
			assert.LessOrEqual(t, got, high)
		}

		// The new timer starts from the initial duration on the tick
		// (B29).
		now := time.Now()
		synctest.Sleep(time.Second)
		synctest.Wait()
		st := state(t, "t1")
		assert.Equal(t, false, st["paused"])
		assert.Equal(t, false, st["completed"])
		assert.Equal(t, false, st["disabled"])
		assert.Equal(t, "MM:SS", st["format"])
		between(t, millis(t, st, "deadline"), float64(now.Add(10*time.Minute).UnixMilli()), float64(now.Add(10*time.Minute+5*time.Second).UnixMilli()))

		// add_to_timer changes the deadline and publishes
		// overlay.timer.adjusted (B29, B38); negative is allowed.
		before := millis(t, state(t, "t1"), "deadline")
		require.NoError(t, f.svc.AddToPersistentTimer(f.ctx, "t1", -time.Minute))
		synctest.Wait()
		assert.Equal(t, before-60000, millis(t, state(t, "t1"), "deadline"))
		eff := f.effectsList()
		last := eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayTimerAdjusted, last.typ)
		assert.Equal(t, &eventtype.OverlayTimer{AmountChanged: -60}, last.payload.Details.OverlayTimer)
		assert.Nil(t, last.payload.User)

		// The event amounts apply to the visible timer with the user of
		// the event (B29).
		before = millis(t, state(t, "t1"), "deadline")
		ada := twitchUser(t, "ada")
		f.publish(t, eventtype.ChannelFollow, ada, eventtype.Details{})
		synctest.Wait()
		assert.Equal(t, before+60000, millis(t, state(t, "t1"), "deadline"))
		eff = f.effectsList()
		last = eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayTimerAdjusted, last.typ)
		assert.Equal(t, ada, last.payload.User)
		assert.Equal(t, &eventtype.OverlayTimer{AmountChanged: 60}, last.payload.Details.OverlayTimer)

		// The state persists in the profile (B20).
		assert.NotEmpty(t, f.stateDocFor(t, "t1"))

		// A hidden timer is frozen, and the event amounts do not reach it
		// (B29).
		sec := f.sectionNow()
		sec.Widgets[0].Visible = false
		f.setSection(sec)
		synctest.Sleep(time.Second)
		synctest.Wait()
		st = state(t, "t1")
		assert.Equal(t, true, st["paused"])
		// The frozen time is the remaining ten minutes minus the tick
		// drift: the -1m and the +60s cancel.
		between(t, millis(t, st, "pausedMs"), float64((9 * time.Minute).Milliseconds()), float64((10*time.Minute + 5*time.Second).Milliseconds()))
		before = millis(t, st, "pausedMs")
		f.publish(t, eventtype.ChannelFollow, twitchUser(t, "bob"), eventtype.Details{})
		synctest.Wait()
		assert.Equal(t, before, millis(t, state(t, "t1"), "pausedMs"))

		// Shown again, the timer resumes from the frozen time (B29).
		sec = f.sectionNow()
		sec.Widgets[0].Visible = true
		f.setSection(sec)
		synctest.Sleep(time.Second)
		synctest.Wait()
		st = state(t, "t1")
		assert.Equal(t, false, st["paused"])
		assert.Greater(t, millis(t, st, "deadline"), float64(time.Now().UnixMilli()))

		// reset_on_enable resets to the initial when the widget is shown
		// again (B29).
		sec = f.sectionNow()
		sec.Widgets[1].Visible = false
		f.setSection(sec)
		synctest.Sleep(time.Second)
		synctest.Wait()
		st = state(t, "t2")
		assert.Equal(t, true, st["paused"])
		assert.Equal(t, float64((10 * time.Minute).Milliseconds()), millis(t, st, "pausedMs"))
		sec = f.sectionNow()
		sec.Widgets[1].Visible = true
		f.setSection(sec)
		now = time.Now()
		synctest.Sleep(time.Second)
		synctest.Wait()
		st = state(t, "t2")
		assert.Equal(t, false, st["paused"])
		between(t, millis(t, st, "deadline"), float64(now.Add(10*time.Minute).UnixMilli()), float64(now.Add(10*time.Minute+5*time.Second).UnixMilli()))

		// The timer that reached zero completes, disables while
		// disable_on_completion, and published overlay.timer.completed
		// (B29).
		st = state(t, "t3")
		assert.Equal(t, true, st["completed"])
		assert.Equal(t, true, st["disabled"])
		assert.Equal(t, float64(0), millis(t, st, "pausedMs"))
		completed := false
		for _, e := range f.effectsList() {
			if e.typ == eventtype.OverlayTimerCompleted {
				completed = true
			}
		}
		assert.True(t, completed, "the completion was not published")
	})
}

// TestWidgetStreamBoss covers B30: the damage and the heal, the self-heal
// of the boss, the forced damage, the event amounts with the user of the
// event, and the new boss with the compounded health after a kill.
func TestWidgetStreamBoss(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cfg := map[string]any{
			"base_health":               100,
			"kill_bonus_health":         10,
			"overkill_bonus_multiplier": 0.5,
			"self_heal_multiplier":      2,
			"event_amounts":             map[string]int64{"channel.follow": 10},
		}
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("b1", overlaywidgets.KindStreamBoss, cfg),
		}})
		defer f.stop()
		f.attach(t)
		f.setFeed([]feed.Event{
			{Platform: platform.Twitch, Type: eventtype.ChannelFollow, Login: "bob", DisplayName: "bob", Time: time.Now()},
		})
		ada := twitchUser(t, "ada")
		bob := twitchUser(t, "bob")

		state := func(t *testing.T) map[string]any {
			t.Helper()
			ups := f.updates(t, "b1")
			require.NotEmpty(t, ups)
			var st map[string]any
			require.NoError(t, json.Unmarshal([]byte(ups[len(ups)-1]), &st))
			return st
		}

		// The damage reduces the health and publishes with the caller
		// (B30); the boss name is empty until the first kill.
		require.NoError(t, f.svc.DamageStreamBoss(f.ctx, "b1", 50, false, ada))
		synctest.Wait()
		st := state(t)
		assert.Equal(t, "", st["boss"])
		assert.Equal(t, float64(50), st["health"])
		assert.Equal(t, float64(100), st["max_health"])
		eff := f.effectsList()
		last := eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayBossDamage, last.typ)
		assert.Equal(t, ada, last.payload.User)
		assert.Equal(t, &eventtype.OverlayBoss{Health: 50, Amount: -50}, last.payload.Details.OverlayBoss)

		// The kill rolls the new boss from the feed, with the compounded
		// health (B30).
		require.NoError(t, f.svc.DamageStreamBoss(f.ctx, "b1", 50, false, ada))
		synctest.Wait()
		st = state(t)
		assert.Equal(t, "bob", st["boss"])
		assert.Equal(t, float64(110), st["health"])
		assert.Equal(t, float64(110), st["max_health"])
		eff = f.effectsList()
		last = eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayBossNew, last.typ)
		assert.Equal(t, &eventtype.OverlayBoss{Health: 110}, last.payload.Details.OverlayBoss)

		// The boss who damages himself heals with the multiplier (B30).
		require.NoError(t, f.svc.DamageStreamBoss(f.ctx, "b1", 10, false, bob))
		synctest.Wait()
		st = state(t)
		assert.Equal(t, float64(130), st["health"])
		eff = f.effectsList()
		last = eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayBossHeal, last.typ)
		assert.Equal(t, bob, last.payload.User)
		assert.Equal(t, &eventtype.OverlayBoss{Health: 130, Amount: 20}, last.payload.Details.OverlayBoss)

		// force forces the damage over the self-heal (B30).
		require.NoError(t, f.svc.DamageStreamBoss(f.ctx, "b1", 10, true, bob))
		synctest.Wait()
		assert.Equal(t, float64(120), state(t)["health"])
		eff = f.effectsList()
		last = eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayBossDamage, last.typ)
		assert.Equal(t, &eventtype.OverlayBoss{Health: 120, Amount: -10}, last.payload.Details.OverlayBoss)

		// A negative amount heals (B30, B38).
		require.NoError(t, f.svc.DamageStreamBoss(f.ctx, "b1", -20, false, ada))
		synctest.Wait()
		assert.Equal(t, float64(140), state(t)["health"])
		eff = f.effectsList()
		last = eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayBossHeal, last.typ)
		assert.Equal(t, &eventtype.OverlayBoss{Health: 140, Amount: 20}, last.payload.Details.OverlayBoss)

		// The event amounts apply with the user of the event (B30).
		f.publish(t, eventtype.ChannelFollow, ada, eventtype.Details{})
		synctest.Wait()
		assert.Equal(t, float64(130), state(t)["health"])
		eff = f.effectsList()
		last = eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayBossDamage, last.typ)
		assert.Equal(t, ada, last.payload.User)
		assert.Equal(t, &eventtype.OverlayBoss{Health: 130, Amount: -10}, last.payload.Details.OverlayBoss)

		// The overkill compounds into the next boss, and without a
		// candidate the boss name stays empty (B30).
		require.NoError(t, f.svc.DamageStreamBoss(f.ctx, "b1", 140, false, ada))
		synctest.Wait()
		st = state(t)
		assert.Equal(t, "", st["boss"])
		assert.Equal(t, float64(125), st["health"]) // 110 + 10 + 10*0.5
		assert.Equal(t, float64(125), st["max_health"])
		eff = f.effectsList()
		last = eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayBossNew, last.typ)
		assert.Equal(t, &eventtype.OverlayBoss{Health: 125}, last.payload.Details.OverlayBoss)
	})
}

// TestWidgetEndCredits covers B31: the events collect into the sections,
// the ban removes the user from every section, add_to_credits adds a
// custom entry, play_credits rolls the credits, and the stream start
// clears.
func TestWidgetEndCredits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("e1", overlaywidgets.KindEndCredits, map[string]any{"custom_sections": []string{"thanks"}}),
		}})
		defer f.stop()
		f.attach(t)
		ada := twitchUser(t, "ada")
		bob := twitchUser(t, "bob")
		carol := twitchUser(t, "carol")
		dave := twitchUser(t, "dave")
		erin := twitchUser(t, "erin")

		state := func(t *testing.T) map[string]any {
			t.Helper()
			ups := f.updates(t, "e1")
			require.NotEmpty(t, ups)
			var st map[string]any
			require.NoError(t, json.Unmarshal([]byte(ups[len(ups)-1]), &st))
			sections, ok := st["sections"].(map[string]any)
			require.True(t, ok, "no sections in the state")
			return sections
		}
		// users is the users of the section; an empty or a cleared
		// section (null) is empty.
		users := func(t *testing.T, sections map[string]any, section string) []string {
			t.Helper()
			raw, _ := sections[section].([]any)
			var out []string
			for _, e := range raw {
				m, _ := e.(map[string]any)
				if v, ok := m["user"].(string); ok {
					out = append(out, v)
				}
			}
			return out
		}

		// The events collect into their sections (B31).
		f.publish(t, eventtype.ChatMessage, ada, eventtype.Details{Message: &eventtype.Message{Text: "hi"}})
		f.publish(t, eventtype.ChannelFollow, bob, eventtype.Details{})
		f.publish(t, eventtype.ChannelSubscribe, carol, eventtype.Details{})
		f.publish(t, eventtype.ChannelRaid, ada, eventtype.Details{})
		synctest.Wait()
		sec := state(t)
		assert.Equal(t, []string{"ada"}, users(t, sec, "chatters"))
		assert.Equal(t, []string{"bob"}, users(t, sec, "new_followers"))
		assert.Equal(t, []string{"carol"}, users(t, sec, "new_subscribers"))
		assert.Equal(t, []string{"carol"}, users(t, sec, "all_subscriptions"))
		assert.Equal(t, []string{"ada"}, users(t, sec, "raids"))

		// The gift adds the gifter and the target (B31).
		env := event.New(event.Source{Kind: event.SourcePlatform, Name: string(platform.Twitch)},
			eventtype.ChannelSubscriptionGift,
			eventtype.Payload{Platform: platform.Twitch, User: dave, Target: erin, Details: eventtype.Details{}})
		require.NoError(t, f.bus.Publish(f.ctx, env))
		synctest.Wait()
		sec = state(t)
		assert.Equal(t, []string{"dave"}, users(t, sec, "gifted_subscriptions"))
		assert.Equal(t, []string{"carol", "erin"}, users(t, sec, "all_subscriptions"))

		// The timeout removes the user from every section (B31).
		f.publish(t, eventtype.ChatUserTimeout, ada, eventtype.Details{})
		synctest.Wait()
		sec = state(t)
		assert.Empty(t, users(t, sec, "chatters"))
		assert.Empty(t, users(t, sec, "raids"))

		// add_to_credits adds a custom entry, and a section it does not
		// know is a clear error (B31, B38).
		require.NoError(t, f.svc.AddToCredits(f.ctx, "e1", "thanks", "the crew"))
		synctest.Wait()
		sec = state(t)
		thanks, ok := sec["thanks"].([]any)
		require.True(t, ok, "no thanks section")
		require.Len(t, thanks, 1)
		entry, _ := thanks[0].(map[string]any)
		assert.Equal(t, "the crew", entry["text"])
		assert.ErrorIs(t, f.svc.AddToCredits(f.ctx, "e1", "nope", "x"), overlaywidgets.ErrUnknownWidget)

		// play_credits rolls the credits (B31, B38).
		require.NoError(t, f.svc.PlayCredits(f.ctx, "e1"))
		synctest.Wait()
		ups := f.updates(t, "e1")
		var last map[string]any
		require.NoError(t, json.Unmarshal([]byte(ups[len(ups)-1]), &last))
		assert.Equal(t, true, last["playing"])

		// The stream start clears the collected data, the custom entries
		// included (B31).
		f.publish(t, eventtype.ChannelStreamStart, nil, eventtype.Details{})
		synctest.Wait()
		sec = state(t)
		assert.Empty(t, users(t, sec, "new_subscribers"))
		assert.Empty(t, users(t, sec, "all_subscriptions"))
		assert.Empty(t, users(t, sec, "thanks"))
	})
}

// TestEndCreditsConcurrentAccess covers Review 347-1: the bus
// goroutine (chat events) and the action goroutines (AddToCredits, the
// state sync) run in parallel on the collected sections; without the
// lock this is a concurrent map write, which the -race detector
// reports.
func TestEndCreditsConcurrentAccess(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("e1", overlaywidgets.KindEndCredits, map[string]any{"custom_sections": []string{"thanks"}}),
		}})
		defer f.stop()
		f.attach(t)
		w := widget("e1", overlaywidgets.KindEndCredits, map[string]any{"custom_sections": []string{"thanks"}})

		var wg sync.WaitGroup
		for i := range 100 {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				name := fmt.Sprintf("user%03d", n)
				if err := f.svc.AddToCredits(f.ctx, "e1", "thanks", name); err != nil {
					t.Errorf("AddToCredits: %v", err)
				}
				f.publish(t, eventtype.ChatMessage, twitchUser(t, name),
					eventtype.Details{Message: &eventtype.Message{Text: "hi"}})
				f.svc.StateForEndCredits(f.ctx, w)
			}(i)
		}
		wg.Wait()
		synctest.Wait() // the bus goroutine processed every event
	})
}

// TestWidgetLeaderboard covers B32: the statistic mapping, the count
// default, and an unknown statistic that pushes nothing.
func TestWidgetLeaderboard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("l1", overlaywidgets.KindLeaderboard, map[string]any{"stat": "watch_time"}),
			widget("l2", overlaywidgets.KindLeaderboard, map[string]any{"stat": "messages", "count": 2}),
			widget("l3", overlaywidgets.KindLeaderboard, map[string]any{"stat": "dance"}),
		}})
		defer f.stop()
		f.attach(t)
		f.mu.Lock()
		f.top = []user.Ranked{
			{User: user.User{Identities: []user.Identity{{Platform: platform.Twitch, Login: "ada", DisplayName: "ada"}}}, Value: 300},
			{User: user.User{Identities: []user.Identity{{Platform: platform.Twitch, Login: "bob", DisplayName: "bob"}}}, Value: 120},
			{User: user.User{Identities: []user.Identity{{Platform: platform.Twitch, Login: "carol", DisplayName: "carol"}}}, Value: 40},
		}
		f.mu.Unlock()

		// The events that change the statistics refresh the leaderboard
		// (B32); the default count is ten, here all three entries.
		f.publish(t, eventtype.ChannelFollow, twitchUser(t, "ada"), eventtype.Details{})
		synctest.Wait()
		ups := f.updates(t, "l1")
		require.Len(t, ups, 1)
		assert.JSONEq(t, `{"stat":"watch_time","entries":[{"name":"ada","value":300},{"name":"bob","value":120},{"name":"carol","value":40}]}`, ups[0])

		// The count caps the entries (B32).
		ups = f.updates(t, "l2")
		require.Len(t, ups, 1)
		assert.JSONEq(t, `{"stat":"messages","entries":[{"name":"ada","value":300},{"name":"bob","value":120}]}`, ups[0])

		// The unknown statistic pushes nothing (B32).
		assert.Empty(t, f.updates(t, "l3"))
	})
}

// TestWidgetWheel covers B33: the spin with the outcome on the runtime and
// the bus, the modifier rule of a negative and a positive landing, and the
// configuration validation.
func TestWidgetWheel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w1 := widget("w1", overlaywidgets.KindWheel, map[string]any{"equal_probability": true, "outcomes": []any{map[string]any{"name": "yes"}, map[string]any{"name": "no"}}})
		w2 := widget("w2", overlaywidgets.KindWheel, map[string]any{"outcomes": []any{
			map[string]any{"name": "yes", "probability": 100.0, "modifier": -10},
			map[string]any{"name": "no", "probability": 0.0, "modifier": 10},
		}})
		w3 := widget("w3", overlaywidgets.KindWheel, map[string]any{"outcomes": []any{
			map[string]any{"name": "yes", "probability": 100.0, "modifier": 10},
			map[string]any{"name": "no", "probability": 0.0, "modifier": -10},
		}})
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{w1, w2, w3}})
		defer f.stop()
		f.attach(t)
		ada := twitchUser(t, "ada")

		// The state sync shows the outcomes with the probabilities (B33,
		// B4).
		assert.JSONEq(t, `{"outcomes":[{"name":"yes","probability":100},{"name":"no","probability":0}]}`,
			marshalState(t, f.svc.StateForSync(f.ctx, w2)))

		// The spin sends the outcome to the runtime and publishes
		// overlay.wheel.spun with the caller (B33).
		require.NoError(t, f.svc.SpinWheel(f.ctx, "w1", ada))
		synctest.Wait()
		ups := f.updates(t, "w1")
		require.Len(t, ups, 1)
		var spin map[string]any
		require.NoError(t, json.Unmarshal([]byte(ups[0]), &spin))
		rot, ok := spin["spin"].(map[string]any)
		require.True(t, ok, "no spin in the state")
		idx, name := rot["index"].(float64), rot["name"].(string)
		assert.InDelta(t, 4.0, rot["duration"], 0.001)
		want := map[float64]string{0: "yes", 1: "no"}
		assert.Equal(t, want[idx], name)
		eff := f.effectsList()
		last := eff[len(eff)-1]
		assert.Equal(t, eventtype.OverlayWheelSpun, last.typ)
		assert.Equal(t, ada, last.payload.User)
		assert.Equal(t, &eventtype.OverlayWheel{Outcome: name}, last.payload.Details.OverlayWheel)

		// A landing on a negative modifier shifts the probabilities by
		// the modifiers (B33); the 100% outcome makes the draw
		// deterministic.
		require.NoError(t, f.svc.SpinWheel(f.ctx, "w2", ada))
		synctest.Wait()
		assert.JSONEq(t, `{"outcomes":[{"name":"yes","probability":90},{"name":"no","probability":10}]}`,
			marshalState(t, f.svc.StateForSync(f.ctx, w2)))

		// A landing on a positive modifier resets to the initial
		// probabilities (B33).
		require.NoError(t, f.svc.SpinWheel(f.ctx, "w3", ada))
		synctest.Wait()
		assert.JSONEq(t, `{"outcomes":[{"name":"yes","probability":100},{"name":"no","probability":0}]}`,
			marshalState(t, f.svc.StateForSync(f.ctx, w3)))

		// The configuration validation: the probabilities must sum to
		// 100, and the modifiers to zero (B33).
		f.setSection(settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("wb", overlaywidgets.KindWheel, map[string]any{"outcomes": []any{
				map[string]any{"name": "yes", "probability": 50.0},
				map[string]any{"name": "no", "probability": 30.0},
			}}),
		}})
		assert.ErrorIs(t, f.svc.SpinWheel(f.ctx, "wb", ada), overlaywidgets.ErrUnknownWidget)
		f.setSection(settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("wb", overlaywidgets.KindWheel, map[string]any{"outcomes": []any{
				map[string]any{"name": "yes", "probability": 50.0, "modifier": 5},
				map[string]any{"name": "no", "probability": 50.0, "modifier": 5},
			}}),
		}})
		assert.ErrorIs(t, f.svc.SpinWheel(f.ctx, "wb", ada), overlaywidgets.ErrUnknownWidget)
	})
}

// TestWidgetEmoteEffect covers B35: the combo count and the timeframe
// trigger the effect, ignore_duplicates dedupes the message, and a code
// without a known emote is dropped.
func TestWidgetEmoteEffect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cfg := func(extra map[string]any) map[string]any {
			base := map[string]any{"effect": "rain", "duration": "2s", "per_emote_shown": 2}
			maps.Copy(base, extra)
			return base
		}
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("m1", overlaywidgets.KindEmoteEffect, cfg(map[string]any{"combo_count": 2})),
		}})
		defer f.stop()
		f.attach(t)
		f.mu.Lock()
		f.emoteList = []emote.Emote{{ID: "1", Code: "Kappa", Image: "https://emotes/Kappa.png"}}
		f.mu.Unlock()
		ada := twitchUser(t, "ada")
		msg := func(codes ...string) {
			t.Helper()
			f.publish(t, eventtype.ChatMessage, ada, eventtype.Details{Message: &eventtype.Message{Text: "x", Emotes: codes}})
		}

		// One emote does not reach the combo count of two (B35).
		msg("Kappa")
		synctest.Wait()
		assert.Empty(t, f.updates(t, "m1"))

		// The second emote within the timeframe triggers the effect with
		// the image of the catalog (B35).
		msg("Kappa")
		synctest.Wait()
		ups := f.updates(t, "m1")
		require.Len(t, ups, 1)
		assert.JSONEq(t, `{"effect":"rain","duration_ms":2000,"code":"Kappa","image":"https://emotes/Kappa.png","per_emote_shown":2}`, ups[0])

		// The combo starts anew after the trigger (B35).
		msg("Kappa")
		synctest.Wait()
		ups = f.updates(t, "m1")
		require.Len(t, ups, 1)

		// The timeframe expires the occurrences: after the window the
		// message is only one occurrence again (B35).
		synctest.Sleep(11 * time.Second)
		synctest.Wait()
		msg("Kappa")
		synctest.Wait()
		ups = f.updates(t, "m1")
		require.Len(t, ups, 1)

		// ignore_duplicates counts the emote once per message (B35): the
		// doubled message is one occurrence, the next one triggers. The
		// section switches to the fresh deduping widget for these steps;
		// the tick reloads the section in the overlay service.
		f.setSection(settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("m4", overlaywidgets.KindEmoteEffect, cfg(map[string]any{"combo_count": 2, "ignore_duplicates": true})),
		}})
		synctest.Sleep(time.Second)
		synctest.Wait()
		msg("Kappa", "Kappa")
		synctest.Wait()
		assert.Empty(t, f.updates(t, "m4"))
		msg("Kappa")
		synctest.Wait()
		ups = f.updates(t, "m4")
		require.Len(t, ups, 1)
		assert.JSONEq(t, `{"effect":"rain","duration_ms":2000,"code":"Kappa","image":"https://emotes/Kappa.png","per_emote_shown":2}`, ups[0])

		// A code without a known emote is dropped, even with a reached
		// combo (B35).
		msg("Nope")
		msg("Nope")
		synctest.Wait()
		ups = f.updates(t, "m4")
		require.Len(t, ups, 1)
	})
}

// TestWidgetGameQueue covers B36: the join and the leave with the
// positions, the duplicate and the full queue, and the persistence of the
// queue of the profile.
func TestWidgetGameQueue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("q1", overlaywidgets.KindGameQueue, map[string]any{"max_size": 2}),
		}})
		defer f.stop()
		f.attach(t)
		ada := twitchUser(t, "ada")
		bob := twitchUser(t, "bob")
		carol := twitchUser(t, "carol")

		// The join adds the user at the end and publishes
		// gamequeue.joined with the position (B36).
		require.NoError(t, f.svc.JoinGameQueue(f.ctx, ada))
		synctest.Wait()
		ups := f.updates(t, "q1")
		require.Len(t, ups, 1)
		assert.JSONEq(t, `{"entries":[{"user":"ada","position":1}]}`, ups[0])
		eff := f.effectsList()
		last := eff[len(eff)-1]
		assert.Equal(t, eventtype.GameQueueJoined, last.typ)
		assert.Equal(t, ada, last.payload.User)
		assert.Equal(t, &eventtype.GameQueue{Position: 1}, last.payload.Details.GameQueue)

		// A second user takes the second position; a duplicate and the
		// queue beyond the max size are clear errors (B36).
		require.NoError(t, f.svc.JoinGameQueue(f.ctx, bob))
		synctest.Wait()
		assert.JSONEq(t, `{"entries":[{"user":"ada","position":1},{"user":"bob","position":2}]}`, f.updates(t, "q1")[1])
		assert.Error(t, f.svc.JoinGameQueue(f.ctx, ada))
		assert.Error(t, f.svc.JoinGameQueue(f.ctx, carol))

		// The leave removes the user and publishes gamequeue.left with
		// the position it left (B36).
		require.NoError(t, f.svc.LeaveGameQueue(f.ctx, ada))
		synctest.Wait()
		assert.JSONEq(t, `{"entries":[{"user":"bob","position":1}]}`, f.updates(t, "q1")[2])
		eff = f.effectsList()
		last = eff[len(eff)-1]
		assert.Equal(t, eventtype.GameQueueLeft, last.typ)
		assert.Equal(t, &eventtype.GameQueue{Position: 1}, last.payload.Details.GameQueue)
		assert.Error(t, f.svc.LeaveGameQueue(f.ctx, carol))

		// The queue persists in the profile under its own key (B20,
		// B36).
		assert.JSONEq(t, `{"entries":[{"user":"bob"}]}`, string(f.stateDocFor(t, "game_queue")))
	})
}

// TestWidgetDiscordVoice covers B37: the discord.voice events refresh the
// occupants on the runtime, and the state sync carries them.
func TestWidgetDiscordVoice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("d1", overlaywidgets.KindDiscordVoice, nil),
		}})
		defer f.stop()
		f.attach(t)

		f.mu.Lock()
		f.discordOcc = []discord.Occupant{{Name: "alice", Channel: "c1"}}
		f.mu.Unlock()
		env := event.New(event.Source{Kind: event.SourceIntegration, Name: "discord"},
			eventtype.DiscordVoiceJoined,
			eventtype.Payload{Details: eventtype.Details{DiscordVoice: &eventtype.DiscordVoice{Name: "alice", Channel: "c1"}}})
		require.NoError(t, f.bus.Publish(f.ctx, env))
		synctest.Wait()
		ups := f.updates(t, "d1")
		require.Len(t, ups, 1)
		assert.JSONEq(t, `{"occupants":[{"name":"alice","channel":"c1"}]}`, ups[0])

		// A change refreshes the occupants, and the state sync carries
		// them (B37, B4).
		f.mu.Lock()
		f.discordOcc = []discord.Occupant{
			{Name: "alice", Channel: "c1"},
			{Name: "bob", Channel: "c1"},
		}
		f.mu.Unlock()
		env = event.New(event.Source{Kind: event.SourceIntegration, Name: "discord"},
			eventtype.DiscordVoiceJoined,
			eventtype.Payload{Details: eventtype.Details{DiscordVoice: &eventtype.DiscordVoice{Name: "bob", Channel: "c1"}}})
		require.NoError(t, f.bus.Publish(f.ctx, env))
		synctest.Wait()
		ups = f.updates(t, "d1")
		require.Len(t, ups, 2)
		assert.JSONEq(t, `{"occupants":[{"name":"alice","channel":"c1"},{"name":"bob","channel":"c1"}]}`, ups[1])
		assert.JSONEq(t, `{"occupants":[{"name":"alice","channel":"c1"},{"name":"bob","channel":"c1"}]}`,
			marshalState(t, f.svc.StateForSync(f.ctx, widget("d1", overlaywidgets.KindDiscordVoice, nil))))
	})
}

// TestWidgetStateSync covers B4: a (re)connecting runtime receives the
// current state of the widgets in the handshake.
func TestWidgetStateSync(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.Overlay{Widgets: []settings.OverlayWidget{
			widget("g1", overlaywidgets.KindGoal, goalConfig()),
			widget("t1", overlaywidgets.KindTimer, map[string]any{"duration": "2m"}),
		}})
		defer f.stop()
		f.seedState("g1", `{"current":30,"segment":0}`)
		f.attach(t)

		states := f.handshakeStates(t)
		assert.JSONEq(t, `{"total":150,"current":30,"segments":[{"name":"s1","amount":100},{"name":"s2","amount":50}]}`, states["g1"])
		var timer map[string]any
		require.NoError(t, json.Unmarshal([]byte(states["t1"]), &timer))
		assert.Equal(t, true, timer["paused"])
		assert.Equal(t, float64((2 * time.Minute).Milliseconds()), timer["pausedMs"])
		assert.Equal(t, "MM:SS", timer["format"])
	})
}

//go:fix inline
