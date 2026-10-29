// SPDX-License-Identifier: MIT

// Package tts has the text_to_speech action (roadmap 7.3, spec
// overlays.md, B41, ADR-0020 §3): it speaks a rendered text with the
// provider browser (the Web Speech API of the overlay runtime), google
// or azure (REST APIs with keys from the environment) or piper (an
// external process, host:process), and plays the result on a sink like
// the sound action (B40). While it waits for the end of the speech it
// reports it to the engine with PlaybackEnds (B43).
package tts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/media"
	"github.com/ripmav/streamcrew/internal/template"
	ttspkg "github.com/ripmav/streamcrew/internal/tts"
)

// TypeTextToSpeech is the type ID of the text_to_speech action
// (Code-ADR-0013, point 1).
const TypeTextToSpeech = "text_to_speech"

// Generate renders the speech of a file provider to a file under the
// generated directory and returns its path (B41); the tts.Service of
// the core satisfies it.
type Generate interface {
	Generate(ctx context.Context, provider ttspkg.Name, s ttspkg.Speech) (string, error)
}

// Browser speaks the speech on the overlay runtime (B41, the provider
// browser); the composition root satisfies it with the overlay service.
type Browser interface {
	// Speak makes the runtimes of the default endpoint speak the
	// speech and returns a channel that is closed when the speech is
	// done.
	Speak(ctx context.Context, s ttspkg.Speech) (<-chan struct{}, error)
}

// Sinks plays the files of the action (B40); the media.Service of the
// core satisfies it.
type Sinks interface {
	// Play plays file on the sink of the name at the volume with the
	// rights the core has now and returns a channel that is closed when
	// the playback is done.
	Play(ctx context.Context, name, file string, volume float64, rights capability.Source) (<-chan struct{}, error)
}

// Ports are the dependencies of the text_to_speech type.
type Ports struct {
	// Templates renders the text.
	Templates *template.Engine
	// TTS renders the speech of the file providers (B41).
	TTS Generate
	// Browser speaks the speech of the provider browser on the overlay
	// (B41).
	Browser Browser
	// Sinks play the generated files (B40, B41).
	Sinks Sinks
	// Rights are the rights the core has now (B41).
	Rights capability.Source
	// DefaultSink is the sink without one in the action (audio-sink,
	// ADR-0020 §2).
	DefaultSink string
	// DefaultProvider is the provider without one in the action
	// (tts-provider, ADR-0020 §3).
	DefaultProvider string
	// Logger records the operations and their results.
	Logger *slog.Logger
}

// ports are the ports of the text_to_speech type.
type ports struct {
	Ports
}

// Descriptors returns the text_to_speech type with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("text_to_speech action types: no template engine")
	case p.TTS == nil:
		return nil, errors.New("text_to_speech action types: no tts service")
	case p.Browser == nil:
		return nil, errors.New("text_to_speech action types: no browser provider")
	case p.Sinks == nil:
		return nil, errors.New("text_to_speech action types: no sinks")
	case p.Rights == nil:
		return nil, errors.New("text_to_speech action types: no rights")
	case p.Logger == nil:
		return nil, errors.New("text_to_speech action types: no logger")
	}
	return descriptors(&ports{Ports: p}), nil
}

// Catalog returns the text_to_speech type without ports, for the type
// catalog and commands as code (Code-ADR-0013, point 3): its actions
// decode, validate and encode, but must not run.
func Catalog() []action.Descriptor {
	return descriptors(nil)
}

// descriptors returns the text_to_speech type with its ports, which
// are nil in the catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:        TypeTextToSpeech,
			Version:     1,
			Category:    action.CategoryOverlay,
			VisualAudio: true,
			Schema:      ttsSchema(),
		}.WithNew(newTextToSpeech(ports)),
	}
}

