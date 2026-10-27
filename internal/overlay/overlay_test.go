// SPDX-License-Identifier: MIT

package overlay_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/settings"
)

// recorder collects the messages sent to the client slots.
type recorder struct {
	mu   sync.Mutex
	list []overlay.Message
}

func (r *recorder) attach(t *testing.T, svc *overlay.Service) {
	t.Helper()
	r.mu.Lock()
	send := func(msg overlay.Message) {
		r.mu.Lock()
		r.list = append(r.list, msg)
		r.mu.Unlock()
	}
	r.mu.Unlock()
	_, err := svc.Attach(context.Background(), settings.DefaultEndpointID, send)
	require.NoError(t, err)
}

func (r *recorder) all() []overlay.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.list)
}

// testService returns a service with the given section.
func testService(t *testing.T, sec settings.Overlay) *overlay.Service {
	t.Helper()
	svc, err := overlay.New(context.Background(), overlay.Ports{
		Section: func(context.Context) (settings.Overlay, error) { return sec, nil },
		Version: "test",
		Rand:    func() (float64, float64) { return 25, 75 },
	})
	require.NoError(t, err)
	return svc
}

// TestEndpointsAreAlwaysAvailable covers B2: the default endpoint exists
// with or without an entry, unknown endpoints are unknown, the list
// carries default first.
func TestEndpointsAreAlwaysAvailable(t *testing.T) {
	svc := testService(t, settings.Overlay{})
	e, ok := svc.Endpoint(settings.DefaultEndpointID)
	require.True(t, ok)
	assert.Equal(t, settings.DefaultEndpointID, e.ID)
	_, ok = svc.Endpoint("nope")
	assert.False(t, ok)

	svc2 := testService(t, settings.Overlay{Endpoints: []settings.OverlayEndpoint{{ID: "alerts", Name: "Alerts"}}})
	_, ok = svc2.Endpoint("alerts")
	assert.True(t, ok)
	list := svc2.Endpoints()
	require.Len(t, list, 2)
	assert.Equal(t, settings.DefaultEndpointID, list[0].ID)
	assert.Equal(t, "alerts", list[1].ID)
}

// TestItemLifecycleCoversShowRemoveAndStateSync covers B4, B11 and B45:
// the handshake carries hello and state.widgets, an item is shown and
// removed after its duration, an unknown endpoint is an error with the
// ID in the message.
func TestItemLifecycleCoversShowRemoveAndStateSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := testService(t, settings.Overlay{})

		// An unknown endpoint is an error with the ID (B45, B18).
		_, err := svc.ShowItem(context.Background(), "nope", overlay.Item{
			Kind: "text", Position: settings.Position{Model: settings.PositionSimple, Point: "center"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nope")

		rec := &recorder{}
		rec.attach(t, svc)
		// The handshake of a new client: hello, state.widgets (B4).
		msgs := rec.all()
		require.Len(t, msgs, 2)
		assert.Equal(t, overlay.TypeHello, msgs[0].Type)
		assert.Equal(t, overlay.TypeStateWidgets, msgs[1].Type)
		hello := msgs[0].Data.(overlay.HelloData)
		assert.Equal(t, "test", hello.Version)
		assert.Equal(t, settings.DefaultEndpointID, hello.Endpoint)

		// The item is shown, and removed after its duration (B11).
		id, err := svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{
			Kind:     "text",
			Position: settings.Position{Model: settings.PositionSimple, Point: "top"},
			Duration: 20 * time.Millisecond,
		})
		require.NoError(t, err)
		assert.Equal(t, overlay.TypeItemShow, rec.all()[2].Type)
		time.Sleep(30 * time.Millisecond) // the duration timer fires
		synctest.Wait()                   // the removal broadcast has run
		assert.Equal(t, overlay.TypeItemRemove, rec.all()[3].Type)
		assert.False(t, svc.RemoveItem(context.Background(), settings.DefaultEndpointID, id)) // twice is a no-op

		// A new client sees the active item again in its handshake (B4).
		id2, err := svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{
			Kind: "text", Position: settings.Position{Model: settings.PositionSimple, Point: "center"},
		})
		require.NoError(t, err)
		rec2 := &recorder{}
		rec2.attach(t, svc)
		msgs2 := rec2.all()
		require.Len(t, msgs2, 3)
		assert.Equal(t, overlay.TypeItemShow, msgs2[2].Type)
		require.True(t, svc.RemoveItem(context.Background(), settings.DefaultEndpointID, id2))
	})
}

// TestItemEndedRemovesTheItemCoversB14: the runtime's item.ended removes
// the item from the active set and tells the other runtimes (B49).
func TestItemEndedRemovesTheItemCoversB14(t *testing.T) {
	svc := testService(t, settings.Overlay{})
	rec := &recorder{}
	rec.attach(t, svc)
	rec.wait(t, 2) // handshake
	id, err := svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{
		Kind:     "video",
		Position: settings.Position{Model: settings.PositionSimple, Point: "center"},
	})
	require.NoError(t, err)
	rec.wait(t, 3)
	require.True(t, svc.ItemEnded(context.Background(), settings.DefaultEndpointID, id))
	rec.wait(t, 4)
	assert.Equal(t, overlay.TypeItemRemove, rec.all()[3].Type)
	assert.Empty(t, svc.ActiveItems(settings.DefaultEndpointID))
}

