// SPDX-License-Identifier: MIT

package streaming_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	streamingaction "github.com/ripmav/streamcrew/internal/action/streaming"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/engine"
)

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

// op is an operation the fake OBS recorded.
type op struct {
	name    string
	scene   string
	source  string
	filter  string
	visible *bool
	x, y    *float64
}

// fakeOBS records the operations and fails them all with err.
type fakeOBS struct {
	mu  sync.Mutex
	ops []op
	err error
}

func (f *fakeOBS) SetScene(_ context.Context, name string) error {
	return f.record(op{name: "set_scene", scene: name})
}

func (f *fakeOBS) StartStream(context.Context) error {
	return f.record(op{name: "start_stream"})
}

func (f *fakeOBS) StopStream(context.Context) error {
	return f.record(op{name: "stop_stream"})
}

func (f *fakeOBS) StartRecording(context.Context) error {
	return f.record(op{name: "start_record"})
}

func (f *fakeOBS) StopRecording(context.Context) error {
	return f.record(op{name: "stop_record"})
}

func (f *fakeOBS) SaveReplay(context.Context) error {
	return f.record(op{name: "save_replay"})
}

func (f *fakeOBS) SetSource(_ context.Context, ch streamingaction.SourceChange) error {
	return f.record(op{name: "set_source", source: ch.Source, visible: ch.Visible, x: ch.X, y: ch.Y})
}

func (f *fakeOBS) SetFilter(_ context.Context, source, filter string, visible bool) error {
	v := visible
	return f.record(op{name: "set_filter", source: source, filter: filter, visible: &v})
}

func (f *fakeOBS) record(o op) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.ops = append(f.ops, o)
	return nil
}

func (f *fakeOBS) opsLocked() []op {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]op(nil), f.ops...)
}

// fixture wires the streaming_software type with the fakes into a running
// engine.
type fixture struct {
	h    *actiontest.Harness
	reg  *action.Registry
	obs  *fakeOBS
	jour *actiontest.Journal
}

// registry returns the streaming_software type with the fakes:
// the capability check lives in the obs service, not in the action.
func registry(t *testing.T) (*action.Registry, *fakeOBS) {
	t.Helper()
	rights, err := capability.NewSet()
	require.NoError(t, err)
	obs := &fakeOBS{}
	descs, err := streamingaction.Descriptors(streamingaction.Ports{
		OBS:    obs,
		Logger: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	reg, err := action.NewRegistry(rights, descs...)
	require.NoError(t, err)
	return reg, obs
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	reg, obs := registry(t)
	return &fixture{h: actiontest.NewHarness(t, reg), reg: reg, obs: obs, jour: &actiontest.Journal{}}
}

// sw makes a streaming_software action of kind with the fixture's ports.
func (f *fixture) sw(t *testing.T, kind streamingaction.Kind, mutate func(a *streamingaction.Software)) streamingaction.Software {
	t.Helper()
	d, ok := f.reg.Descriptor(streamingaction.TypeStreamingSoftware)
	require.True(t, ok)
	a, ok := d.New().(streamingaction.Software)
	require.True(t, ok)
	a.Kind = kind
	switch kind {
	case streamingaction.KindSetScene:
		a.SetScene = &streamingaction.SetSceneOptions{Scene: "Cut"}
	case streamingaction.KindSetSource:
		a.SetSource = &streamingaction.SetSourceOptions{Source: "cam"}
	case streamingaction.KindSetFilter:
		a.SetFilter = &streamingaction.SetFilterOptions{Input: "cam", Filter: "Crop", Show: new(true)}
	default:
		// the other kinds have no options
	}
	if mutate != nil {
		mutate(&a)
	}
	return a
}

// TestConformance covers the schema of the type (Code-ADR-0013, point 6)
// against the golden and the examples.
func TestConformance(t *testing.T) {
	t.Parallel()
	reg, _ := registry(t)
	d, ok := reg.Descriptor(streamingaction.TypeStreamingSoftware)
	require.True(t, ok)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "set_scene", Doc: `{"type":"streaming_software","kind":"set_scene","scene":"Cut"}`, Valid: true},
		{Name: "start_stream", Doc: `{"type":"streaming_software","kind":"start_stream"}`, Valid: true},
		{Name: "stop_stream", Doc: `{"type":"streaming_software","kind":"stop_stream"}`, Valid: true},
		{Name: "start_recording", Doc: `{"type":"streaming_software","kind":"start_recording"}`, Valid: true},
		{Name: "stop_recording", Doc: `{"type":"streaming_software","kind":"stop_recording"}`, Valid: true},
		{Name: "save_replay", Doc: `{"type":"streaming_software","kind":"save_replay"}`, Valid: true},
		{Name: "set_source visible", Doc: `{"type":"streaming_software","kind":"set_source","source":"cam","visible":false}`, Valid: true},
		{Name: "set_source position", Doc: `{"type":"streaming_software","kind":"set_source","source":"cam","x":10.5,"y":-4}`, Valid: true},
		{Name: "set_filter", Doc: `{"type":"streaming_software","kind":"set_filter","input":"cam","filter":"Crop","show":true}`, Valid: true},
		{Name: "kind missing", Doc: `{"type":"streaming_software"}`},
		{Name: "unknown kind", Doc: `{"type":"streaming_software","kind":"nope"}`},
		{Name: "set_scene without scene", Doc: `{"type":"streaming_software","kind":"set_scene"}`},
		{Name: "set_source without a change", Doc: `{"type":"streaming_software","kind":"set_source","source":"cam"}`},
		{Name: "set_filter without show", Doc: `{"type":"streaming_software","kind":"set_filter","input":"cam","filter":"Crop"}`},
		{Name: "the scene of another kind", Doc: `{"type":"streaming_software","kind":"start_stream","scene":"Cut"}`},
	}}.Run(t)
}

