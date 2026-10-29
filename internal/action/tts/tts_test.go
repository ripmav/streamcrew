// SPDX-License-Identifier: MIT

package tts_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	ttsaction "github.com/ripmav/streamcrew/internal/action/tts"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
	ttspkg "github.com/ripmav/streamcrew/internal/tts"
)

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

// fakeGenerate records the speeches of the file providers and returns
// the generated files.
type fakeGenerate struct {
	mu        sync.Mutex
	providers []ttspkg.Name
	speeches  []ttspkg.Speech
	next      int
	err       error
}

func (g *fakeGenerate) Generate(_ context.Context, provider ttspkg.Name, s ttspkg.Speech) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return "", g.err
	}
	g.providers = append(g.providers, provider)
	g.speeches = append(g.speeches, s)
	g.next++
	return fmt.Sprintf("/media/generated/tts-%d.mp3", g.next), nil
}

func (g *fakeGenerate) Speech(t *testing.T, i int) ttspkg.Speech {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	require.Less(t, i, len(g.speeches), "the speech %d is not generated", i)
	return g.speeches[i]
}

func (g *fakeGenerate) Providers() []ttspkg.Name {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ttspkg.Name(nil), g.providers...)
}

// fakeBrowser records the speeches of the provider browser and serves
// the end channels; the test closes one to end a speech.
type fakeBrowser struct {
	mu       sync.Mutex
	speeches []ttspkg.Speech
	spokenAt []time.Time
	ends     []chan struct{}
	err      error
}

func (b *fakeBrowser) Speak(_ context.Context, s ttspkg.Speech) (<-chan struct{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return nil, b.err
	}
	end := make(chan struct{})
	b.speeches = append(b.speeches, s)
	b.spokenAt = append(b.spokenAt, time.Now())
	b.ends = append(b.ends, end)
	return end, nil
}

func (b *fakeBrowser) Speech(t *testing.T, i int) ttspkg.Speech {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	require.Less(t, i, len(b.speeches), "the speech %d is not spoken", i)
	return b.speeches[i]
}

// end closes the end channel of the i-th speech.
func (b *fakeBrowser) end(t *testing.T, i int) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	require.Less(t, i, len(b.ends), "the speech %d is not spoken", i)
	close(b.ends[i])
}

func (b *fakeBrowser) SpokenAt(t *testing.T, i int) time.Time {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	require.Less(t, i, len(b.spokenAt), "the speech %d is not spoken", i)
	return b.spokenAt[i]
}

// fakeSinks records the playbacks of the generated files.
type fakeSinks struct {
	mu       sync.Mutex
	files    []string
	vols     []float64
	sinks    []string
	playedAt []time.Time
}

func (s *fakeSinks) Play(_ context.Context, name, file string, volume float64, _ capability.Source) (<-chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = append(s.files, file)
	s.vols = append(s.vols, volume)
	s.sinks = append(s.sinks, name)
	s.playedAt = append(s.playedAt, time.Now())
	return nil, nil
}

func (s *fakeSinks) Files() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.files...)
}

// fixture wires the text_to_speech type with the fakes and the note
// journal into a running engine.
type fixture struct {
	h       *actiontest.Harness
	reg     *action.Registry
	eng     *template.Engine
	gen     *fakeGenerate
	browser *fakeBrowser
	sinks   *fakeSinks
	journal *actiontest.Journal
}

