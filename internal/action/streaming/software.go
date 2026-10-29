// SPDX-License-Identifier: MIT

// Package streaming has the streaming_software action (roadmap 7.4, spec
// overlays.md, B42, B55): it runs the operations on the OBS Studio
// connection of the core (internal/obs), set_scene, the stream and
// record outputs, the replay buffer, the visibility and the position of
// a source of the current scene, and the visibility of a filter.
// Without an active OBS connection every run is a clear error of the
// action step (B55), never a wait.
package streaming

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/engine"
)

// TypeStreamingSoftware is the type ID of the streaming_software action
// (Code-ADR-0013, point 1).
const TypeStreamingSoftware = "streaming_software"

// Kind is what a streaming_software action does (B42).
type Kind string

// The kinds of the streaming_software action, in the order editors show
// them.
const (
	// KindSetScene sets the current program scene.
	KindSetScene Kind = "set_scene"
	// KindStartStream starts the stream output.
	KindStartStream Kind = "start_stream"
	// KindStopStream stops the stream output.
	KindStopStream Kind = "stop_stream"
	// KindStartRecording starts the record output.
	KindStartRecording Kind = "start_recording"
	// KindStopRecording stops the record output.
	KindStopRecording Kind = "stop_recording"
	// KindSaveReplay saves the replay buffer.
	KindSaveReplay Kind = "save_replay"
	// KindSetSource changes the visibility and/or the position of a
	// source of the current program scene.
	KindSetSource Kind = "set_source"
	// KindSetFilter sets the visibility of a filter of a source.
	KindSetFilter Kind = "set_filter"
)

// Kinds returns the kinds of the streaming_software action, in the order
// editors show them.
func Kinds() []Kind {
	return []Kind{
		KindSetScene,
		KindStartStream,
		KindStopStream,
		KindStartRecording,
		KindStopRecording,
		KindSaveReplay,
		KindSetSource,
		KindSetFilter,
	}
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return slices.Contains(Kinds(), k) }

// OBS is what the action needs of the OBS connection (B42, B55); the
// obs.Service of the core satisfies it.
type OBS interface {
	// SetScene sets the current program scene to name.
	SetScene(ctx context.Context, name string) error
	// StartStream starts the stream output.
	StartStream(ctx context.Context) error
	// StopStream stops the stream output.
	StopStream(ctx context.Context) error
	// StartRecording starts the record output.
	StartRecording(ctx context.Context) error
	// StopRecording stops the record output.
	StopRecording(ctx context.Context) error
	// SaveReplay saves the replay buffer.
	SaveReplay(ctx context.Context) error
	// SetSource changes the visibility and/or the position of the scene
	// item of source in the current program scene; a nil member stays as
	// it is.
	SetSource(ctx context.Context, ch SourceChange) error
	// SetFilter sets the visibility of the filter of the source.
	SetFilter(ctx context.Context, source, filter string, visible bool) error
}

// SourceChange is the change of the kind set_source (B42): the source of
// the current program scene and the members to change; a nil member stays
// as it is.
type SourceChange struct {
	// Source is the name of the source of the current program scene.
	Source string
	// Visible sets the visibility of the scene item; nil keeps it.
	Visible *bool
	// X and Y set the position of the scene item; nil keeps it.
	X, Y *float64
}

// Ports are what the streaming_software type needs.
type Ports struct {
	// OBS runs the operations on the OBS connection (B42, B55).
	OBS OBS
	// Logger records the operations and their results.
	Logger *slog.Logger
}

// ports are the ports of the streaming_software type.
type ports struct {
	Ports
}

// Descriptors returns the streaming_software type with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.OBS == nil:
		return nil, errors.New("streaming_software action types: no OBS connection")
	case p.Logger == nil:
		return nil, errors.New("streaming_software action types: no logger")
	}
	return descriptors(&ports{Ports: p}), nil
}

// Catalog returns the streaming_software type without ports, for the type
// catalog and commands as code (Code-ADR-0013, point 3): its actions
// decode, validate and encode, but must not run.
func Catalog() []action.Descriptor {
	return descriptors(nil)
}

