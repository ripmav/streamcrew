// SPDX-License-Identifier: Apache-2.0

package action

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"

	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// Descriptor describes an action type for the registry, the type catalog
// of the API and generic editors (plan §6.9, Code-ADR-0013, point 3).
type Descriptor struct {
	// Type is the stable type ID, e.g. "web_request" (point 1).
	Type string
	// Version is the current schema version, from 1.
	Version int
	// Category groups the type in the catalog.
	Category Category
	// Capabilities are needed to run actions of the type; empty for none
	// (ADR-0013).
	Capabilities []capability.Capability
	// VisualAudio types share the lock "visual_audio" (command-engine.md
	// B23).
	VisualAudio bool
	// Schema describes the configuration of the current version; build it
	// with schema.Document or schema.Kinds. The registry fixes its member
	// "type" and sets its defaults from New.
	Schema *schema.Schema
	// New returns a new action with the defaults for creating one
	// (point 4). Members without a default are missing from it.
	New func() command.Action
	// Decode decodes the members of the current version, starting from the
	// defaults of New; members that the schema requires must be there.
	// WithNew sets New and Decode.
	Decode func(data []byte, opts json.Options) (command.Action, error)
	// Migrations[i] upgrades version i+1 to i+2 (Code-ADR-0010).
	Migrations []polydoc.Migration
}

// WithNew returns d with New and Decode for actions of type A that newA
// makes with the defaults for creating one and the ports of the type.
// Decode starts from newA, so that a member missing in a handwritten
// document keeps its default; a member the schema requires must be there
// (Code-ADR-0013, point 4; Code-ADR-0017, point 6).
func (d Descriptor) WithNew[A command.Action](newA func() A) Descriptor {
	d.New = func() command.Action { return newA() }
	d.Decode = func(data []byte, opts json.Options) (command.Action, error) {
		if d.Schema == nil {
			return nil, fmt.Errorf("%w: type %q has no schema", ErrInvalid, d.Type)
		}
		if err := requireMembers(data, d.Schema.Required); err != nil {
			return nil, err
		}
		a := newA()
		if err := json.Unmarshal(data, &a, json.RejectUnknownMembers(true), opts); err != nil {
			return nil, err
		}
		return a, nil
	}
	return d
}

// entry returns the polydoc entry of d.
func (d Descriptor) entry() polydoc.Entry[command.Action] {
	return polydoc.Entry[command.Action]{Type: d.Type, Version: d.Version, Decode: d.Decode, Migrations: d.Migrations}
}

// requireMembers returns an error if data, a JSON object without the header
// keys, lacks one of names.
func requireMembers(data []byte, names []string) error {
	var members map[string]jsontext.Value
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	for _, name := range names {
		if name == polydoc.KeyType || name == polydoc.KeySchemaVersion {
			continue // header keys; polydoc has read them
		}
		if _, ok := members[name]; !ok {
			return fmt.Errorf("%w: member %q is missing", ErrInvalid, name)
		}
	}
	return nil
}