func newFixture(t *testing.T, defaultSink string, caps ...capability.Capability) *fixture {
	t.Helper()
	if defaultSink == "" {
		defaultSink = "overlay"
	}
	rights, err := capability.NewSet(caps...)
	require.NoError(t, err)
	regT, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	eng := template.New(regT)
	gen := &fakeGenerate{}
	browser := &fakeBrowser{}
	sinks := &fakeSinks{}
	journal := &actiontest.Journal{}
	descs, err := ttsaction.Descriptors(ttsaction.Ports{
		Templates:       eng,
		TTS:             gen,
		Browser:         browser,
		Sinks:           sinks,
		Rights:          rights,
		DefaultSink:     defaultSink,
		DefaultProvider: "browser",
		Logger:          slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	registry, err := action.NewRegistry(rights, descs...)
	require.NoError(t, err)
	return &fixture{
		h: actiontest.NewHarness(t, registry), reg: registry, eng: eng,
		gen: gen, browser: browser, sinks: sinks, journal: journal,
	}
}

// tts makes a text_to_speech action with the fixture's ports.
func (f *fixture) tts(t *testing.T, mutate func(a *ttsaction.TextToSpeech)) ttsaction.TextToSpeech {
	t.Helper()
	d, ok := f.reg.Descriptor(ttsaction.TypeTextToSpeech)
	require.True(t, ok)
	a, ok := d.New().(ttsaction.TextToSpeech)
	require.True(t, ok)
	a.Text = "hello"
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
	regT, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	eng := template.New(regT)
	rights, err := capability.NewSet()
	require.NoError(t, err)
	descs, err := ttsaction.Descriptors(ttsaction.Ports{
		Templates: eng, TTS: &fakeGenerate{}, Browser: &fakeBrowser{}, Sinks: &fakeSinks{},
		Rights: rights, DefaultSink: "overlay", DefaultProvider: "browser",
		Logger: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	registry, err := action.NewRegistry(rights, descs...)
	require.NoError(t, err)
	d, ok := registry.Descriptor(ttsaction.TypeTextToSpeech)
	require.True(t, ok)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "text", Doc: `{"type":"text_to_speech","text":"hello"}`, Valid: true},
		{Name: "all options", Doc: `{"type":"text_to_speech","text":"hi $arg1text","provider":"piper","voice":"en-GB-alba-medium","volume":80,"rate":1.5,"pitch":0.8,"waitForFinish":true,"sink":"local"}`, Valid: true},
		{Name: "template in the text", Doc: `{"type":"text_to_speech","text":"hi $user"}`, Valid: true},
		{Name: "text missing", Doc: `{"type":"text_to_speech"}`},
		{Name: "empty text", Doc: `{"type":"text_to_speech","text":""}`},
		{Name: "unknown provider", Doc: `{"type":"text_to_speech","text":"hello","provider":"edge"}`},
		{Name: "volume above 100", Doc: `{"type":"text_to_speech","text":"hello","volume":101}`},
		{Name: "rate below 0", Doc: `{"type":"text_to_speech","text":"hello","rate":-0.5}`},
		{Name: "pitch below 0", Doc: `{"type":"text_to_speech","text":"hello","pitch":-1}`},
		{Name: "unknown sink", Doc: `{"type":"text_to_speech","text":"hello","sink":"tv"}`},
	}}.Run(t)
}

func TestValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(a *ttsaction.TextToSpeech)
		ok     bool
	}{
		{name: "text", ok: true},
		{name: "provider browser", mutate: func(a *ttsaction.TextToSpeech) { a.Provider = "browser" }, ok: true},
		{name: "provider piper", mutate: func(a *ttsaction.TextToSpeech) { a.Provider = "piper" }, ok: true},
		{name: "volume 0", mutate: func(a *ttsaction.TextToSpeech) { a.Volume = 0 }, ok: true},
		{name: "rate 0 keeps the default", mutate: func(a *ttsaction.TextToSpeech) { a.Rate = 0 }, ok: true},
		{name: "empty text", mutate: func(a *ttsaction.TextToSpeech) { a.Text = "" }},
		{name: "unknown provider", mutate: func(a *ttsaction.TextToSpeech) { a.Provider = "edge" }},
		{name: "volume below 0", mutate: func(a *ttsaction.TextToSpeech) { a.Volume = -1 }},
		{name: "volume above 100", mutate: func(a *ttsaction.TextToSpeech) { a.Volume = 101 }},
		{name: "rate below 0", mutate: func(a *ttsaction.TextToSpeech) { a.Rate = -0.5 }},
		{name: "pitch below 0", mutate: func(a *ttsaction.TextToSpeech) { a.Pitch = -1 }},
		{name: "unknown sink", mutate: func(a *ttsaction.TextToSpeech) { a.Sink = "tv" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := ttsaction.TextToSpeech{Common: action.On(), Text: "hello", Volume: 100, Rate: 1, Pitch: 1}
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

// TestTTSSpeakBrowser covers B41: the default provider browser speaks the
// rendered text on the overlay, renders rate 0 and pitch 0 to the normal
// 1, and takes no sink.
func TestTTSSpeakBrowser(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, "overlay")
		cmd := f.h.Command("speak",
			f.tts(t, func(a *ttsaction.TextToSpeech) { a.Rate = 0; a.Pitch = 0 }),
			f.journal.Note("done"),
		)
		in := f.h.Start(cmd, engine.Params{})
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		require.Len(t, f.browser.speeches, 1, "the browser speaks")
		assert.Equal(t, ttspkg.Speech{Text: "hello", Volume: 100, Rate: 1, Pitch: 1}, f.browser.Speech(t, 0))
		assert.Empty(t, f.sinks.Files(), "the browser provider speaks on the overlay, it plays no file")
		assert.Equal(t, []string{"done"}, f.journal.Lines())
	})
}

