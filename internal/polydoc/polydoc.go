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
//
// A document may hold documents of its own family in fields of the family's
// type, such as the child actions of an action (Code-ADR-0013). The registry
// decodes and encodes them like top-level documents, at most MaxDepth levels
// deep.
//
// JSON goes through encoding/json/v2 (Code-ADR-0018): documents are read
// strictly and written deterministically.
package polydoc

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
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

// MaxDepth is how deep documents of a family may nest, counting the
// top-level document as level 1 (Code-ADR-0013). Decode and Encode reject
// deeper documents, so that documents from outside cannot drive them into
// deep recursion.
const MaxDepth = 16

// ErrTooDeep is returned for documents nested deeper than MaxDepth.
var ErrTooDeep = errors.New("documents nested too deep")

// Document is implemented by every value of a family: it names its type ID.
type Document interface {
	DocType() string
}

// Raw is implemented by values that carry their original JSON, such as the
// placeholders for unknown types. Encode writes it unchanged.
type Raw interface {
	RawJSON() jsontext.Value
}

// Unknown holds a document the registry cannot decode: an unknown type or a
// newer version than supported. The family turns it into a placeholder
// value that is not executed.
type Unknown struct {
	Type string
	// Version is the schemaVersion of the document; 0 if it has none.
	Version int
	Raw     jsontext.Value
	Reason  string
}

// Migration upgrades a document by one version. It works on the members of
// the JSON object without the header keys, each as its raw JSON, so that
// numbers keep their precision (Code-ADR-0018). It must not keep references
// to them.
type Migration func(doc map[string]jsontext.Value) error

