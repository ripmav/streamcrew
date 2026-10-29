// SPDX-License-Identifier: MIT

package sound_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	soundaction "github.com/ripmav/streamcrew/internal/action/sound"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
)

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

// fakeMedia resolves the files under the media directory of the profile
// (B40, B47).
type fakeMedia struct {
	dir string
	err error
}

func (m *fakeMedia) File(file string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return filepath.Join(m.dir, file), nil
}

// play is a playback the fake sinks recorded.
type play struct {
	sink   string
	file   string
	volume float64
}

// fakeSinks records the playbacks and serves the end channels; the test
// closes one to end a playback.
type fakeSinks struct {
	mu       sync.Mutex
	plays    []play
	playedAt []time.Time
	ends     []chan struct{}
	err      error
}

func (s *fakeSinks) Play(_ context.Context, name, file string, volume float64, _ capability.Source) (<-chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	end := make(chan struct{})
	s.plays = append(s.plays, play{sink: name, file: file, volume: volume})
	s.playedAt = append(s.playedAt, time.Now())
	s.ends = append(s.ends, end)
	return end, nil
}

func (s *fakeSinks) playsLocked() []play {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]play(nil), s.plays...)
}

// end closes the end channel of the i-th playback.
func (s *fakeSinks) end(t *testing.T, i int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Less(t, i, len(s.ends), "the playback %d is not started", i)
	close(s.ends[i])
}

func (s *fakeSinks) PlayedAt(t *testing.T, i int) time.Time {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Less(t, i, len(s.playedAt), "the playback %d is not started", i)
	return s.playedAt[i]
}

// fixture wires the sound type with the fakes and the note journal into
// a running engine.
type fixture struct {
	h       *actiontest.Harness
	reg     *action.Registry
	media   *fakeMedia
	sinks   *fakeSinks
	journal *actiontest.Journal
}

// registry returns the sound type with the fakes and the empty rights:
// the capability check lives in the media service, not in the action.
func registry(t *testing.T) (*action.Registry, *fakeMedia, *fakeSinks) {
	t.Helper()
	rights, err := capability.NewSet()
	require.NoError(t, err)
	media := &fakeMedia{dir: "/media/profile"}
	sinks := &fakeSinks{}
	descs, err := soundaction.Descriptors(soundaction.Ports{
		Media:       media,
		Sinks:       sinks,
		Rights:      rights,
		DefaultSink: "overlay",
		Logger:      slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	reg, err := action.NewRegistry(rights, descs...)
	require.NoError(t, err)
	return reg, media, sinks
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	reg, media, sinks := registry(t)
	return &fixture{h: actiontest.NewHarness(t, reg), reg: reg, media: media, sinks: sinks, journal: &actiontest.Journal{}}
}

// snd makes a sound action with the fixture's ports.
func (f *fixture) snd(t *testing.T, mutate func(a *soundaction.Sound)) soundaction.Sound {
	t.Helper()
	d, ok := f.reg.Descriptor(soundaction.TypeSound)
	require.True(t, ok)
	a, ok := d.New().(soundaction.Sound)
	require.True(t, ok)
	a.File = "chime.mp3"
	if mutate != nil {
		mutate(&a)
	}
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

func TestConformance(t *testing.T) {
	t.Parallel()
	reg, _, _ := registry(t)
	d, ok := reg.Descriptor(soundaction.TypeSound)
	require.True(t, ok)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "file", Doc: `{"type":"sound","file":"chime.mp3"}`, Valid: true},
		{Name: "volume, sink and wait", Doc: `{"type":"sound","file":"chime.mp3","volume":40,"sink":"local","waitForFinish":true}`, Valid: true},
		{Name: "file missing", Doc: `{"type":"sound"}`},
		{Name: "empty file", Doc: `{"type":"sound","file":""}`},
		{Name: "volume above 100", Doc: `{"type":"sound","file":"a.mp3","volume":101}`},
		{Name: "volume below 0", Doc: `{"type":"sound","file":"a.mp3","volume":-1}`},
		{Name: "unknown sink", Doc: `{"type":"sound","file":"a.mp3","sink":"tv"}`},
	}}.Run(t)
}

func TestValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(a *soundaction.Sound)
		ok     bool
	}{
		{name: "file", ok: true},
		{name: "volume 0", mutate: func(a *soundaction.Sound) { a.Volume = 0 }, ok: true},
		{name: "volume 100", mutate: func(a *soundaction.Sound) { a.Volume = 100 }, ok: true},
		{name: "sink local", mutate: func(a *soundaction.Sound) { a.Sink = "local" }, ok: true},
		{name: "empty file", mutate: func(a *soundaction.Sound) { a.File = "" }},
		{name: "volume below 0", mutate: func(a *soundaction.Sound) { a.Volume = -1 }},
		{name: "volume above 100", mutate: func(a *soundaction.Sound) { a.Volume = 101 }},
		{name: "unknown sink", mutate: func(a *soundaction.Sound) { a.Sink = "tv" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := soundaction.Sound{Common: action.On(), File: "chime.mp3", Volume: 100}
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

// TestSoundPlaysOnSink covers B40: the action plays the file of the media
// directory on the default sink at the volume, and is done when the
// playback starts without waitForFinish.
func TestSoundPlaysOnSink(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		cmd := f.h.Command("play",
			f.snd(t, func(a *soundaction.Sound) { a.Volume = 40 }),
			f.journal.Note("done"),
		)
		in := f.h.Start(cmd, engine.Params{})
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		plays := f.sinks.playsLocked()
		require.Len(t, plays, 1)
		assert.Equal(t, "overlay", plays[0].sink, "the default sink")
		assert.Equal(t, filepath.Join(f.media.dir, "chime.mp3"), plays[0].file)
		assert.Equal(t, 40.0, plays[0].volume)
		assert.Equal(t, []string{"done"}, f.journal.Lines())
	})
}

// TestSoundWaitsForFinish covers B14 and B40: with waitForFinish the
// action is done when the playback ends, and reports the end to the
// engine (B43).
func TestSoundWaitsForFinish(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		cmd := f.h.Command("wait",
			f.snd(t, func(a *soundaction.Sound) { a.Wait = true }),
			f.journal.Note("after"),
		)
		in := f.h.Start(cmd, engine.Params{})
		assert.Equal(t, engine.StateRunning, f.h.Instance(in.ID).State, "the action waits for the playback")
		f.sinks.end(t, 0)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		assert.Equal(t, []string{"after"}, f.journal.Lines())
	})
}