// TestValidate covers the checks of the type (B42).
func TestValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		kind   streamingaction.Kind
		mutate func(a *streamingaction.Software)
		ok     bool
	}{
		{name: "set_scene", kind: streamingaction.KindSetScene, ok: true},
		{name: "set_scene empty scene", kind: streamingaction.KindSetScene, mutate: func(a *streamingaction.Software) { a.SetScene.Scene = "" }},
		{name: "start_stream", kind: streamingaction.KindStartStream, ok: true},
		{name: "stop_stream", kind: streamingaction.KindStopStream, ok: true},
		{name: "start_recording", kind: streamingaction.KindStartRecording, ok: true},
		{name: "stop_recording", kind: streamingaction.KindStopRecording, ok: true},
		{name: "save_replay", kind: streamingaction.KindSaveReplay, ok: true},
		{name: "set_source visible", kind: streamingaction.KindSetSource, mutate: func(a *streamingaction.Software) { a.SetSource.Visible = new(false) }, ok: true},
		{name: "set_source x", kind: streamingaction.KindSetSource, mutate: func(a *streamingaction.Software) { a.SetSource.X = new(10.0) }, ok: true},
		{name: "set_source y", kind: streamingaction.KindSetSource, mutate: func(a *streamingaction.Software) { a.SetSource.Y = new(-4.0) }, ok: true},
		{name: "set_source x and y", kind: streamingaction.KindSetSource, mutate: func(a *streamingaction.Software) { a.SetSource.X = new(10.0); a.SetSource.Y = new(-4.0) }, ok: true},
		{name: "set_source without a change", kind: streamingaction.KindSetSource, mutate: func(a *streamingaction.Software) { a.SetSource.Visible = nil }},
		{name: "set_source empty source", kind: streamingaction.KindSetSource, mutate: func(a *streamingaction.Software) { a.SetSource.Source = ""; a.SetSource.Visible = new(true) }},
		{name: "set_filter", kind: streamingaction.KindSetFilter, ok: true},
		{name: "set_filter empty filter", kind: streamingaction.KindSetFilter, mutate: func(a *streamingaction.Software) { a.SetFilter.Filter = "" }},
		{name: "set_filter without show", kind: streamingaction.KindSetFilter, mutate: func(a *streamingaction.Software) { a.SetFilter.Show = nil }},
		{name: "the scene of another kind", kind: streamingaction.KindStartStream, mutate: func(a *streamingaction.Software) { a.SetScene = &streamingaction.SetSceneOptions{Scene: "Cut"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := sw(streamingaction.Software{}, tc.kind)
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

// sw makes a streaming_software action of kind without ports, for the
// validation tests.
func sw(a streamingaction.Software, kind streamingaction.Kind) streamingaction.Software {
	a.Common = action.On()
	a.Kind = kind
	switch kind {
	case streamingaction.KindSetScene:
		a.SetScene = &streamingaction.SetSceneOptions{Scene: "Cut"}
	case streamingaction.KindSetSource:
		a.SetSource = &streamingaction.SetSourceOptions{Source: "cam", Visible: new(true)}
	case streamingaction.KindSetFilter:
		a.SetFilter = &streamingaction.SetFilterOptions{Input: "cam", Filter: "Crop", Show: new(true)}
	default:
		// the other kinds have no options
	}
	return a
}

// TestKindsRunTheirOperations covers B42: every kind runs its operation
// on the OBS connection of the core.
func TestKindsRunTheirOperations(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		cmd := f.h.Command("kinds",
			f.sw(t, streamingaction.KindSetScene, nil),
			f.sw(t, streamingaction.KindStartStream, nil),
			f.sw(t, streamingaction.KindStopStream, nil),
			f.sw(t, streamingaction.KindStartRecording, nil),
			f.sw(t, streamingaction.KindStopRecording, nil),
			f.sw(t, streamingaction.KindSaveReplay, nil),
		)
		in := f.h.Start(cmd, engine.Params{})
		assert.Equal(t, engine.StateCompleted, in.State)
		got := []string{}
		for _, o := range f.obs.opsLocked() {
			got = append(got, o.name)
		}
		assert.Equal(t, []string{
			"set_scene", "start_stream", "stop_stream",
			"start_record", "stop_record", "save_replay",
		}, got)
	})
}

// TestSetSourceRunsTheMembers covers B42: the kind set_source runs the
// members that are set and leaves the rest as it is.
func TestSetSourceRunsTheMembers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		cmd := f.h.Command("source",
			f.sw(t, streamingaction.KindSetSource, func(a *streamingaction.Software) {
				a.SetSource = &streamingaction.SetSourceOptions{Source: "cam", Visible: new(false)}
			}),
			f.sw(t, streamingaction.KindSetSource, func(a *streamingaction.Software) {
				a.SetSource = &streamingaction.SetSourceOptions{Source: "cam", X: new(10.0), Y: new(-4.0)}
			}),
		)
		in := f.h.Start(cmd, engine.Params{})
		assert.Equal(t, engine.StateCompleted, in.State)
		ops := f.obs.opsLocked()
		require.Len(t, ops, 2)
		assert.Equal(t, "cam", ops[0].source)
		require.NotNil(t, ops[0].visible)
		assert.False(t, *ops[0].visible)
		assert.Nil(t, ops[0].x, "the position is not set")
		assert.Nil(t, ops[0].y, "the position is not set")
		require.NotNil(t, ops[1].x)
		require.NotNil(t, ops[1].y)
		assert.Equal(t, 10.0, *ops[1].x)
		assert.Equal(t, -4.0, *ops[1].y)
		assert.Nil(t, ops[1].visible, "the visibility is not set")
	})
}

