// SPDX-License-Identifier: MIT

// Package sound has the sound action (roadmap 7.3, spec overlays.md,
// B40, ADR-0020): it plays a file of the media directory at a volume
// from 0 to 100 on a sink, overlay (default) or local, and reports the
// end of the playback to the engine with PlaybackEnds while it waits
// for it (B43). The capability the sink needs is checked at the action
// with the rights of the core now, so the server mode without host:audio
// is a clear error for the sink local (ADR-0020 §2).
package sound

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
)

// TypeSound is the type ID of the sound action (Code-ADR-0013, point 1).
const TypeSound = "sound"

// Media resolves the files of the action under the media directory of
// the profile (B40, B47).
type Media interface {
	// File returns the absolute path of file under the media directory;
	// a clear error if file escapes the directory or is not a file.
	File(file string) (string, error)
}

// Sinks plays the files of the action (B40); the media.Service of the
// core satisfies it.
type Sinks interface {
	// Play plays file on the sink of the name at the volume with the
	// rights the core has now and returns a channel that is closed when
	// the playback is done.
	Play(ctx context.Context, name, file string, volume float64, rights capability.Source) (<-chan struct{}, error)
}

// Ports are the dependencies of the sound type.
type Ports struct {
	// Media resolves the files under the media directory (B47).
	Media Media
	// Sinks play the files on the sink of the action (B40).
	Sinks Sinks
	// Rights are the rights the core has now (ADR-0020 §2).
	Rights capability.Source
	// DefaultSink is the sink without one in the action
	// (audio-sink, ADR-0020 §2).
	DefaultSink string
	// Logger records the operations and their results.
	Logger *slog.Logger
}

// ports are the ports of the sound type.
type ports struct {
	Ports
}

// Descriptors returns the sound type with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Media == nil:
		return nil, errors.New("sound action types: no media resolver")
	case p.Sinks == nil:
		return nil, errors.New("sound action types: no sinks")
	case p.Rights == nil:
		return nil, errors.New("sound action types: no rights")
	case p.Logger == nil:
		return nil, errors.New("sound action types: no logger")
	}
	return descriptors(&ports{Ports: p}), nil
}

// Catalog returns the sound type without ports, for the type catalog
// and commands as code (Code-ADR-0013, point 3): its actions decode,
// validate and encode, but must not run.
func Catalog() []action.Descriptor {
	return descriptors(nil)
}

// descriptors returns the sound type with its ports, which are nil in
// the catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:        TypeSound,
			Version:     1,
			Category:    action.CategoryOverlay,
			VisualAudio: true,
			Schema:      soundSchema(),
		}.WithNew(newSound(ports)),
	}
}

// soundSchema returns the schema of the sound action (B40): the file of
// the media directory, the volume, the sink and the wait.
func soundSchema() *schema.Schema {
	return schema.Document(
		schema.Property{Name: "file", Schema: schema.NonEmpty(schema.UIText), Required: true},
		schema.Property{Name: "volume", Schema: volumeSchema()},
		schema.Property{Name: "sink", Schema: schema.Choice(media.Sinks()...)},
		schema.Property{Name: "waitForFinish", Schema: schema.Switch()},
	)
}

// volumeSchema is the volume from 0 to 100 of the media actions (B40).
func volumeSchema() *schema.Schema {
	return &schema.Schema{Type: "number", Minimum: new(0.0), Maximum: new(100.0)}
}

// Sound is the sound action (roadmap 7.3).
type Sound struct {
	action.Common `json:",embed"`
	// File is the path of the file under the media directory (B40,
	// B47); required.
	File string `json:"file"`
	// Volume is the volume from 0 to 100; 100 for full.
	Volume float64 `json:"volume"`
	// Sink is the sink to play on, one of the media sinks; empty for
	// the default (audio-sink, B40).
	Sink string `json:"sink,omitzero"`
	// Wait holds the action until the playback is done (B40); without
	// it, the action is done when the playback starts, and the engine
	// counts the media gap from the end of the action (B43).
	Wait  bool `json:"waitForFinish,omitzero"`
	ports *ports
}

// newSound makes a new sound action with the defaults and the ports of
// the type.
func newSound(ports *ports) func() Sound {
	return func() Sound {
		return Sound{Common: action.On(), Volume: 100, ports: ports}
	}
}

// DocType implements command.Action.
func (Sound) DocType() string { return TypeSound }

// Validate implements command.Action.
func (t Sound) Validate() error {
	switch {
	case t.File == "":
		return field("file", fmt.Errorf("%w: an empty file", action.ErrInvalid))
	case t.Volume < 0 || t.Volume > 100:
		return field("volume", fmt.Errorf("%w: the volume %g is 0 to 100", action.ErrInvalid, t.Volume))
	case t.Sink != "" && !media.ValidSink(t.Sink):
		return field("sink", fmt.Errorf("%w: unknown sink %q", action.ErrInvalid, t.Sink))
	default:
		return nil
	}
}

// Perform implements engine.Performer. The action plays the file on its
// sink (B40) and, with waitForFinish, waits for the end of the playback
// and reports it to the engine (B14, B43).
func (t Sound) Perform(ctx context.Context, run *engine.Run) error {
	ports := t.ports
	if ports == nil {
		return errors.New("sound action: no ports (catalog action)")
	}
	file, err := ports.Media.File(t.File)
	if err != nil {
		return field("file", err)
	}
	sink := t.Sink
	if sink == "" {
		sink = ports.DefaultSink
	}
	end, err := ports.Sinks.Play(ctx, sink, file, t.Volume, ports.Rights)
	if err != nil {
		return field("sink", err)
	}
	ports.Logger.InfoContext(ctx, "sound played", "instance_id", run.InstanceID(), "file", t.File, "sink", sink, "volume", t.Volume, "wait", t.Wait)
	if !t.Wait {
		// B40, B43: without the wait the engine counts the media gap
		// from the end of the action, not from the end of the playback.
		return nil
	}
	select {
	case <-end:
		run.PlaybackEnds(time.Now())
		return nil
	case <-ctx.Done():
		// B46: the time limit ends the action; the playback ends with
		// it, as far as the engine knows.
		return ctx.Err()
	}
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
