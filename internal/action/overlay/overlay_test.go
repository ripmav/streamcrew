// SPDX-License-Identifier: MIT

package overlay_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	overlayaction "github.com/ripmav/streamcrew/internal/action/overlay"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/emote"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
)

// removedItem is a removal the fake overlay service recorded: the ID of
// the item and whether the context of the call was canceled.
type removedItem struct {
	id       string
	canceled bool
}

// fakeOverlay is a fake of the overlay service the action runs on: it
// records the shown items, the removals and the widget operations and
// serves the end channels of the items from ends.
type fakeOverlay struct {
	mu      sync.Mutex
	items   []overlay.Item
	ends    []chan struct{}
	removed []removedItem
	visible map[string]bool
	updated map[string]any
	widgets map[string]settings.OverlayWidget
	// showAt is when each item was shown, for the media gate (B43).
	showAt []time.Time
	next   int
}

func newFakeOverlay() *fakeOverlay {
	return &fakeOverlay{
		visible: map[string]bool{},
		updated: map[string]any{},
		widgets: map[string]settings.OverlayWidget{},
	}
}

// ShowItem implements the action/overlay service port; it returns the
// next end channel, which the test closes to end the item.
func (f *fakeOverlay) ShowItem(_ context.Context, _ string, item overlay.Item) (string, <-chan struct{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, item)
	f.showAt = append(f.showAt, time.Now())
	end := make(chan struct{})
	f.ends = append(f.ends, end)
	id := fmt.Sprintf("item-%d", f.next)
	f.next++
	return id, end, nil
}

// RemoveItem implements the action/overlay service port; it records the
// removal and whether the context of the call was canceled.
func (f *fakeOverlay) RemoveItem(ctx context.Context, _, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, removedItem{id: id, canceled: ctx.Err() != nil})
	return true
}

// SetVisible implements the action/overlay service port.
func (f *fakeOverlay) SetVisible(_ context.Context, widgetID string, visible bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.visible[widgetID] = visible
	return nil
}

// Widget implements the action/overlay service port.
func (f *fakeOverlay) Widget(widgetID string) (settings.OverlayWidget, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.widgets[widgetID]
	return w, ok
}

// UpdateWidget implements the action/overlay service port.
func (f *fakeOverlay) UpdateWidget(_ context.Context, widgetID string, state any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated[widgetID] = state
	return nil
}

// end closes the end channel of the i-th shown item.
func (f *fakeOverlay) end(t *testing.T, i int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Less(t, i, len(f.ends), "the item %d is not shown", i)
	close(f.ends[i])
}

func (f *fakeOverlay) Item(t *testing.T, i int) overlay.Item {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Less(t, i, len(f.items), "the item %d is not shown", i)
	return f.items[i]
}

// ShowAt returns when the i-th item was shown.
func (f *fakeOverlay) ShowAt(t *testing.T, i int) time.Time {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Less(t, i, len(f.showAt), "the item %d is not shown", i)
	return f.showAt[i]
}

// count is the number of the shown items.
func (f *fakeOverlay) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.items)
}

// fakeWidgets is a fake of the widget operations the action runs.
type fakeWidgets struct {
	mu      sync.Mutex
	goals   []string
	timers  []string
	adds    []string
	pauses  int
	lists   []string
	credits []string
	wheels  []string
	bosses  []string
}

func (f *fakeWidgets) AddToGoal(_ context.Context, widgetID string, amount int64, p platform.Name, _ *user.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.goals = append(f.goals, fmt.Sprintf("%s:%d:%s", widgetID, amount, p))
	return nil
}

func (f *fakeWidgets) UpdateTimer(_ context.Context, widgetID string, d time.Duration, reset bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timers = append(f.timers, fmt.Sprintf("%s:%s:%v", widgetID, d, reset))
	return nil
}

func (f *fakeWidgets) AddToTimer(_ context.Context, widgetID string, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adds = append(f.adds, fmt.Sprintf("%s:%s", widgetID, d))
	return nil
}

func (f *fakeWidgets) PauseTimer(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauses++
	return nil
}

func (f *fakeWidgets) AddToEventList(_ context.Context, widgetID, userName, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists = append(f.lists, fmt.Sprintf("%s:%s:%s", widgetID, userName, text))
	return nil
}