// TestSetFilterRunsTheVisibility covers B42: the kind set_filter sets the
// visibility of the filter of the source.
func TestSetFilterRunsTheVisibility(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		cmd := f.h.Command("filter",
			f.sw(t, streamingaction.KindSetFilter, func(a *streamingaction.Software) {
				a.SetFilter.Show = new(false)
			}),
		)
		in := f.h.Start(cmd, engine.Params{})
		assert.Equal(t, engine.StateCompleted, in.State)
		ops := f.obs.opsLocked()
		require.Len(t, ops, 1)
		assert.Equal(t, "cam", ops[0].source)
		assert.Equal(t, "Crop", ops[0].filter)
		require.NotNil(t, ops[0].visible)
		assert.False(t, *ops[0].visible)
	})
}

// TestWithoutAConnection covers B55: without the OBS connection the
// action is a clear error of the step, not a wait, and the command goes
// on.
func TestWithoutAConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.obs.err = errors.New("obs: no active connection to OBS")
		cmd := f.h.Command("offline",
			f.sw(t, streamingaction.KindSetScene, nil),
			f.jour.Note("after"),
		)
		in := f.h.Start(cmd, engine.Params{})
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "no active connection to OBS")
		assert.Equal(t, []string{"after"}, f.jour.Lines())
		assert.Equal(t, engine.StateCompleted, in.State, "the error policy is continue")
	})
}

// TestDescriptorsNeedPorts covers the ports of the type: without the OBS
// connection or the logger the type is not registered.
func TestDescriptorsNeedPorts(t *testing.T) {
	t.Parallel()
	full := streamingaction.Ports{OBS: &fakeOBS{}, Logger: slog.New(slog.DiscardHandler)}
	_, err := streamingaction.Descriptors(full)
	require.NoError(t, err)
	for name, change := range map[string]func(p *streamingaction.Ports){
		"obs":    func(p *streamingaction.Ports) { p.OBS = nil },
		"logger": func(p *streamingaction.Ports) { p.Logger = nil },
	} {
		p := full
		change(&p)
		_, err := streamingaction.Descriptors(p)
		assert.Error(t, err, name)
	}
}
