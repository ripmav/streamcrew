// SPDX-License-Identifier: Apache-2.0

package command

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"

	"github.com/ripmav/streamcrew/internal/polydoc"
)

// Action is a single step of a command (B4). The action types come from
// the action type registry (internal/action, Code-ADR-0013); the engine
// runs them.
type Action interface {
	polydoc.Document
	// Validate checks the configuration of the action itself, without its
	// child actions (Code-ADR-0013, point 7).
	Validate() error
}

// Parent is an action with child actions, such as a condition
// (Code-ADR-0013). Children returns them always in the same order.
type Parent interface {
	Children() []Action
}

// UnknownAction keeps an action this version cannot read; it is saved
// unchanged and not run (B4, Code-ADR-0010).
type UnknownAction struct{ polydoc.Unknown }

// DocType implements polydoc.Document.
func (u UnknownAction) DocType() string { return u.Type }

// Validate implements Action: an unknown action is kept as it is.
func (UnknownAction) Validate() error { return nil }

// RawJSON implements polydoc.Raw.
func (u UnknownAction) RawJSON() jsontext.Value { return u.Raw }

// Codec converts between commands and their stored form. Build it in the
// composition root; it is safe for concurrent use.
type Codec struct {
	actions      *polydoc.Registry[Action]
	requirements *polydoc.Registry[Requirement]
}

// NewCodec returns a codec that knows the requirement types of this package
// and the given action types.
func NewCodec(actions ...polydoc.Entry[Action]) (*Codec, error) {
	c := &Codec{
		actions:      polydoc.NewRegistry("action", func(u polydoc.Unknown) Action { return UnknownAction{u} }),
		requirements: polydoc.NewRegistry("requirement", func(u polydoc.Unknown) Requirement { return UnknownRequirement{u} }),
	}
	for _, e := range actions {
		if err := c.actions.Register(e); err != nil {
			return nil, err
		}
	}
	for _, e := range requirementTypes() {
		if err := c.requirements.Register(e); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// RequirementTypes returns the known requirement type IDs in sorted order.
func (c *Codec) RequirementTypes() []string {
	return c.requirements.Types()
}

// Action decodes one action document; an unknown type comes back as
// UnknownAction.
func (c *Codec) Action(data []byte) (Action, error) {
	return c.actions.Decode(data)
}

// Requirement decodes one requirement document; an unknown type comes back
// as UnknownRequirement.
func (c *Codec) Requirement(data []byte) (Requirement, error) {
	return c.requirements.Decode(data)
}

// Record encodes a command.
func (c *Codec) Record(cmd Command) (Record, error) {
	reqs, err := encodeList(c.requirements, cmd.Requirements)
	if err != nil {
		return Record{}, err
	}
	acts, err := encodeList(c.actions, cmd.Actions)
	if err != nil {
		return Record{}, err
	}
	return Record{Header: cmd.Header, Requirements: reqs, Actions: acts}, nil
}

// Command decodes a stored command. Unknown requirement and action types
// come back as UnknownRequirement and UnknownAction.
func (c *Codec) Command(rec Record) (Command, error) {
	reqs, err := decodeList(c.requirements, rec.Requirements)
	if err != nil {
		return Command{}, fmt.Errorf("command %s: %w", rec.ID, err)
	}
	acts, err := decodeList(c.actions, rec.Actions)
	if err != nil {
		return Command{}, fmt.Errorf("command %s: %w", rec.ID, err)
	}
	return Command{Header: rec.Header, Requirements: reqs, Actions: acts}, nil
}

// encodeList writes a JSON array of documents.
func encodeList[T polydoc.Document](r *polydoc.Registry[T], items []T) (jsontext.Value, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if any(item) == nil {
			return nil, fmt.Errorf("%w: empty entry in a document list", ErrInvalid)
		}
		doc, err := r.Encode(item)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(doc)
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// decodeList reads a JSON array of documents; empty input is an empty list.
func decodeList[T polydoc.Document](r *polydoc.Registry[T], data jsontext.Value) ([]T, error) {
	if len(data) == 0 {
		return []T{}, nil
	}
	var docs []jsontext.Value
	if err := json.Unmarshal(data, &docs); err != nil {
		return nil, fmt.Errorf("decode document list: %w", err)
	}
	items := make([]T, 0, len(docs))
	for _, doc := range docs {
		item, err := r.Decode(doc)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