func (f *fakeWidgets) AddToCredits(_ context.Context, widgetID, section, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credits = append(f.credits, fmt.Sprintf("%s:%s:%s", widgetID, section, text))
	return nil
}

func (f *fakeWidgets) PlayCredits(_ context.Context, widgetID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credits = append(f.credits, "play:"+widgetID)
	return nil
}

func (f *fakeWidgets) SpinWheel(_ context.Context, widgetID string, _ *user.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wheels = append(f.wheels, widgetID)
	return nil
}

func (f *fakeWidgets) DamageStreamBoss(_ context.Context, widgetID string, amount int64, force bool, _ *user.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bosses = append(f.bosses, fmt.Sprintf("%s:%d:%t", widgetID, amount, force))
	return nil
}

// fixture wires the overlay type with the fakes and the note journal into
// a running engine.
type fixture struct {
	h       *actiontest.Harness
	reg     *action.Registry
	eng     *template.Engine
	overlay *fakeOverlay
	// real is the real overlay service the fixture runs on, nil while the
	// fake serves (Review 344-5).
	real    *overlay.Service
	widgets *fakeWidgets
	journal *actiontest.Journal
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, newFakeOverlay())
}

// newRealFixture is newFixture with the real overlay service, so the item
// timers of the service run (Review 344-5).
func newRealFixture(t *testing.T) *fixture {
	t.Helper()
	svc, err := overlay.New(t.Context(), overlay.Ports{
		Section: func(context.Context) (settings.Overlay, error) { return settings.Overlay{}, nil },
		Version: "test",
	})
	require.NoError(t, err)
	f := newFixtureWith(t, svc)
	f.real = svc
	return f
}

func newFixtureWith(t *testing.T, svc overlayaction.Service) *fixture {
	t.Helper()
	reg, _ := template.NewRegistry()
	eng := template.New(reg)
	fake, _ := svc.(*fakeOverlay)
	fakeWidgets := &fakeWidgets{}
	journal := &actiontest.Journal{}
	ports := overlayaction.Ports{
		Templates: eng,
		Overlay:   svc,
		Widgets:   fakeWidgets,
		Clips:     fakeClips{clip: overlayaction.Clip{URL: "https://clips.twitch.tv/Clip", Duration: 30 * time.Second}},
		Emotes:    testEmotes,
		Logger:    slog.New(slog.DiscardHandler),
	}
	descs, err := overlayaction.Descriptors(ports)
	require.NoError(t, err)
	registry, err := action.NewRegistry(capability.Set{}, descs...)
	require.NoError(t, err)
	h := actiontest.NewHarness(t, registry)
	return &fixture{h: h, reg: registry, eng: eng, overlay: fake, widgets: fakeWidgets, journal: journal}
}

// ov makes an overlay action of kind with the fixture's ports.
func (f *fixture) ov(t *testing.T, kind overlayaction.Kind, mutate func(a *overlayaction.Overlay)) overlayaction.Overlay {
	t.Helper()
	d, ok := f.reg.Descriptor(overlayaction.TypeOverlay)
	require.True(t, ok)
	a, ok := d.New().(overlayaction.Overlay)
	require.True(t, ok)
	a.Kind = kind
	switch kind {
	case overlayaction.KindItem:
		a.Item = &overlayaction.ItemOptions{
			Position: settings.Position{Model: settings.PositionSimple, Point: "top-left"},
			Content:  map[string]any{"kind": "text", "text": "hi"},
		}
	case overlayaction.KindWidget:
		a.Widget = &overlayaction.WidgetOptions{Widget: "w1", Op: overlayaction.OpShow}
	}
	mutate(&a)
	return a
}

// greet triggers cmd as a greeting and returns its instance.
func (f *fixture) greet(t *testing.T, cmd command.Command) id.ID {
	t.Helper()
	res, err := f.h.Engine().Trigger(t.Context(), engine.Request{Command: cmd, Source: engine.SourceChat, Entrance: true})
	require.NoError(t, err)
	require.Equal(t, engine.OutcomeQueued, res.Outcome)
	return res.Instances[0]
}

