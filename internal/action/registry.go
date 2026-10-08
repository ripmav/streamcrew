// SPDX-License-Identifier: MIT

package action

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// Registry knows the action types (Code-ADR-0013, point 3). Build it in the
// composition root; it is safe for concurrent use once built. It implements
// engine.ActionTypes.
type Registry struct {
	types map[string]Descriptor
	// granted are the capabilities the core has.
	granted capability.Set
}

// NewRegistry returns a registry of the descriptors. granted are the
// capabilities the core has in its operating mode (ADR-0013). Every
// descriptor is checked; an error names the type, and the core does not
// start with it.
func NewRegistry(granted capability.Set, descriptors ...Descriptor) (*Registry, error) {
	r := &Registry{types: make(map[string]Descriptor, len(descriptors)), granted: granted}
	docs := polydoc.NewRegistry("action", func(u polydoc.Unknown) command.Action { return command.UnknownAction{Unknown: u} })
	var errs []error
	for _, d := range descriptors {
		if _, dup := r.types[d.Type]; dup {
			errs = append(errs, fmt.Errorf("%w: type %q registered twice", ErrInvalid, d.Type))
			continue
		}
		d, err := prepare(d, docs)
		if err != nil {
			errs = append(errs, fmt.Errorf("action type %q: %w", d.Type, err))
			continue
		}
		r.types[d.Type] = d
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return r, nil
}

// prepare checks d, registers it with docs and returns it with its schema
// bound to its type and the defaults of New.
func prepare(d Descriptor, docs *polydoc.Registry[command.Action]) (Descriptor, error) {
	switch {
	case len(d.Type) > MaxTypeLength || !typePattern.MatchString(d.Type):
		return d, fmt.Errorf("%w: type ID %q is not lowercase words joined by \"_\" of at most %d characters", ErrInvalid, d.Type, MaxTypeLength)
	case !d.Category.Valid():
		return d, fmt.Errorf("%w: unknown category %q", ErrInvalid, d.Category)
	case d.Schema == nil:
		return d, fmt.Errorf("%w: no schema", ErrInvalid)
	case d.New == nil:
		return d, fmt.Errorf("%w: no constructor", ErrInvalid)
	}
	for _, c := range d.Capabilities {
		if !c.Valid() {
			return d, fmt.Errorf("%w: unknown capability %q", ErrInvalid, c)
		}
	}
	for _, name := range d.Results {
		if err := ResultName(name).Validate(); err != nil {
			return d, fmt.Errorf("fixed result name: %w", err)
		}
	}
	if err := docs.Register(d.entry()); err != nil {
		return d, err
	}
	a := d.New()
	if a == nil || a.DocType() != d.Type {
		return d, fmt.Errorf("%w: New makes no action of the type", ErrInvalid)
	}
	encoded, err := docs.Encode(a)
	if err != nil {
		return d, fmt.Errorf("encode a new action: %w", err)
	}
	var defaults map[string]jsontext.Value
	if err := json.Unmarshal(encoded, &defaults); err != nil {
		return d, err
	}
	s := d.Schema
	d.Schema = s.Clone()
	d.Schema.BindType(d.Type)
	d.Schema.SetDefaults(defaults)
	if err := d.Schema.Validate(); err != nil {
		return d, fmt.Errorf("%w: schema: %w", ErrInvalid, err)
	}
	for name := range defaults {
		if name == polydoc.KeySchemaVersion {
			continue
		}
		if _, ok := d.Schema.Properties.Lookup(name); !ok {
			return d, fmt.Errorf("%w: member %q of a new action is not in the schema", ErrInvalid, name)
		}
	}
	d.Capabilities = slices.Clone(d.Capabilities)
	d.Results = slices.Clone(d.Results)
	d.Migrations = slices.Clone(d.Migrations)
	return d, nil
}

// Reserved reports whether name, regardless of case, is a fixed result name
// of an action type (actions.md B5), and which one. Saving rejects names
// the streamer chooses that hide one; the composition root joins it with
// template.Registry.Reserved for command.Names.
func (r *Registry) Reserved(name string) (fixed string, reserved bool) {
	for _, d := range r.types {
		for _, fixed := range d.Results {
			if strings.EqualFold(name, fixed) {
				return fixed, true
			}
		}
	}
	return "", false
}

// Descriptors returns the descriptors sorted by type ID, for the type
// catalog and the export of schemas.
func (r *Registry) Descriptors() []Descriptor {
	list := make([]Descriptor, 0, len(r.types))
	for _, typ := range slices.Sorted(maps.Keys(r.types)) {
		list = append(list, r.types[typ])
	}
	return list
}

// Descriptor returns the descriptor of typ; ok is false if the type is
// unknown.
func (r *Registry) Descriptor(typ string) (d Descriptor, ok bool) {
	d, ok = r.types[typ]
	return d, ok
}

// Entries returns the entries for command.NewCodec, sorted by type ID.
func (r *Registry) Entries() []polydoc.Entry[command.Action] {
	list := make([]polydoc.Entry[command.Action], 0, len(r.types))
	for _, d := range r.Descriptors() {
		list = append(list, d.entry())
	}
	return list
}

// VisualAudio implements engine.ActionTypes (command-engine.md B23).
func (r *Registry) VisualAudio(actionType string) bool {
	return r.types[actionType].VisualAudio
}

// Missing implements engine.ActionTypes: the capabilities that actions of
// the type need and the core does not have (actions.md B7). The engine runs
// only actions of known types, so for an unknown type the list is empty.
func (r *Registry) Missing(actionType string) []capability.Capability {
	return r.granted.Missing(r.types[actionType].Capabilities)
}