// descriptors returns the streaming_software type with its ports, which
// are nil in the catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeStreamingSoftware,
			Version:  1,
			Category: action.CategoryObs,
			Schema:   softwareSchema(),
		}.WithKinds(KindSetScene, newSoftware(ports)),
	}
}

// softwareSchema returns the schema of the streaming_software action
// (B42): the kind picks the members.
func softwareSchema() *schema.Schema {
	position := &schema.Schema{Type: "number"}
	return schema.Kinds(
		nil,
		schema.Variant{Kind: string(KindSetScene), Props: []schema.Property{
			{Name: "scene", Schema: schema.NonEmpty(schema.UIText), Required: true},
		}},
		schema.Variant{Kind: string(KindStartStream)},
		schema.Variant{Kind: string(KindStopStream)},
		schema.Variant{Kind: string(KindStartRecording)},
		schema.Variant{Kind: string(KindStopRecording)},
		schema.Variant{Kind: string(KindSaveReplay)},
		schema.Variant{Kind: string(KindSetSource), Props: []schema.Property{
			{Name: "source", Schema: schema.NonEmpty(schema.UIText), Required: true},
			{Name: "visible", Schema: schema.Switch()},
			{Name: "x", Schema: position},
			{Name: "y", Schema: position},
		}, AnyOf: []*schema.Schema{
			{Required: []string{"visible"}},
			{Required: []string{"x"}},
			{Required: []string{"y"}},
		}},
		schema.Variant{Kind: string(KindSetFilter), Props: []schema.Property{
			{Name: "input", Schema: schema.NonEmpty(schema.UIText), Required: true},
			{Name: "filter", Schema: schema.NonEmpty(schema.UIText), Required: true},
			{Name: "show", Schema: schema.Switch(), Required: true},
		}},
	)
}

// Software is the streaming_software action (roadmap 7.4). Which members
// it has depends on its kind.
type Software struct {
	action.Common `json:",embed"`
	Kind          Kind `json:"kind"`
	// SetScene is the option of the kind set_scene; nil for the other
	// kinds.
	SetScene *SetSceneOptions `json:",embed"`
	// SetSource is the option of the kind set_source; nil for the other
	// kinds.
	SetSource *SetSourceOptions `json:",embed"`
	// SetFilter is the option of the kind set_filter; nil for the other
	// kinds.
	SetFilter *SetFilterOptions `json:",embed"`
	ports     *ports
}

// SetSceneOptions is the option of the kind set_scene (B42).
type SetSceneOptions struct {
	// Scene is the name of the program scene to switch to; not empty.
	Scene string `json:"scene"`
}

// SetSourceOptions is the option of the kind set_source (B42): the
// source of the current program scene, and the members to change; a
// member that is not there stays as it is.
type SetSourceOptions struct {
	// Source is the name of the source of the current program scene; not
	// empty.
	Source string `json:"source"`
	// Visible sets the visibility of the scene item; absent keeps it.
	Visible *bool `json:"visible,omitzero"`
	// X and Y set the position of the scene item; absent keeps it.
	X *float64 `json:"x,omitzero"`
	Y *float64 `json:"y,omitzero"`
}

// SetFilterOptions is the option of the kind set_filter (B42): the
// visibility the filter of the input gets. The members are "input" and
// "show", because the "source" of the kind set_source and the "enabled"
// of the header are taken, and the obs-websocket API names a source an
// input (Code-ADR-0013, point 4).
type SetFilterOptions struct {
	// Input is the name of the source the filter is on; not empty.
	Input string `json:"input"`
	// Filter is the name of the filter; not empty.
	Filter string `json:"filter"`
	// Show is the visibility the filter gets; the kind is the setting
	// of it, so the member is there.
	Show *bool `json:"show,omitzero"`
}

// newSoftware makes a streaming_software action of kind k with the
// defaults of that kind and the ports of the type; ok is false for an
// unknown kind.
func newSoftware(ports *ports) func(Kind) (Software, bool) {
	return func(k Kind) (Software, bool) {
		s := Software{Common: action.On(), Kind: k, ports: ports}
		switch k {
		case KindSetScene:
			s.SetScene = &SetSceneOptions{}
		case KindSetSource:
			s.SetSource = &SetSourceOptions{}
		case KindSetFilter:
			s.SetFilter = &SetFilterOptions{}
		default:
			if !k.Valid() {
				return Software{}, false
			}
		}
		return s, true
	}
}