// fakeClips is a fake clip resolver.
type fakeClips struct {
	clip overlayaction.Clip
	err  error
}

func (f fakeClips) LatestClip(_ context.Context, _ string) (overlayaction.Clip, error) {
	return f.clip, f.err
}

// testEmotes is the Emotes port of the fixture: the catalog the emote
// effect resolves over.
func testEmotes(context.Context) []emote.Emote {
	return []emote.Emote{
		{ID: "e1", Code: "Kappa", Image: "https://emotes/Kappa.png"},
		{ID: "e2", Code: "Pog", Image: "https://emotes/Pog.png"},
	}
}

// overlayAction makes an overlay action of kind without ports, for the
// validation tests.
func overlayAction(kind overlayaction.Kind) overlayaction.Overlay {
	a := overlayaction.Overlay{Common: action.On(), Kind: kind}
	switch kind {
	case overlayaction.KindItem:
		a.Item = &overlayaction.ItemOptions{
			Position: settings.Position{Model: settings.PositionSimple, Point: "top-left"},
			Content:  map[string]any{"kind": "text", "text": "hi"},
		}
	case overlayaction.KindWidget:
		a.Widget = &overlayaction.WidgetOptions{Widget: "w1", Op: overlayaction.OpShow}
	}
	return a
}

func TestOverlayValidate(t *testing.T) {
	cases := []struct {
		name   string
		kind   overlayaction.Kind
		mutate func(a *overlayaction.Overlay)
		ok     bool
	}{
		{name: "item valid", kind: overlayaction.KindItem, ok: true},
		{name: "item unknown kind", mutate: func(a *overlayaction.Overlay) { a.Kind = "nope" }, ok: false},
		{name: "item without options", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) { a.Item = nil }, ok: false},
		{name: "item unknown item kind", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "nope"}
		}, ok: false},
		{name: "item text without text", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "text"}
		}, ok: false},
		{name: "item text with a member of another kind", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "text", "text": "hi", "url": "x"}
		}, ok: false},
		{name: "item image without src", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "image"}
		}, ok: false},
		{name: "item youtube without size", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "youtube", "url": "https://youtu.be/x"}
		}, ok: false},
		{name: "item youtube valid", kind: overlayaction.KindItem, ok: true, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "youtube", "url": "https://youtu.be/x", "width": 640, "height": 360}
		}},
		{name: "item clip with url and user", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "clip", "url": "x", "user": "y"}
		}, ok: false},
		{name: "item clip without url and user", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "clip"}
		}, ok: false},
		{name: "item timer without duration", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "timer"}
		}, ok: false},
		{name: "item timer with bad duration", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "timer", "duration": "soon"}
		}, ok: false},
		{name: "item with unknown animation", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Animation = "warp"
		}, ok: false},
		{name: "item with negative duration", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Duration = polydoc.Duration(-10 * time.Second)
		}, ok: false},
		{name: "item with percent out of range", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Position = settings.Position{Model: settings.PositionPercent, X: 101, Y: 0}
		}, ok: false},
		{name: "widget valid", kind: overlayaction.KindWidget, ok: true},
		{name: "widget without options", kind: overlayaction.KindWidget, mutate: func(a *overlayaction.Overlay) { a.Widget = nil }, ok: false},
		{name: "widget with empty ID", kind: overlayaction.KindWidget, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Widget = ""
		}, ok: false},
		{name: "widget with unknown operation", kind: overlayaction.KindWidget, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Op = "not_an_operation"
		}, ok: false},
		{name: "widget spin_wheel valid", kind: overlayaction.KindWidget, ok: true, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Op = overlayaction.OpSpinWheel
		}},
		{name: "widget spin_wheel with amount", kind: overlayaction.KindWidget, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Op = overlayaction.OpSpinWheel
			a.Widget.Amount = action.Fixed(decimal.New(1))
		}, ok: false},
		{name: "widget play_credits valid", kind: overlayaction.KindWidget, ok: true, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Op = overlayaction.OpPlayCredits
		}},
		{name: "widget add_to_credits valid", kind: overlayaction.KindWidget, ok: true, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Op = overlayaction.OpAddToCredits
			a.Widget.Data = map[string]any{"section": "chatters"}
			a.Widget.Text = "thanks"
		}},
		{name: "widget damage_stream_boss valid", kind: overlayaction.KindWidget, ok: true, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Op = overlayaction.OpDamageStreamBoss
			a.Widget.Amount = action.Fixed(decimal.New(50))
			a.Widget.Data = map[string]any{"force": true}
		}},
		{name: "widget damage_stream_boss without amount", kind: overlayaction.KindWidget, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Op = overlayaction.OpDamageStreamBoss
		}, ok: false},
		{name: "item emote_effect valid", kind: overlayaction.KindItem, ok: true, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "emote_effect", "emotes": []string{"Kappa"}}
		}},
		{name: "item emote_effect without emotes", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "emote_effect"}
		}, ok: false},
		{name: "item emote_effect with unknown effect", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Item.Content = map[string]any{"kind": "emote_effect", "emotes": []string{"Kappa"}, "effect": "warp"}
		}, ok: false},
		{name: "widget add_to_goal without amount", kind: overlayaction.KindWidget, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Op = overlayaction.OpAddToGoal
		}, ok: false},
		{name: "widget show with data", kind: overlayaction.KindWidget, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Data = map[string]any{"x": 1}
		}, ok: false},
		{name: "widget add_to_event_list with text", kind: overlayaction.KindWidget, ok: true, mutate: func(a *overlayaction.Overlay) {
			a.Widget.Op = overlayaction.OpAddToEventList
			a.Widget.Text = "goal reached"
		}},
		{name: "item with widget options", kind: overlayaction.KindItem, mutate: func(a *overlayaction.Overlay) {
			a.Widget = &overlayaction.WidgetOptions{Widget: "w1", Op: overlayaction.OpShow}
		}, ok: false},
		{name: "widget with item options", kind: overlayaction.KindWidget, mutate: func(a *overlayaction.Overlay) {
			a.Item = &overlayaction.ItemOptions{Content: map[string]any{"kind": "text", "text": "hi"}}
		}, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := overlayAction(tc.kind)
			if tc.mutate != nil {
				tc.mutate(&a)
			}
			err := a.Validate()
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestOverlayItemWaitsForEnd covers B11 and B14: the action of the kind
// item is done when the item leaves the active set, and the end channel
// of the overlay service carries the end.
func TestOverlayItemWaitsForEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		cmd := f.h.Command("show",
			f.journal.Note("before"),
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{"kind": "text", "text": "hello"}
			}),
			f.journal.Note("after"),
		)
		in := f.h.Start(cmd, engine.Params{})
		assert.Equal(t, engine.StateRunning, f.h.Instance(in.ID).State)
		f.overlay.end(t, 0)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		assert.Equal(t, []string{"before", "after"}, f.journal.Lines())
		item := f.overlay.Item(t, 0)
		assert.Equal(t, "text", item.Kind)
		assert.Equal(t, map[string]any{"text": "hello", "maxWidth": float64(0)}, item.Content)
	})
}

