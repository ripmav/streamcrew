// SPDX-License-Identifier: MIT

// Package polydoc stores polymorphic values as flat JSON documents with a
// type discriminator and a schema version (Code-ADR-0010):
//
//	{"type": "chat.send", "schemaVersion": 1, "message": "Hello $username!"}
//
// A Registry per family (actions, requirements, settings sections …) knows
// the types, decodes the current version strictly, migrates older versions
// step by step and keeps unknown types and too new versions unchanged, so
// that no data is lost.
package polydoc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Header keys of every document.
const (
	KeyType          = "type"
	KeySchemaVersion = "schemaVersion"
)

// Document is implemented by every value of a family: it names its type ID.
type Document interface {
	DocType() string
}

// Raw is implemented by values that carry their original JSON, such as the
// placeholders for unknown types. Encode writes it unchanged.
type Raw interface {
	RawJSON() json.RawMessage
}

// Unknown holds a document the registry cannot decode: an unknown type or a
// newer version than supported. The family turns it into a placeholder
// value that is not executed.
type Unknown struct {
	Type    string
	Version int
	Raw     json.RawMessage
	Reason  string
}

// Migration upgrades a document by one version. It works on the decoded
// JSON object without the header keys, in which numbers are json.Number,
// and must not keep references to it.
type Migration func(doc map[string]any) error

// Entry describes one type of a family.
type Entry[T Document] struct {
	// Type is the stable type ID, e.g. "chat.send".
	Type string
	// Version is the current schema version, starting at 1.
	Version int
	// Decode decodes the fields of the current version, without the header
	// keys. Strict is the usual implementation.
	Decode func(data []byte) (T, error)
	// Migrations[i] upgrades version i+1 to i+2; there are Version-1 of them.
	Migrations []Migration
}

// Registry decodes and encodes the documents of one family. Build it in the
// composition root; it is safe for concurrent use once built.
type Registry[T Document] struct {
	family  string
	entries map[string]Entry[T]
	unknown func(Unknown) T
}

// NewRegistry returns an empty registry. unknown wraps documents the
// registry cannot decode into a placeholder of the family; the placeholder
// should implement Raw, so that Encode writes the original back. Without
// unknown (nil), Decode returns an error for such documents instead.
func NewRegistry[T Document](family string, unknown func(Unknown) T) *Registry[T] {
	return &Registry[T]{family: family, entries: make(map[string]Entry[T]), unknown: unknown}
}

// Register adds a type.
func (r *Registry[T]) Register(e Entry[T]) error {
	switch {
	case e.Type == "":
		return fmt.Errorf("%s: empty type", r.family)
	case e.Version < 1:
		return fmt.Errorf("%s %q: version must be at least 1, got %d", r.family, e.Type, e.Version)
	case len(e.Migrations) != e.Version-1:
		return fmt.Errorf("%s %q: version %d needs %d migrations, got %d", r.family, e.Type, e.Version, e.Version-1, len(e.Migrations))
	case e.Decode == nil:
		return fmt.Errorf("%s %q: no decode function", r.family, e.Type)
	case slices.ContainsFunc(e.Migrations, func(m Migration) bool { return m == nil }):
		return fmt.Errorf("%s %q: empty migration", r.family, e.Type)
	}
	if _, dup := r.entries[e.Type]; dup {
		return fmt.Errorf("%s %q: already registered", r.family, e.Type)
	}
	r.entries[e.Type] = e
	return nil
}

// Types returns the registered type IDs in sorted order.
func (r *Registry[T]) Types() []string {
	return slices.Sorted(maps.Keys(r.entries))
}

// Version returns the current version of a type, or 0 if it is unknown.
func (r *Registry[T]) Version(typ string) int {
	return r.entries[typ].Version
}