// TestTTSTextRenders covers B41: the text is a template rendered with the
// scope of the run.
func TestTTSTextRenders(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, "overlay")
		cmd := f.h.Command("render",
			f.tts(t, func(a *ttsaction.TextToSpeech) { a.Text = "hi $arg1text" }),
		)
		in := f.h.Start(cmd, engine.Params{Args: []string{"world"}, ArgsText: "world"})
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		require.Len(t, f.browser.speeches, 1)
		assert.Equal(t, "hi world", f.browser.Speech(t, 0).Text)
	})
}

// TestTTSEndsWithWait covers B41: with waitForFinish the action is done
// when the speech ends, and reports the end to the engine (B43).
func TestTTSEndsWithWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, "overlay")
		cmd := f.h.Command("wait",
			f.tts(t, func(a *ttsaction.TextToSpeech) { a.Wait = true }),
			f.journal.Note("after"),
		)
		in := f.h.Start(cmd, engine.Params{})
		assert.Equal(t, engine.StateRunning, f.h.Instance(in.ID).State, "the action waits for the speech")
		f.browser.end(t, 0)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State)
		assert.Equal(t, []string{"after"}, f.journal.Lines())
	})
}

// TestTTSLookupInGreeting covers B41 and B43: the speech of a greeting
// with waitForFinish reports the end, and the speech of the next greeting
// starts the default gap (5 s) after it.
func TestTTSLookupInGreeting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, "overlay")

		// The first greeting speaks; the speech runs 8 s.
		in1 := f.greet(t, f.h.Command("g1",
			f.journal.Note("g1"),
			f.tts(t, func(a *ttsaction.TextToSpeech) { a.Wait = true }),
		))
		synctest.Wait()
		synctest.Sleep(8 * time.Second)
		f.browser.end(t, 0)
		synctest.Wait()
		require.Equal(t, engine.StateCompleted, f.h.Instance(in1).State)

		// The second greeting speaks; the default gap is 5 s (settings),
		// so it starts 5 s after the first ended.
		in2 := f.greet(t, f.h.Command("g2",
			f.tts(t, nil),
			f.journal.Note("g2"),
		))
		synctest.Sleep(14 * time.Second)
		f.browser.end(t, 1)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.h.Instance(in2).State)
		g1 := f.h.Instance(in1)
		assert.Equal(t, 5*time.Second, f.browser.SpokenAt(t, 1).Sub(g1.EndedAt))
		assert.Equal(t, []string{"g1", "g2"}, f.journal.Lines())
	})
}

// TestTTSBrowserOtherSink covers B41: the provider browser speaks on the
// overlay and takes no other sink, as the action or the default.
func TestTTSBrowserOtherSink(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		defaultSink string
		mutate      func(a *ttsaction.TextToSpeech)
	}{
		{name: "the sink of the action", mutate: func(a *ttsaction.TextToSpeech) { a.Sink = "local" }},
		{name: "the default sink", defaultSink: "local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, tc.defaultSink)
				in := f.h.Start(f.h.Command("x", f.tts(t, tc.mutate)), engine.Params{})
				require.Len(t, in.Errors, 1)
				assert.Contains(t, in.Errors[0].Message, "the provider browser speaks on the overlay (sink overlay)")
				assert.Empty(t, f.browser.speeches, "nothing is spoken")
			})
		})
	}
}

