// SPDX-License-Identifier: MIT

// The behavior tests of the widget service (roadmap 7.2, spec
// overlays.md, B24-B28, B50): the goal with its persistence and events,
// the timer operations, the event list, the chat and the labels, as the
// runtime and the bus see them.
package overlaywidgets_test

import (
	"context"
	"encoding/json/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/feed"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/overlaywidgets"
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

	mu       sync.Mutex
	sec      settings.Overlay
	loc      *time.Location // nil for the profile default (UTC)
	msgs     []overlay.Message
	effects  []effect
	state    map[string][]byte
	feed     []feed.Event
	streams  map[platform.Name]template.StreamState
	sessions map[platform.Name]feed.SessionStats
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
		Overlay:      ov,
		Bus:          f.bus,
		Section:      f.section,
		Feed:         f,
		State:        f,
		Templates:    template.New(reg),
		Scope:        f.scope,
		StreamStates: f,
		SessionStats: f,
		Locale:       f.locale,
	}, overlaywidgets.WithLabelTick(time.Second))
	require.NoError(t, err)
	ov.SetStateProvider(f.svc.StateForSync)
	f.runWG.Go(func() {
		_ = f.svc.Run(f.ctx)
	})
	synctest.Wait() // Run registers its subscription before the test publishes
	sub := f.bus.Subscribe(f.ctx,
		event.WithName("fixture"),
		event.WithTypes(eventtype.OverlayGoalProgress, eventtype.OverlayGoalSegmentCompleted))
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