// DocType implements command.Action.
func (Software) DocType() string { return TypeStreamingSoftware }

// Validate implements command.Action.
func (t Software) Validate() error {
	switch {
	case !t.Kind.Valid():
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, t.Kind))
	case t.Kind == KindSetScene:
		if t.SetScene == nil {
			return field("scene", fmt.Errorf("%w: the kind set_scene needs its options", action.ErrInvalid))
		}
		if t.SetScene.Scene == "" {
			return field("scene", fmt.Errorf("%w: an empty scene", action.ErrInvalid))
		}
	case t.Kind == KindSetSource:
		if t.SetSource == nil {
			return field("source", fmt.Errorf("%w: the kind set_source needs its options", action.ErrInvalid))
		}
		if t.SetSource.Source == "" {
			return field("source", fmt.Errorf("%w: an empty source", action.ErrInvalid))
		}
		// The kind changes something (B42); without a member it changes
		// nothing.
		if t.SetSource.Visible == nil && t.SetSource.X == nil && t.SetSource.Y == nil {
			return field("visible", fmt.Errorf("%w: no change, set visible, x or y", action.ErrInvalid))
		}
	case t.Kind == KindSetFilter:
		if t.SetFilter == nil {
			return field("input", fmt.Errorf("%w: the kind set_filter needs its options", action.ErrInvalid))
		}
		if t.SetFilter.Input == "" {
			return field("input", fmt.Errorf("%w: an empty input", action.ErrInvalid))
		}
		if t.SetFilter.Filter == "" {
			return field("filter", fmt.Errorf("%w: an empty filter", action.ErrInvalid))
		}
		if t.SetFilter.Show == nil {
			return field("show", fmt.Errorf("%w: the kind set_filter sets the visibility", action.ErrInvalid))
		}
	case t.SetScene != nil || t.SetSource != nil || t.SetFilter != nil:
		return field("kind", fmt.Errorf("%w: only its kind has its options", action.ErrInvalid))
	}
	return nil
}

// Perform implements engine.Performer. The action runs its kind on the
// OBS connection of the core (B42); without the connection it is a clear
// error of the step, not a wait (B55).
func (t Software) Perform(ctx context.Context, run *engine.Run) error {
	ports := t.ports
	if ports == nil {
		return errors.New("streaming_software action: no ports (catalog action)")
	}
	switch t.Kind {
	case KindSetScene:
		if err := ports.OBS.SetScene(ctx, t.SetScene.Scene); err != nil {
			return field("scene", err)
		}
	case KindStartStream:
		if err := ports.OBS.StartStream(ctx); err != nil {
			return field("kind", err)
		}
	case KindStopStream:
		if err := ports.OBS.StopStream(ctx); err != nil {
			return field("kind", err)
		}
	case KindStartRecording:
		if err := ports.OBS.StartRecording(ctx); err != nil {
			return field("kind", err)
		}
	case KindStopRecording:
		if err := ports.OBS.StopRecording(ctx); err != nil {
			return field("kind", err)
		}
	case KindSaveReplay:
		if err := ports.OBS.SaveReplay(ctx); err != nil {
			return field("kind", err)
		}
	case KindSetSource:
		if err := ports.OBS.SetSource(ctx, SourceChange{
			Source:  t.SetSource.Source,
			Visible: t.SetSource.Visible,
			X:       t.SetSource.X,
			Y:       t.SetSource.Y,
		}); err != nil {
			return field("source", err)
		}
	case KindSetFilter:
		if err := ports.OBS.SetFilter(ctx, t.SetFilter.Input, t.SetFilter.Filter, *t.SetFilter.Show); err != nil {
			return field("filter", err)
		}
	default:
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, t.Kind))
	}
	ports.Logger.InfoContext(ctx, "streaming_software action", "instance_id", run.InstanceID(), "kind", t.Kind)
	return nil
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