// TestTTSFileProviderPlays covers B41: the file providers google, azure
// and piper render the speech with their capability and play the file on
// the sink at the volume.
func TestTTSFileProviderPlays(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		provider string
		cap      capability.Capability
	}{
		{provider: string(ttspkg.Google), cap: capability.NetOutbound},
		{provider: string(ttspkg.Azure), cap: capability.NetOutbound},
		{provider: string(ttspkg.Piper), cap: capability.HostProcess},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, "overlay", tc.cap)
				cmd := f.h.Command("gen",
					f.tts(t, func(a *ttsaction.TextToSpeech) {
						a.Provider = tc.provider
						a.Voice = "voice-1"
						a.Volume = 60
						a.Rate = 1.2
						a.Pitch = 0.9
					}),
				)
				in := f.h.Start(cmd, engine.Params{})
				assert.Equal(t, engine.StateCompleted, f.h.Instance(in.ID).State, in.Errors)
				require.Equal(t, []ttspkg.Name{ttspkg.Name(tc.provider)}, f.gen.Providers(), "the provider renders the speech")
				assert.Equal(t, ttspkg.Speech{Text: "hello", Voice: "voice-1", Volume: 60, Rate: 1.2, Pitch: 0.9}, f.gen.Speech(t, 0))
				files := f.sinks.Files()
				require.Len(t, files, 1)
				assert.Equal(t, "/media/generated/tts-1.mp3", files[0])
				assert.Equal(t, []float64{60}, f.sinks.vols)
				assert.Equal(t, []string{"overlay"}, f.sinks.sinks, "the default sink")
			})
		})
	}
}

// TestTTSFileProviderNeedsCapability covers B41 and ADR-0020 §2: the
// provider needs its capability at the action, with the rights of the
// core now, so the server mode without it is a clear error.
func TestTTSFileProviderNeedsCapability(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		provider string
		cap      capability.Capability
	}{
		{provider: string(ttspkg.Google), cap: capability.NetOutbound},
		{provider: string(ttspkg.Azure), cap: capability.NetOutbound},
		{provider: string(ttspkg.Piper), cap: capability.HostProcess},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, "overlay") // no capabilities
				in := f.h.Start(f.h.Command("gen",
					f.tts(t, func(a *ttsaction.TextToSpeech) { a.Provider = tc.provider }),
				), engine.Params{})
				require.Len(t, in.Errors, 1)
				assert.Contains(t, in.Errors[0].Message, "needs the capability")
				assert.Contains(t, in.Errors[0].Message, string(tc.cap))
				assert.Empty(t, f.gen.Providers(), "nothing is generated")
			})
		})
	}
}

// TestTTSGenerateFails covers B41: an error of the provider is a clear
// error of the action, the command goes on.
func TestTTSGenerateFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, "overlay", capability.NetOutbound)
		f.gen.err = errors.New("tts: the key is not set (STREAMCREW_TTS_GOOGLE_KEY)")
		cmd := f.h.Command("gen",
			f.tts(t, func(a *ttsaction.TextToSpeech) { a.Provider = "google" }),
			f.journal.Note("after"),
		)
		in := f.h.Start(cmd, engine.Params{})
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "the key is not set")
		assert.Equal(t, []string{"after"}, f.journal.Lines())
	})
}

func TestDescriptorsNeedPorts(t *testing.T) {
	t.Parallel()
	regT, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	rights, err := capability.NewSet()
	require.NoError(t, err)
	full := ttsaction.Ports{
		Templates: template.New(regT), TTS: &fakeGenerate{}, Browser: &fakeBrowser{},
		Sinks: &fakeSinks{}, Rights: rights, DefaultSink: "overlay", DefaultProvider: "browser",
		Logger: slog.New(slog.DiscardHandler),
	}
	_, err = ttsaction.Descriptors(full)
	require.NoError(t, err)
	for name, change := range map[string]func(p *ttsaction.Ports){
		"templates": func(p *ttsaction.Ports) { p.Templates = nil },
		"tts":       func(p *ttsaction.Ports) { p.TTS = nil },
		"browser":   func(p *ttsaction.Ports) { p.Browser = nil },
		"sinks":     func(p *ttsaction.Ports) { p.Sinks = nil },
		"rights":    func(p *ttsaction.Ports) { p.Rights = nil },
		"logger":    func(p *ttsaction.Ports) { p.Logger = nil },
	} {
		p := full
		change(&p)
		_, err := ttsaction.Descriptors(p)
		assert.Error(t, err, name)
	}
}