// TestSoundInGreeting covers B40 and B43: the sound of a greeting with
// waitForFinish reports the end of the playback, and the sound of the
// next greeting starts the default gap (5 s) after it.
func TestSoundInGreeting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)

		// The first greeting plays a sound; the playback runs 8 s.
		in1 := f.greet(t, f.h.Command("g1",
			f.journal.Note("g1"),
			f.snd(t, func(a *soundaction.Sound) { a.Wait = true }),
		))
		synctest.Wait()
		synctest.Sleep(8 * time.Second)
		f.sinks.end(t, 0)
		synctest.Wait()
		require.Equal(t, engine.StateCompleted, f.h.Instance(in1).State)

		// The second greeting plays a sound; the default gap is 5 s
		// (settings), so it starts 5 s after the first ended.
		in2 := f.greet(t, f.h.Command("g2",
			f.snd(t, nil),
			f.journal.Note("g2"),
		))
		synctest.Sleep(14 * time.Second)
		f.sinks.end(t, 1)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in2).State)
		g1 := f.h.Instance(in1)
		// B43: the sound of the second greeting starts 5 s, the default
		// gap, after the playback of the first ended. The instances
		// start at once; only the action waits for the gate.
		assert.Equal(t, 5*time.Second, f.sinks.PlayedAt(t, 1).Sub(g1.EndedAt))
		assert.Equal(t, []string{"g1", "g2"}, f.journal.Lines())
	})
}

// TestSoundInGreetingWithoutWait covers the B43 fallback: without
// waitForFinish the action is done when the playback starts, and the
// engine counts the media gap from the end of the action, not from the
// end of the playback.
func TestSoundInGreetingWithoutWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)

		// The first greeting plays a sound without waiting; the
		// playback would run 8 s, the action ends at its start.
		in1 := f.greet(t, f.h.Command("g1", f.snd(t, nil)))
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in1).State, "the action ends when the playback starts")

		// The second greeting starts the default gap (5 s) after the
		// end of the first, while the first playback still runs.
		in2 := f.greet(t, f.h.Command("g2", f.snd(t, nil)))
		synctest.Sleep(14 * time.Second)
		f.sinks.end(t, 0)
		f.sinks.end(t, 1)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in2).State)
		g1 := f.h.Instance(in1)
		assert.Equal(t, 5*time.Second, f.sinks.PlayedAt(t, 1).Sub(g1.EndedAt))
	})
}

// TestSoundErrors covers B40 and B47: a file that does not resolve under
// the media directory and an unavailable sink (not built, no capability)
// are clear errors of the action, the command goes on.
func TestSoundErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name               string
		mediaErr, sinksErr error
		want               string
	}{
		{name: "file outside the media directory", mediaErr: errFile, want: "not under the media directory"},
		{name: "sink not built", sinksErr: errSink, want: "not built"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t)
				f.media.err = tc.mediaErr
				f.sinks.err = tc.sinksErr
				cmd := f.h.Command("err",
					f.snd(t, nil),
					f.journal.Note("after"),
				)
				in := f.h.Start(cmd, engine.Params{})
				require.Len(t, in.Errors, 1)
				assert.Contains(t, in.Errors[0].Message, tc.want)
				// The error policy is continue: the next action runs.
				assert.Equal(t, []string{"after"}, f.journal.Lines())
				assert.Equal(t, engine.StateCompleted, in.State)
			})
		})
	}
}

// errFile is the error of a file that does not resolve under the media
// directory (B47).
var errFile = errors.New("the file \"chime.mp3\" is not under the media directory")

// errSink is the error of an unavailable sink (B40).
var errSink = errors.New("media: the sink local is not built; the build needs the tag localaudio (ADR-0020)")

func TestDescriptorsNeedPorts(t *testing.T) {
	t.Parallel()
	rights, err := capability.NewSet()
	require.NoError(t, err)
	full := soundaction.Ports{
		Media:       &fakeMedia{dir: "/media/profile"},
		Sinks:       &fakeSinks{},
		Rights:      rights,
		DefaultSink: "overlay",
		Logger:      slog.New(slog.DiscardHandler),
	}
	_, err = soundaction.Descriptors(full)
	require.NoError(t, err)
	for name, change := range map[string]func(p *soundaction.Ports){
		"media":  func(p *soundaction.Ports) { p.Media = nil },
		"sinks":  func(p *soundaction.Ports) { p.Sinks = nil },
		"rights": func(p *soundaction.Ports) { p.Rights = nil },
		"logger": func(p *soundaction.Ports) { p.Logger = nil },
	} {
		p := full
		change(&p)
		_, err := soundaction.Descriptors(p)
		assert.Error(t, err, name)
	}
}