// TestOverlayItemInGreeting covers B11, B39 and B43: the item of a
// greeting holds the media gate until the item leaves the active set,
// and the item of the next greeting shows the gap after that.
func TestOverlayItemInGreeting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)

		// The first greeting shows the audio item; the playback runs 8 s.
		in1 := f.greet(t, f.h.Command("g1",
			f.journal.Note("g1"),
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{"kind": "audio", "src": "/media/a.mp3"}
			}),
		))
		synctest.Wait()
		synctest.Sleep(8 * time.Second)
		f.overlay.end(t, 0)
		synctest.Wait()

		// The second greeting shows an item; the default gap is 5 s
		// (settings), so its item starts 5 s after the first ended.
		in2 := f.greet(t, f.h.Command("g2",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{"kind": "text", "text": "g2"}
			}),
			f.journal.Note("g2"),
		))
		synctest.Sleep(14 * time.Second)
		f.overlay.end(t, 1)
		synctest.Wait()

		g1 := f.h.Instance(in1)
		g2 := f.h.Instance(in2)
		assert.Equal(t, engine.StateCompleted, g1.State)
		assert.Equal(t, engine.StateCompleted, g2.State)
		// B43: the item of the second greeting shows 5 s, the default
		// gap, after the playback of the first ended. The instances
		// start at once; only the action waits for the gate.
		assert.Equal(t, 5*time.Second, f.overlay.ShowAt(t, 1).Sub(g1.EndedAt))
		assert.Equal(t, []string{"g1", "g2"}, f.journal.Lines())
	})
}