// Entry describes one type of a family.
type Entry[T Document] struct {
	// Type is the stable type ID, e.g. "chat.send".
	Type string
	// Version is the current schema version, starting at 1.
	Version int
	// Decode decodes the fields of the current version, without the header
	// keys. opts decode the fields of the family's type through the
	// registry, as nested documents; Decode passes them on. Strict is the
	// usual implementation.
	Decode func(data []byte, opts json.Options) (T, error)
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

// Version returns the current version of a type; ok is false if the type is
// not registered.
func (r *Registry[T]) Version(typ string) (version int, ok bool) {
	e, ok := r.entries[typ]
	return e.Version, ok
}

// Decode reads a document. Unknown types and versions newer than supported
// come back as the family's placeholder, without an error, also as nested
// documents. A document without schemaVersion is in the current version of
// its type, as handwritten documents may be (Code-ADR-0010).
func (r *Registry[T]) Decode(data []byte) (T, error) {
	return r.decode(data, 1)
}

// decode reads a document at the given level of nesting.
func (r *Registry[T]) decode(data []byte, depth int) (T, error) {
	var zero T
	if depth > MaxDepth {
		return zero, fmt.Errorf("%s: %w: more than %d levels", r.family, ErrTooDeep, MaxDepth)
	}
	var doc map[string]jsontext.Value
	if err := json.Unmarshal(data, &doc); err != nil {
		return zero, fmt.Errorf("%s: decode document: %w", r.family, err)
	}
	if doc == nil {
		return zero, fmt.Errorf("%s: document is not a JSON object", r.family)
	}

	var typ string
	if raw, ok := doc[KeyType]; !ok || json.Unmarshal(raw, &typ) != nil || typ == "" {
		return zero, fmt.Errorf("%s: missing or invalid %q", r.family, KeyType)
	}
	version, hasVersion, err := schemaVersion(doc)
	if err != nil {
		return zero, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}

	e, ok := r.entries[typ]
	if !ok {
		return r.placeholder(typ, version, data, "unknown type")
	}
	if !hasVersion {
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
	body, err := json.Marshal(doc, json.Deterministic(true))
	if err != nil {
		return zero, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	v, err := e.Decode(body, r.nestedDecoding(depth))
	if err != nil {
		return zero, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	return v, nil
}

// nestedDecoding returns the options that decode fields of type T in a
// document at depth through the registry.
func (r *Registry[T]) nestedDecoding(depth int) json.Options {
	return json.WithUnmarshalers(json.UnmarshalFromFunc(func(dec *jsontext.Decoder, p *T) error {
		data, err := dec.ReadValue()
		if err != nil {
			return err
		}
		v, err := r.decode(data, depth+1)
		if err != nil {
			return err
		}
		*p = v
		return nil
	}))
}

// Encode writes v in the current version of its type, nested documents
// included. Values that implement Raw are written unchanged.
func (r *Registry[T]) Encode(v T) ([]byte, error) {
	return r.encode(v, 1)
}

// encode writes a document at the given level of nesting.
func (r *Registry[T]) encode(v T, depth int) ([]byte, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("%s: %w: more than %d levels", r.family, ErrTooDeep, MaxDepth)
	}
	if raw, ok := any(v).(Raw); ok {
		return raw.RawJSON(), nil
	}
	typ := v.DocType()
	e, ok := r.entries[typ]
	if !ok {
		return nil, fmt.Errorf("%s: encode unregistered type %q", r.family, typ)
	}
	body, err := json.Marshal(v, json.Deterministic(true), r.nestedEncoding(depth))
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	if len(body) < 2 || body[0] != '{' {
		return nil, fmt.Errorf("%s %q: value does not encode to a JSON object", r.family, typ)
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	if _, clash := fields[KeyType]; clash {
		return nil, fmt.Errorf("%s %q: field %q is reserved", r.family, typ, KeyType)
	}
	if _, clash := fields[KeySchemaVersion]; clash {
		return nil, fmt.Errorf("%s %q: field %q is reserved", r.family, typ, KeySchemaVersion)
	}

	name, err := jsontext.AppendQuote(nil, typ)
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

// nestedEncoding returns the options that encode values of type T in a
// document at depth through the registry. The marshaler applies to every
// type that implements T, the document itself included; at the top level
// it steps aside, so that the document's own fields are encoded as usual.
func (r *Registry[T]) nestedEncoding(depth int) json.Options {
	return json.WithMarshalers(json.MarshalToFunc(func(enc *jsontext.Encoder, v T) error {
		if enc.StackDepth() == 0 {
			return errors.ErrUnsupported
		}
		doc, err := r.encode(v, depth+1)
		if err != nil {
			return err
		}
		return enc.WriteValue(doc)
	}))
}

func (r *Registry[T]) placeholder(typ string, version int, data []byte, reason string) (T, error) {
	if r.unknown == nil {
		var zero T
		return zero, fmt.Errorf("%s %q: %s", r.family, typ, reason)
	}
	// Decode has read data as a JSON object, so compacting succeeds.
	raw := jsontext.Value(slices.Clone(data))
	if err := raw.Compact(); err != nil {
		var zero T
		return zero, fmt.Errorf("%s %q: %w", r.family, typ, err)
	}
	return r.unknown(Unknown{Type: typ, Version: version, Raw: raw, Reason: reason}), nil
}

// errVersion is returned for an invalid schemaVersion.
var errVersion = errors.New("invalid schemaVersion")

// schemaVersion reads the version of doc; ok is false if doc has none.
func schemaVersion(doc map[string]jsontext.Value) (version int, ok bool, err error) {
	raw, ok := doc[KeySchemaVersion]
	if !ok {
		return 0, false, nil
	}
	var v int64
	if err := json.Unmarshal(raw, &v); err != nil || v < 1 || v > 1<<20 {
		return 0, false, fmt.Errorf("%w: %s", errVersion, raw)
	}
	return int(v), true, nil
}

// Strict decodes the fields of a document into C and rejects unknown fields,
// so that typos in handwritten documents are noticed. opts are the options
// the registry passes to Entry.Decode.
func Strict[C any](data []byte, opts json.Options) (C, error) {
	var c C
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true), opts); err != nil {
		return c, err
	}
	return c, nil
}
