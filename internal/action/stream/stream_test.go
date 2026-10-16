// SPDX-License-Identifier: MIT

package stream_test

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/stream"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/connectortest"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
)

// platforms is the stream Platforms port.
type platforms struct {
	set []connector.Platform
}

func (p platforms) Connected() []connector.Platform { return p.set }

func (p platforms) Platform(name platform.Name) (connector.Platform, bool) {
	for _, ps := range p.set {
		if ps.Name() == name {
			return ps, true
		}
	}
	return nil, false
}

type fixture struct {
	t       *testing.T
	reg     *action.Registry
	harness *actiontest.Harness
}

func newFixture(t *testing.T, set []connector.Platform) *fixture {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	ds, err := stream.Descriptors(stream.Ports{
		Templates: template.New(identifiers),
		Platforms: platforms{set: set},
		General: func(context.Context) (settings.General, error) {
			return settings.General{DefaultPlatform: platform.Mock}, nil
		},
		Logger: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	reg, err := action.NewRegistry(capability.Set{}, ds...)
	require.NoError(t, err)
	return &fixture{t: t, reg: reg, harness: actiontest.NewHarness(t, reg)}
}

func (f *fixture) action(k stream.Kind, text string) stream.Stream {
	f.t.Helper()
	d, ok := f.reg.Descriptor(stream.TypeStream)
	require.True(f.t, ok)
	s, ok := d.New().(stream.Stream)
	require.True(f.t, ok)
	s.Kind, s.Text = k, action.Template(text)
	require.NoError(f.t, s.Validate())
	return s
}

func mockPlatform(t *testing.T) *connectortest.Platform {
	t.Helper()
	return connectortest.New(platform.Mock, connectortest.Features{})
}

func TestSetTitle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		plat := mockPlatform(t)
		f := newFixture(t, []connector.Platform{plat})
		inst := f.harness.Start(f.harness.Command("x", f.action(stream.KindSetTitle, "$allargs")),
			engine.Params{Platform: platform.Mock, Args: []string{"New", "Title"}, ArgsText: "New Title"})
		assert.Equal(t, engine.StateCompleted, inst.State)
		calls := plat.Calls()
		require.Len(t, calls, 1)
		assert.Equal(t, connectortest.OpSetTitle, calls[0].Op)
		assert.Equal(t, "New Title", calls[0].Text)
	})
}

func TestSetGame(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		plat := mockPlatform(t)
		f := newFixture(t, []connector.Platform{plat})
		inst := f.harness.Start(f.harness.Command("x", f.action(stream.KindSetGame, "$allargs")),
			engine.Params{Platform: platform.Mock, Args: []string{"Just", "Chatting"}, ArgsText: "Just Chatting"})
		assert.Equal(t, engine.StateCompleted, inst.State)
		calls := plat.Calls()
		require.Len(t, calls, 1)
		assert.Equal(t, connectortest.OpSetGame, calls[0].Op)
		assert.Equal(t, "Just Chatting", calls[0].Text)
	})
}

// TestDefaultPlatform: without a run platform, the action acts on the
// default platform of the profile.
func TestDefaultPlatform(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		plat := mockPlatform(t)
		f := newFixture(t, []connector.Platform{plat})
		inst := f.harness.Start(f.harness.Command("x", f.action(stream.KindSetTitle, "T")),
			engine.Params{Args: []string{"T"}, ArgsText: "T"})
		assert.Equal(t, engine.StateCompleted, inst.State)
		require.Len(t, plat.Calls(), 1)
		assert.Equal(t, connectortest.OpSetTitle, plat.Calls()[0].Op)
	})
}

func TestNoPlatform(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, nil)
		inst := f.harness.Start(f.harness.Command("x", f.action(stream.KindSetTitle, "x")),
			engine.Params{})
		assert.NotEmpty(t, inst.Errors, "no platform is recorded")
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, []connector.Platform{mockPlatform(t)})
		assert.NoError(t, f.action(stream.KindSetTitle, "x").Validate())
		assert.NoError(t, f.action(stream.KindSetGame, "x").Validate())
		s := f.action(stream.KindSetTitle, "x")
		s.Kind = ""
		assert.Error(t, s.Validate())
	})
}