// TestOverlayClipItem covers B13 and B16: the clip item by user resolves
// the latest clip of the user, and its duration lands in the content.
func TestOverlayClipItem(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		cmd := f.h.Command("clip",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{"kind": "clip", "user": "streamer"}
			}),
		)
		in := f.h.Start(cmd, engine.Params{})
		f.overlay.end(t, 0)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		item := f.overlay.Item(t, 0)
		assert.Equal(t, "clip", item.Kind)
		assert.Equal(t, map[string]any{"url": "https://clips.twitch.tv/Clip", "duration": 30}, item.Content)
	})
}

// TestOverlayClipItemRejectsForeignURLs covers Review 344-6s: a clip
// URL that is not a Twitch clip page is a clear error of the action, so
// a template rendered from the chat cannot reach the runtime.
func TestOverlayClipItemRejectsForeignURLs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		url  string
		ok   bool
	}{
		{name: "javascript scheme", url: "javascript:alert(1)"},
		{name: "foreign host", url: "https://evil.example"},
		{name: "http clip page", url: "http://clips.twitch.tv/Foo-Bar_9"},
		{name: "lookalike host", url: "https://clips.twitch.tv.evil.example/Foo"},
		{name: "clips page without slug", url: "https://clips.twitch.tv/"},
		{name: "channel page without clip", url: "https://www.twitch.tv/kanal"},
		{name: "clip page without slug", url: "https://www.twitch.tv/kanal/clip"},
		{name: "clips page", url: "https://clips.twitch.tv/Foo-Bar_9", ok: true},
		{name: "www clip page", url: "https://www.twitch.tv/kanal/clip/Foo-Bar_9", ok: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t)
				cmd := f.h.Command("clip",
					f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
						a.Item.Content = map[string]any{"kind": "clip", "url": tc.url}
					}),
				)
				// The error policy aborts, so the failure of the action
				// ends the instance (B71).
				cmd.ErrorPolicy = command.ErrorAbort
				f.h.Put(cmd)
				in := f.h.Start(cmd, engine.Params{})
				if tc.ok {
					assert.Equal(t, engine.StateRunning, in.State)
					require.NoError(t, f.h.Engine().Cancel(t.Context(), in.ID))
					synctest.Wait()
				} else {
					assert.Equal(t, engine.StateFailed, in.State)
				}
			})
		})
	}
}

// TestOverlayItemWithoutADurationFallsAfterTheDefault covers B11 and
// Review 344-5: an item without a duration shows for the default of
// 10 s, a clip by user for the duration of the clip, and audio and video
// run until item.ended (B14).
func TestOverlayItemWithoutADurationFallsAfterTheDefault(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newRealFixture(t)

		// A text item without a duration falls after the default of 10 s.
		cmd := f.h.Command("text",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{"kind": "text", "text": "hello"}
			}),
		)
		in := f.h.Start(cmd, engine.Params{})
		synctest.Sleep(9 * time.Second)
		synctest.Wait()
		assert.Equal(t, engine.StateRunning, f.h.Instance(in.ID).State)
		synctest.Sleep(time.Second)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		assert.Empty(t, f.real.ActiveItems(settings.DefaultEndpointID))

		// A clip by user shows for the duration of the clip (30 s of the
		// fake), not the default (Review 344-5).
		cmd = f.h.Command("clip",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{"kind": "clip", "user": "streamer"}
			}),
		)
		in = f.h.Start(cmd, engine.Params{})
		synctest.Sleep(29 * time.Second)
		synctest.Wait()
		assert.Equal(t, engine.StateRunning, f.h.Instance(in.ID).State)
		synctest.Sleep(time.Second)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		assert.Empty(t, f.real.ActiveItems(settings.DefaultEndpointID))

		// An audio item without a duration runs until item.ended (B14);
		// the default does not apply to the playback.
		cmd = f.h.Command("audio",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{"kind": "audio", "src": "/media/a.mp3"}
			}),
		)
		in = f.h.Start(cmd, engine.Params{})
		synctest.Sleep(10 * time.Second)
		synctest.Wait()
		assert.Equal(t, engine.StateRunning, f.h.Instance(in.ID).State)
		items := f.real.ActiveItems(settings.DefaultEndpointID)
		require.Len(t, items, 1)
		require.True(t, f.real.ItemEnded(t.Context(), settings.DefaultEndpointID, items[0].ID))
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		assert.Empty(t, f.real.ActiveItems(settings.DefaultEndpointID))
	})
}