// Decode reads a document. Unknown types and versions newer than supported
// come back as the family's placeholder, without an error.
func (r *Registry[T]) Decode(data []byte) (T, error) {
	var zero T
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return zero, fmt.Errorf("%s: decode document: %w", r.family, err)
	}
	if doc == nil {
		return zero, fmt.Errorf("%s: document is not a JSON object", r.family)
	}
	if dec.More() {
		return zero, fmt.Errorf("%s: trailing data after the document", r.family)
	}

	typ, ok := doc[KeyType].(string)
	if !ok || typ == "" {
		return zero, fmt.Errorf("%s: missing or invalid %q", r.family, KeyType)
	}
	version, err := schemaVersion(doc)
	if err != nil {
		return zero, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}

	e, ok := r.entries[typ]
	if !ok {
		return r.placeholder(typ, version, data, "unknown type")
	}
	if version == 0 {
		version = e.Version
	}
	if version > e.Version {
		return r.placeholder(typ, version, data, fmt.Sprintf("version %d is newer than the supported version %d", version, e.Version))
	}

	delete(doc, KeyType)
	delete(doc, KeySchemaVersion)
	for v := version; v < e.Version; v++ {
		if err := e.Migrations[v-1](doc); err != nil {
			return zero, fmt.Errorf("%s %q: migrate version %d to %d: %w", r.family, typ, v, v+1, err)
		}
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return zero, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	v, err := e.Decode(body)
	if err != nil {
		return zero, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	return v, nil
}

// Encode writes v in the current version of its type. Values that implement
// Raw are written unchanged.
func (r *Registry[T]) Encode(v T) ([]byte, error) {
	if raw, ok := any(v).(Raw); ok {
		return raw.RawJSON(), nil
	}
	typ := v.DocType()
	e, ok := r.entries[typ]
	if !ok {
		return nil, fmt.Errorf("%s: encode unregistered type %q", r.family, typ)
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	if len(body) < 2 || body[0] != '{' {
		return nil, fmt.Errorf("%s %q: value does not encode to a JSON object", r.family, typ)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	if _, clash := fields[KeyType]; clash {
		return nil, fmt.Errorf("%s %q: field %q is reserved", r.family, typ, KeyType)
	}
	if _, clash := fields[KeySchemaVersion]; clash {
		return nil, fmt.Errorf("%s %q: field %q is reserved", r.family, typ, KeySchemaVersion)
	}

	name, err := json.Marshal(typ)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	// The header comes first, so that stored documents read naturally.
	var out bytes.Buffer
	out.WriteString(`{"` + KeyType + `":`)
	out.Write(name)
	fmt.Fprintf(&out, `,"%s":%d`, KeySchemaVersion, e.Version)
	if len(fields) > 0 {
		out.WriteByte(',')
		out.Write(body[1:])
	} else {
		out.WriteByte('}')
	}
	return out.Bytes(), nil
}

func (r *Registry[T]) placeholder(typ string, version int, data []byte, reason string) (T, error) {
	if r.unknown == nil {
		var zero T
		return zero, fmt.Errorf("%s %q: %s", r.family, typ, reason)
	}
	var compact bytes.Buffer
	raw := json.RawMessage(slices.Clone(data))
	if err := json.Compact(&compact, data); err == nil {
		raw = compact.Bytes()
	}
	return r.unknown(Unknown{Type: typ, Version: version, Raw: raw, Reason: reason}), nil
}

// errVersion is returned for an invalid schemaVersion.
var errVersion = errors.New("invalid schemaVersion")

// schemaVersion reads the version; 0 means absent.
func schemaVersion(doc map[string]any) (int, error) {
	raw, ok := doc[KeySchemaVersion]
	if !ok {
		return 0, nil
	}
	n, ok := raw.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%w: %v", errVersion, raw)
	}
	v, err := n.Int64()
	if err != nil || v < 1 || v > 1<<20 {
		return 0, fmt.Errorf("%w: %s", errVersion, n)
	}
	return int(v), nil
}

// Strict decodes the fields of a document into C and rejects unknown fields,
// so that typos in handwritten documents are noticed.
func Strict[C any](data []byte) (C, error) {
	var c C
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, err
	}
	return c, nil
}
