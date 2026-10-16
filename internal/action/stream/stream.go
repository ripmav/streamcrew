// SPDX-License-Identifier: MIT

// Package stream is the stream action (roadmap 5.6, SetTitle, SetGame): it
// sets the title and the category of the stream of a platform.
package stream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeStream is the type ID of the stream action (Code-ADR-0013).
const TypeStream = "stream"

// Kind is what a stream action does.
type Kind string

// The kinds of the stream action.
const (
	// KindSetTitle sets the title of the stream (roadmap 5.6, SetTitle).
	KindSetTitle Kind = "set_title"
	// KindSetGame sets the category of the stream (roadmap 5.6, SetGame).
	KindSetGame Kind = "set_game"
)

// Kinds returns the kinds of the stream action.
func Kinds() []Kind { return []Kind{KindSetTitle, KindSetGame} }

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return slices.Contains(Kinds(), k) }

// Ports are what the stream type needs.
type Ports struct {
	// Templates renders the title or category.
	Templates *template.Engine
	// Platforms are the platforms the action acts on.
	Platforms Platforms
	// General reads the settings section "general": the default platform.
	General func(ctx context.Context) (settings.General, error)
	// Logger records the run.
	Logger *slog.Logger
}

// ports are the ports of the stream type.
type ports struct {
	Ports
}

// Platforms is what the action needs of the platforms (like the
// moderation action).
type Platforms interface {
	// Connected returns the platforms that are connected now.
	Connected() []connector.Platform
	// Platform returns the platform name; ok is false if the profile has
	// none.
	Platform(name platform.Name) (p connector.Platform, ok bool)
}

// Descriptors returns the stream type with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("stream action types: no template engine")
	case p.Platforms == nil:
		return nil, errors.New("stream action types: no platforms")
	case p.General == nil:
		return nil, errors.New("stream action types: no general settings")
	}
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	ports := &ports{Ports: p}
	return descriptors(ports), nil
}

// Catalog returns the stream type without ports, for the type catalog and
// commands as code (Code-ADR-0013, point 3): its actions decode, validate
// and encode, but must not run.
func Catalog() []action.Descriptor { return descriptors(nil) }

// descriptors returns the stream type with ports, which are nil in the
// catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeStream,
			Version:  1,
			Category: action.CategoryStream,
			Schema:   streamSchema(),
		}.WithKinds(KindSetTitle, func(k Kind) (Stream, bool) {
			if !k.Valid() {
				return Stream{}, false
			}
			return Stream{Common: action.On(), Kind: k, ports: ports}, true
		}),
	}
}

// streamSchema returns the schema of the stream action.
func streamSchema() *schema.Schema {
	return schema.Kinds(
		[]schema.Property{
			{Name: "text", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
		},
	)
}

// Stream is the stream action (roadmap 5.6, SetTitle, SetGame).
type Stream struct {
	action.Common `json:",embed"`
	Kind          Kind `json:"kind"`
	// Text is the new title or category; it is not empty.
	Text  action.Template `json:"text"`
	ports *ports
}

// DocType implements command.Action.
func (s Stream) DocType() string { return TypeStream }

// Validate implements command.Action.
func (s Stream) Validate() error {
	switch {
	case !s.Kind.Valid():
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, s.Kind))
	case s.Text == "":
		return field("text", fmt.Errorf("%w: empty text", action.ErrInvalid))
	default:
		return nil
	}
}

// Perform implements engine.Performer: it renders the text and sets the
// title or the category of the stream of the platform.
func (s Stream) Perform(ctx context.Context, run *engine.Run) error {
	p := run.Params()
	target, err := s.target(ctx, p)
	if err != nil {
		return err
	}
	ctl, ok := target.(connector.StreamControl)
	if !ok {
		return fmt.Errorf("%s: %w", target.Name(), connector.ErrNotImplemented)
	}
	rendered, err := s.ports.Templates.RenderEach(ctx, []template.Template{s.Text.Parse()}, run.Scope())
	if err != nil {
		return err
	}
	text := rendered[0].Text
	ch := connector.Channel(p.Channel)
	s.ports.Logger.InfoContext(ctx, "stream action", "kind", s.Kind, "platform", target.Name())
	switch s.Kind {
	case KindSetTitle:
		return ctl.SetTitle(ctx, ch, text)
	case KindSetGame:
		return ctl.SetGame(ctx, ch, text)
	default:
		return fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, s.Kind)
	}
}

// target returns the platform the action acts on: the platform of the run,
// or without one the default platform of the profile, first.
func (s Stream) target(ctx context.Context, p engine.Params) (connector.Platform, error) {
	if p.Platform == "" {
		targets := connector.DefaultFirst(s.ports.Platforms.Connected(), s.defaultPlatform(ctx))
		if len(targets) == 0 {
			return nil, fmt.Errorf("%w: no platform", connector.ErrNotConnected)
		}
		return targets[0], nil
	}
	target, ok := s.ports.Platforms.Platform(p.Platform)
	if !ok || !target.Status().Connected() {
		return nil, fmt.Errorf("%s: %w", p.Platform, connector.ErrNotConnected)
	}
	return target, nil
}

// defaultPlatform returns the default platform of the profile (general.
// defaultPlatform); a failed read keeps the section default.
func (s Stream) defaultPlatform(ctx context.Context) platform.Name {
	g, err := s.ports.General(ctx)
	if err != nil {
		s.ports.Logger.WarnContext(ctx, "reading the general settings failed", "error", err)
		return platform.Default
	}
	return g.DefaultPlatform
}

func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