// TestOverlayInterruptedActionRemovesItsItem covers Review 344-5: an
// interrupted action removes its item from the active set, so a
// reconnect does not resend it; the removal runs without the canceled
// context of the action (context.WithoutCancel).
func TestOverlayInterruptedActionRemovesItsItem(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// The real service: the item leaves the active set.
		f := newRealFixture(t)
		cmd := f.h.Command("audio",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{"kind": "audio", "src": "/media/a.mp3"}
			}),
		)
		in := f.h.Start(cmd, engine.Params{})
		assert.Equal(t, engine.StateRunning, in.State)
		items := f.real.ActiveItems(settings.DefaultEndpointID)
		require.Len(t, items, 1)
		require.NoError(t, f.h.Engine().Cancel(t.Context(), in.ID))
		synctest.Wait()
		assert.Equal(t, engine.StateCanceled, f.h.Instance(in.ID).State)
		assert.Empty(t, f.real.ActiveItems(settings.DefaultEndpointID))

		// The fake service: the removal arrives without a canceled
		// context.
		fake := newFixture(t)
		cmd = fake.h.Command("audio",
			fake.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{"kind": "audio", "src": "/media/a.mp3"}
			}),
		)
		in = fake.h.Start(cmd, engine.Params{})
		require.NoError(t, fake.h.Engine().Cancel(t.Context(), in.ID))
		synctest.Wait()
		assert.Equal(t, engine.StateCanceled, fake.h.Instance(in.ID).State)
		require.Len(t, fake.overlay.removed, 1)
		assert.Equal(t, "item-0", fake.overlay.removed[0].id)
		assert.False(t, fake.overlay.removed[0].canceled)
	})
}

// TestOverlayWidgetOps covers B21 to B23 and B38: the operations of the
// kind widget run on the overlay service and the widget operations.
func TestOverlayWidgetOps(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.overlay.widgets["t1"] = settings.OverlayWidget{ID: "t1", Kind: "timer"}
		f.overlay.widgets["l1"] = settings.OverlayWidget{ID: "l1", Kind: "label"}
		cmd := f.h.Command("ops",
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Op = overlayaction.OpShow
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "t1"
				a.Widget.Op = overlayaction.OpUpdate
				a.Widget.Data = map[string]any{"duration": "5m"}
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "t1"
				a.Widget.Op = overlayaction.OpAddToTimer
				a.Widget.Amount = action.Fixed(decimal.New(120))
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "t1"
				a.Widget.Op = overlayaction.OpPauseTimer
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "t1"
				a.Widget.Op = overlayaction.OpAddToGoal
				a.Widget.Amount = action.Fixed(decimal.New(-3))
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "l1"
				a.Widget.Op = overlayaction.OpUpdate
				a.Widget.Data = map[string]any{"text": "new"}
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "l1"
				a.Widget.Op = overlayaction.OpAddToEventList
				a.Widget.Text = "hello"
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "e1"
				a.Widget.Op = overlayaction.OpAddToCredits
				a.Widget.Data = map[string]any{"section": "thanks"}
				a.Widget.Text = "the crew"
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "e1"
				a.Widget.Op = overlayaction.OpPlayCredits
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "w1"
				a.Widget.Op = overlayaction.OpSpinWheel
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "b1"
				a.Widget.Op = overlayaction.OpDamageStreamBoss
				a.Widget.Amount = action.Fixed(decimal.New(50))
				a.Widget.Data = map[string]any{"force": true}
			}),
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Op = overlayaction.OpHide
			}),
		)
		in := f.h.Start(cmd, engine.Params{})
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		assert.Equal(t, []string{"t1:5m0s:false"}, f.widgets.timers)
		assert.Equal(t, []string{"t1:2m0s"}, f.widgets.adds)
		assert.Equal(t, 1, f.widgets.pauses)
		assert.Equal(t, []string{"t1:-3:"}, f.widgets.goals)
		assert.Equal(t, []string{"l1::hello"}, f.widgets.lists)
		assert.Equal(t, []string{"e1:thanks:the crew", "play:e1"}, f.widgets.credits)
		assert.Equal(t, []string{"w1"}, f.widgets.wheels)
		assert.Equal(t, []string{"b1:50:true"}, f.widgets.bosses)
		assert.Equal(t, map[string]any{"text": "new"}, f.overlay.updated["l1"])
		assert.Equal(t, map[string]bool{"w1": false}, f.overlay.visible)

		// add_to_credits without the section in the data is a clear error,
		// the command continues without the widget call (B31, B38).
		missing := f.h.Command("nocredits",
			f.ov(t, overlayaction.KindWidget, func(a *overlayaction.Overlay) {
				a.Widget.Widget = "e1"
				a.Widget.Op = overlayaction.OpAddToCredits
				a.Widget.Text = "x"
			}),
		)
		in = f.h.Start(missing, engine.Params{})
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		assert.Len(t, f.widgets.credits, 2)
	})
}