// TestItemValidationCoversB9AndB10: unknown kinds, animations, positions
// and durations are rejected; a random position is rolled into a
// concrete point.
func TestItemValidationCoversB9AndB10(t *testing.T) {
	svc := testService(t, settings.Overlay{})
	pos := settings.Position{Model: settings.PositionSimple, Point: "center"}

	_, err := svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{Kind: "hologram", Position: pos})
	require.Error(t, err)
	_, err = svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{Kind: "text", Position: pos, Animation: "spiral"})
	require.Error(t, err)
	_, err = svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{
		Kind: "text", Position: settings.Position{Model: "warp", Point: "center"},
	})
	require.Error(t, err)
	_, err = svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{Kind: "text", Position: pos, Duration: -time.Second})
	require.Error(t, err)
	assert.Empty(t, svc.ActiveItems(settings.DefaultEndpointID))

	// The random position is rolled into percent coordinates (B9).
	id, err := svc.ShowItem(context.Background(), settings.DefaultEndpointID, overlay.Item{
		Kind: "text", Position: settings.Position{Model: settings.PositionRandom},
	})
	require.NoError(t, err)
	items := svc.ActiveItems(settings.DefaultEndpointID)
	require.Len(t, items, 1)
	assert.Equal(t, settings.PositionPercent, items[0].Position.Model)
	assert.Equal(t, 25.0, items[0].Position.X)
	assert.Equal(t, 75.0, items[0].Position.Y)
	require.True(t, svc.RemoveItem(context.Background(), settings.DefaultEndpointID, id))
}

// TestWidgetVisibilityCoversB21: a hidden widget is not sent,
// widget.show and widget.hide switch it at runtime, an unknown widget
// is an error.
func TestWidgetVisibilityCoversB21(t *testing.T) {
	sec := settings.Overlay{
		Widgets: []settings.OverlayWidget{
			{ID: "goal1", Kind: "goal", Name: "Goal", Visible: true, Position: settings.Position{Model: settings.PositionSimple, Point: "top"}},
			{ID: "label1", Kind: "label", Visible: false, Position: settings.Position{Model: settings.PositionSimple, Point: "bottom"}},
		},
	}
	svc := testService(t, sec)
	rec := &recorder{}
	rec.attach(t, svc)
	rec.wait(t, 2)

	// Only the visible widget is in the state (B21).
	state := rec.all()[1].Data.(overlay.StateWidgetsData)
	require.Len(t, state.Widgets, 1)
	assert.Equal(t, "goal1", state.Widgets[0].ID)

	// Hiding and showing broadcast (B21).
	require.NoError(t, svc.SetVisible(context.Background(), "goal1", false))
	rec.wait(t, 3)
	assert.Equal(t, overlay.TypeWidgetHide, rec.all()[2].Type)
	require.NoError(t, svc.SetVisible(context.Background(), "label1", true))
	rec.wait(t, 4)
	assert.Equal(t, overlay.TypeWidgetShow, rec.all()[3].Type)

	// An unknown widget is an error.
	err := svc.SetVisible(context.Background(), "nope", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")

	// Showing the first widget again: the state now carries both.
	require.NoError(t, svc.SetVisible(context.Background(), "goal1", true))
	rec.wait(t, 5)

	// The state now carries both widgets.
	var again overlay.StateWidgetsData
	require.NoError(t, svc.SyncState(context.Background(), settings.DefaultEndpointID, func(m overlay.Message) {
		if m.Type == overlay.TypeStateWidgets {
			again = m.Data.(overlay.StateWidgetsData)
		}
	}))
	require.Len(t, again.Widgets, 2)
}

// TestReloadAppliesTheSectionCoversB2: a new endpoint and widget show
// up, the items of a removed endpoint are dropped.
func TestReloadAppliesTheSectionCoversB2(t *testing.T) {
	var mu sync.Mutex
	sec := settings.Overlay{Endpoints: []settings.OverlayEndpoint{{ID: "alerts"}}}
	svc, err := overlay.New(context.Background(), overlay.Ports{
		Section: func(context.Context) (settings.Overlay, error) {
			mu.Lock()
			defer mu.Unlock()
			return sec, nil
		},
		Version: "test",
	})
	require.NoError(t, err)

	id, err := svc.ShowItem(context.Background(), "alerts", overlay.Item{
		Kind: "text", Position: settings.Position{Model: settings.PositionSimple, Point: "center"},
	})
	require.NoError(t, err)
	require.Len(t, svc.ActiveItems("alerts"), 1)

	mu.Lock()
	sec = settings.Overlay{
		Widgets: []settings.OverlayWidget{{ID: "w1", Kind: "label", Position: settings.Position{Model: settings.PositionSimple, Point: "top"}}},
	}
	mu.Unlock()
	require.NoError(t, svc.Reload(context.Background()))
	_, ok := svc.Endpoint("alerts")
	assert.False(t, ok) // the endpoint left the section: its items are dropped
	_, ok = svc.Widget("w1")
	assert.True(t, ok)
	assert.Empty(t, svc.ActiveItems("alerts"))
	require.False(t, svc.RemoveItem(context.Background(), "alerts", id))
}

// wait checks that n messages are recorded; the sends of the service
// are synchronous, so the count holds at the call site.
func (r *recorder) wait(t *testing.T, n int) {
	t.Helper()
	require.GreaterOrEqual(t, len(r.all()), n)
}