// ttsSchema returns the schema of the text_to_speech action (B41): the
// text, the provider, the voice, volume, rate and pitch, and the wait
// and the sink like the sound action (B40).
func ttsSchema() *schema.Schema {
	positive := &schema.Schema{Type: "number", Minimum: new(0.0)}
	return schema.Document(
		schema.Property{Name: "text", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
		schema.Property{Name: "provider", Schema: schema.Choice(providerNames()...)},
		schema.Property{Name: "voice", Schema: schema.Text(schema.UIText)},
		schema.Property{Name: "volume", Schema: volumeSchema()},
		// 0 keeps the default of 1 (normal), as the schema cannot say
		// exclusive minimum.
		schema.Property{Name: "rate", Schema: positive},
		schema.Property{Name: "pitch", Schema: positive},
		schema.Property{Name: "waitForFinish", Schema: schema.Switch()},
		schema.Property{Name: "sink", Schema: schema.Choice(media.Sinks()...)},
	)
}

// providerNames are the provider names for the schema, in the order
// of tts.All.
func providerNames() []string {
	names := make([]string, len(ttspkg.All()))
	for i, n := range ttspkg.All() {
		names[i] = string(n)
	}
	return names
}

// volumeSchema is the volume from 0 to 100 of the media actions (B40).
func volumeSchema() *schema.Schema {
	return &schema.Schema{Type: "number", Minimum: new(0.0), Maximum: new(100.0)}
}

// TextToSpeech is the text_to_speech action (roadmap 7.3).
type TextToSpeech struct {
	action.Common `json:",embed"`
	// Text is the text to speak, as a template; required (B41).
	Text action.Template `json:"text"`
	// Provider is the provider to speak with, one of the tts providers;
	// empty for the default (tts-provider, B41).
	Provider string `json:"provider,omitzero"`
	// Voice is the name of the voice, provider specific; empty for the
	// default voice.
	Voice string `json:"voice,omitzero"`
	// Volume is the volume from 0 to 100; 100 for full.
	Volume float64 `json:"volume"`
	// Rate is the speaking rate; 0 or 1 for normal.
	Rate float64 `json:"rate"`
	// Pitch is the pitch; 0 or 1 for normal.
	Pitch float64 `json:"pitch"`
	// Wait holds the action until the speech is done (B41, default off);
	// without it, the engine counts the media gap from the end of the
	// action (B43).
	Wait bool `json:"waitForFinish,omitzero"`
	// Sink is the sink to play on, one of the media sinks; empty for the
	// default (audio-sink, B40). The provider browser speaks on the
	// overlay and takes no other sink.
	Sink  string `json:"sink,omitzero"`
	ports *ports
}

// newTextToSpeech makes a new text_to_speech action with the defaults
// and the ports of the type.
func newTextToSpeech(ports *ports) func() TextToSpeech {
	return func() TextToSpeech {
		return TextToSpeech{Common: action.On(), Volume: 100, Rate: 1, Pitch: 1, ports: ports}
	}
}

// DocType implements command.Action.
func (TextToSpeech) DocType() string { return TypeTextToSpeech }

// Validate implements command.Action.
func (t TextToSpeech) Validate() error {
	switch {
	case t.Text == "":
		return field("text", fmt.Errorf("%w: an empty text", action.ErrInvalid))
	case t.Provider != "" && !ttspkg.Name(t.Provider).Valid():
		return field("provider", fmt.Errorf("%w: unknown provider %q", action.ErrInvalid, t.Provider))
	case t.Volume < 0 || t.Volume > 100:
		return field("volume", fmt.Errorf("%w: the volume %g is 0 to 100", action.ErrInvalid, t.Volume))
	case t.Rate < 0 || t.Pitch < 0:
		return field("rate", fmt.Errorf("%w: the rate and the pitch are 0 (default) or more", action.ErrInvalid))
	case t.Sink != "" && !media.ValidSink(t.Sink):
		return field("sink", fmt.Errorf("%w: unknown sink %q", action.ErrInvalid, t.Sink))
	default:
		return nil
	}
}

// Perform implements engine.Performer. The action speaks the rendered
// text with its provider (B41) and, with waitForFinish, waits for the
// end of the speech and reports it to the engine (B14, B43).
func (t TextToSpeech) Perform(ctx context.Context, run *engine.Run) error {
	ports := t.ports
	if ports == nil {
		return errors.New("text_to_speech action: no ports (catalog action)")
	}
	text, err := ports.Templates.Render(ctx, t.Text.Parse(), run.Scope(), template.Text)
	if err != nil {
		return field("text", err)
	}
	provider := ttspkg.Name(t.Provider)
	if provider == "" {
		provider = ttspkg.Name(ports.DefaultProvider)
	}
	sink := t.Sink
	if sink == "" {
		sink = ports.DefaultSink
	}
	speech := ttspkg.Speech{
		Text:   text,
		Voice:  t.Voice,
		Volume: t.Volume,
		Rate:   orOne(t.Rate),
		Pitch:  orOne(t.Pitch),
	}
	var end <-chan struct{}
	switch {
	case provider == ttspkg.Browser:
		if sink != media.SinkOverlay {
			return field("sink", fmt.Errorf("%w: the provider browser speaks on the overlay (sink %s)", action.ErrInvalid, media.SinkOverlay))
		}
		end, err = ports.Browser.Speak(ctx, speech)
	case provider.IsFileProvider():
		if c := capabilityFor(provider); c != "" && !ports.Rights.Current().Has(c) {
			return field("provider", fmt.Errorf("the provider %s needs the capability %s, which the core does not have now", provider, c))
		}
		var file string
		file, err = ports.TTS.Generate(ctx, provider, speech)
		if err != nil {
			return field("provider", err)
		}
		end, err = ports.Sinks.Play(ctx, sink, file, t.Volume, ports.Rights)
	default:
		return field("provider", fmt.Errorf("%w: unknown provider %q", action.ErrInvalid, provider))
	}
	if err != nil {
		return field("speak", err)
	}
	ports.Logger.InfoContext(ctx, "text to speech", "instance_id", run.InstanceID(), "provider", provider, "sink", sink, "wait", t.Wait)
	if !t.Wait {
		// B41, B43: without the wait the engine counts the media gap
		// from the end of the action, not from the end of the speech.
		return nil
	}
	select {
	case <-end:
		run.PlaybackEnds(time.Now())
		return nil
	case <-ctx.Done():
		// B46: the time limit ends the action; the speech ends with
		// it, as far as the engine knows.
		return ctx.Err()
	}
}

// capabilityFor is the capability the provider needs to run (B41,
// ADR-0020 §3); empty for none. The check is at the action with the
// rights of the core now, not at the configuration (ADR-0020 §2).
func capabilityFor(p ttspkg.Name) capability.Capability {
	switch p {
	case ttspkg.Piper:
		return capability.HostProcess
	case ttspkg.Google, ttspkg.Azure:
		return capability.NetOutbound
	default:
		return ""
	}
}

// orOne returns v if it is positive, 1 otherwise.
func orOne(v float64) float64 {
	if v > 0 {
		return v
	}
	return 1
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