// TestOverlayEmoteEffectItem covers B35: the item kind emote_effect
// resolves the names over the emote catalog, caps the shown emotes, keeps
// the unknown as text while allowEmoji, and shows nothing without a
// resolvable emote.
func TestOverlayEmoteEffectItem(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)

		// The names resolve to the images of the catalog; maxShown caps
		// the shown emotes over the list (B35).
		cmd := f.h.Command("rain",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{
					"kind": "emote_effect", "emotes": []string{"Kappa", "Pog"},
					"effect": "rain", "perEmoteShown": 2, "maxShown": 3,
				}
			}),
		)
		in := f.h.Start(cmd, engine.Params{})
		f.overlay.end(t, 0)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		item := f.overlay.Item(t, 0)
		assert.Equal(t, "emote_effect", item.Kind)
		assert.Equal(t, map[string]any{
			"effect": "rain",
			"emotes": []map[string]any{
				{"code": "Kappa", "image": "https://emotes/Kappa.png", "shown": 2},
				{"code": "Pog", "image": "https://emotes/Pog.png", "shown": 1},
			},
		}, item.Content)

		// allowEmoji keeps the unknown name without an image, as text
		// for the runtime (B35).
		cmd = f.h.Command("emoji",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{
					"kind": "emote_effect", "emotes": []string{"Kappa", "Nope"},
					"effect": "burst", "allowEmoji": true,
				}
			}),
		)
		in = f.h.Start(cmd, engine.Params{})
		f.overlay.end(t, 1)
		synctest.Wait()
		item = f.overlay.Item(t, 1)
		assert.Equal(t, map[string]any{
			"effect": "burst",
			"emotes": []map[string]any{
				{"code": "Kappa", "image": "https://emotes/Kappa.png", "shown": 1},
				{"code": "Nope", "image": "", "shown": 1},
			},
		}, item.Content)

		// Without allowEmoji the unknown name is dropped, and without a
		// resolvable emote no item is shown, the command continues
		// (B35).
		cmd = f.h.Command("dropped",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{
					"kind": "emote_effect", "emotes": []string{"Kappa", "Nope"},
				}
			}),
		)
		in = f.h.Start(cmd, engine.Params{})
		f.overlay.end(t, 2)
		synctest.Wait()
		item = f.overlay.Item(t, 2)
		assert.Equal(t, map[string]any{
			"effect": "random",
			"emotes": []map[string]any{
				{"code": "Kappa", "image": "https://emotes/Kappa.png", "shown": 1},
			},
		}, item.Content)

		none := f.h.Command("none",
			f.ov(t, overlayaction.KindItem, func(a *overlayaction.Overlay) {
				a.Item.Content = map[string]any{
					"kind": "emote_effect", "emotes": []string{"Nope", "Meh"},
				}
			}),
		)
		in = f.h.Start(none, engine.Params{})
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		assert.Equal(t, 3, f.overlay.count())
	})
}
