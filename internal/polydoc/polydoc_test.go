// SPDX-License-Identifier: Apache-2.0

package polydoc_test

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/polydoc"
)

// step is a test family, like actions.
type step interface {
	polydoc.Document
	isStep()
}

// say is at version 2: version 1 called the field "text".
type say struct {
	Message string `json:"message"`
	AsBot   bool   `json:"asBot,omitzero"`
}

func (say) DocType() string { return "chat.say" }
func (say) isStep()         {}

type wait struct {
	Millis int64 `json:"millis"`
}

func (wait) DocType() string { return "flow.wait" }
func (wait) isStep()         {}

// block holds steps of its own family, like an action with child actions.
type block struct {
	Steps []step `json:"steps"`
}

func (block) DocType() string { return "flow.block" }
func (block) isStep()         {}

// unknownStep keeps documents the registry cannot decode.
type unknownStep struct{ polydoc.Unknown }

func (u unknownStep) DocType() string         { return u.Type }
func (u unknownStep) RawJSON() jsontext.Value { return u.Raw }
func (unknownStep) isStep()                   {}

func newRegistry(t *testing.T) *polydoc.Registry[step] {
	t.Helper()
	r := polydoc.NewRegistry("step", func(u polydoc.Unknown) step { return unknownStep{u} })
	require.NoError(t, r.Register(polydoc.Entry[step]{
		Type:    "chat.say",
		Version: 2,
		Decode: func(data []byte, opts json.Options) (step, error) {
			return polydoc.Strict[say](data, opts)
		},
		Migrations: []polydoc.Migration{
			func(doc map[string]jsontext.Value) error { // 1 → 2: "text" becomes "message"
				doc["message"] = doc["text"]
				delete(doc, "text")
				return nil
			},
		},
	}))
	require.NoError(t, r.Register(polydoc.Entry[step]{
		Type:    "flow.wait",
		Version: 1,
		Decode: func(data []byte, opts json.Options) (step, error) {
			w, err := polydoc.Strict[wait](data, opts)
			if err == nil && w.Millis < 0 {
				err = errors.New("millis must not be negative")
			}
			return w, err
		},
	}))
	require.NoError(t, r.Register(polydoc.Entry[step]{
		Type:    "flow.block",
		Version: 1,
		Decode: func(data []byte, opts json.Options) (step, error) {
			return polydoc.Strict[block](data, opts)
		},
	}))
	return r
}

func TestDecodeCurrentVersion(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)

	v, err := r.Decode([]byte(`{"type":"chat.say","schemaVersion":2,"message":"hi $user","asBot":true}`))
	require.NoError(t, err)
	assert.Equal(t, say{Message: "hi $user", AsBot: true}, v)

	v, err = r.Decode([]byte(`{"type":"flow.wait","millis":1500}`))
	require.NoError(t, err)
	assert.Equal(t, wait{Millis: 1500}, v, "a missing schemaVersion means the current version")
}

func TestDecodeMigratesOlderVersions(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	v, err := r.Decode([]byte(`{"type":"chat.say","schemaVersion":1,"text":"old"}`))
	require.NoError(t, err)
	assert.Equal(t, say{Message: "old"}, v)

	out, err := r.Encode(v)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"chat.say","schemaVersion":2,"message":"old"}`, string(out), "encoded in the current version")
}

func TestUnknownIsPreserved(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	tests := []struct {
		name       string
		doc        string
		wantReason string
	}{
		{name: "unknown type", doc: `{"type": "obs.scene", "schemaVersion": 3, "scene": "BRB"}`, wantReason: "unknown type"},
		{name: "newer version", doc: `{"type": "chat.say", "schemaVersion": 7, "message": "x", "future": [1, 2]}`, wantReason: "newer than the supported version 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, err := r.Decode([]byte(tc.doc))
			require.NoError(t, err)
			u, ok := v.(unknownStep)
			require.True(t, ok, "got %T", v)
			assert.Contains(t, u.Reason, tc.wantReason)

			out, err := r.Encode(v)
			require.NoError(t, err)
			assert.JSONEq(t, tc.doc, string(out), "written back unchanged")
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	for name, doc := range map[string]string{
		"not JSON":               `{"type":`,
		"not an object":          `["chat.say"]`,
		"null":                   `null`,
		"trailing data":          `{"type":"flow.wait","millis":1} {}`,
		"missing type":           `{"millis":1}`,
		"type not a string":      `{"type":7}`,
		"version zero":           `{"type":"flow.wait","schemaVersion":0,"millis":1}`,
		"version not a number":   `{"type":"flow.wait","schemaVersion":"1","millis":1}`,
		"version fractional":     `{"type":"flow.wait","schemaVersion":1.5,"millis":1}`,
		"unknown field (typo)":   `{"type":"flow.wait","milis":1}`,
		"validation in decode":   `{"type":"flow.wait","millis":-1}`,
		"wrong field type":       `{"type":"chat.say","message":5}`,
		"migrated field clash":   `{"type":"chat.say","schemaVersion":1,"text":"a","message":"b","extra":1}`,
		"huge version":           `{"type":"flow.wait","schemaVersion":99999999999,"millis":1}`,
		"number too big for i64": `{"type":"flow.wait","millis":1e30}`,
		// Strict reading of encoding/json/v2 (Code-ADR-0018).
		"duplicate name":     `{"type":"flow.wait","millis":1,"millis":2}`,
		"other spelling":     `{"type":"flow.wait","Millis":1}`,
		"invalid UTF-8":      "{\"type\":\"chat.say\",\"message\":\"\xff\"}",
		"version with a dot": `{"type":"flow.wait","schemaVersion":1.0,"millis":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := r.Decode([]byte(doc))
			assert.Error(t, err)
		})
	}
}

func TestEncodeLayout(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	out, err := r.Encode(say{Message: "hello"})
	require.NoError(t, err)
	assert.Equal(t, `{"type":"chat.say","schemaVersion":2,"message":"hello"}`, string(out), "header first")

	out, err = r.Encode(wait{})
	require.NoError(t, err)
	assert.Equal(t, `{"type":"flow.wait","schemaVersion":1,"millis":0}`, string(out))
}

type empty struct{}

func (empty) DocType() string { return "flow.noop" }
func (empty) isStep()         {}

type clash struct {
	Type string `json:"type"`
}

func (clash) DocType() string { return "flow.clash" }
func (clash) isStep()         {}

func TestEncodeEdgeCases(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	require.NoError(t, r.Register(polydoc.Entry[step]{Type: "flow.noop", Version: 1, Decode: func([]byte, json.Options) (step, error) { return empty{}, nil }}))
	require.NoError(t, r.Register(polydoc.Entry[step]{Type: "flow.clash", Version: 1, Decode: func([]byte, json.Options) (step, error) { return clash{}, nil }}))

	out, err := r.Encode(empty{})
	require.NoError(t, err)
	assert.Equal(t, `{"type":"flow.noop","schemaVersion":1}`, string(out))

	_, err = r.Encode(clash{Type: "x"})
	require.ErrorContains(t, err, "reserved")

	_, err = polydoc.NewRegistry[step]("step", nil).Encode(say{})
	require.ErrorContains(t, err, "unregistered")
}

func TestRegisterValidation(t *testing.T) {
	t.Parallel()
	decode := func([]byte, json.Options) (step, error) { return wait{}, nil }
	r := polydoc.NewRegistry[step]("step", nil)
	for name, e := range map[string]polydoc.Entry[step]{
		"empty type":         {Version: 1, Decode: decode},
		"version zero":       {Type: "a.b", Decode: decode},
		"missing migrations": {Type: "a.b", Version: 3, Decode: decode, Migrations: []polydoc.Migration{nil}},
		"empty migration":    {Type: "a.b", Version: 2, Decode: decode, Migrations: []polydoc.Migration{nil}},
		"no decode":          {Type: "a.b", Version: 1},
	} {
		assert.Error(t, r.Register(e), name)
	}
	require.NoError(t, r.Register(polydoc.Entry[step]{Type: "a.b", Version: 1, Decode: decode}))
	assert.ErrorContains(t, r.Register(polydoc.Entry[step]{Type: "a.b", Version: 1, Decode: decode}), "already registered")
	assert.Equal(t, []string{"a.b"}, r.Types())
	version, ok := r.Version("a.b")
	assert.True(t, ok)
	assert.Equal(t, 1, version)
	_, ok = r.Version("x.y")
	assert.False(t, ok)
}

// TestWithoutPlaceholder checks that a registry without a placeholder for
// unknown documents fails with an error instead of a panic.
func TestWithoutPlaceholder(t *testing.T) {
	t.Parallel()
	r := polydoc.NewRegistry[step]("step", nil)
	decode := func([]byte, json.Options) (step, error) { return wait{}, nil }
	require.NoError(t, r.Register(polydoc.Entry[step]{Type: "flow.wait", Version: 1, Decode: decode}))

	_, err := r.Decode([]byte(`{"type":"obs.scene","schemaVersion":1}`))
	require.ErrorContains(t, err, "unknown type")
	_, err = r.Decode([]byte(`{"type":"flow.wait","schemaVersion":2,"millis":1}`))
	require.ErrorContains(t, err, "newer than the supported version")
}

func TestMigrationError(t *testing.T) {
	t.Parallel()
	r := polydoc.NewRegistry[step]("step", nil)
	require.NoError(t, r.Register(polydoc.Entry[step]{
		Type: "a.b", Version: 2,
		Decode:     func([]byte, json.Options) (step, error) { return wait{}, nil },
		Migrations: []polydoc.Migration{func(map[string]jsontext.Value) error { return errors.New("cannot migrate") }},
	}))
	_, err := r.Decode([]byte(`{"type":"a.b","schemaVersion":1}`))
	assert.ErrorContains(t, err, "migrate version 1 to 2: cannot migrate")
}

func TestNestedDocuments(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	v, err := r.Decode([]byte(`{"type": "flow.block", "steps": [
		{"type": "chat.say", "schemaVersion": 1, "text": "old"},
		{"type": "obs.scene", "scene": "BRB"},
		{"type": "flow.block", "steps": [{"type": "flow.wait", "millis": 5}]}
	]}`))
	require.NoError(t, err)
	b, ok := v.(block)
	require.True(t, ok, "got %T", v)
	require.Len(t, b.Steps, 3)
	assert.Equal(t, say{Message: "old"}, b.Steps[0], "nested documents are migrated")
	assert.IsType(t, unknownStep{}, b.Steps[1], "unknown nested types are kept")
	assert.Equal(t, block{Steps: []step{wait{Millis: 5}}}, b.Steps[2])

	out, err := r.Encode(v)
	require.NoError(t, err)
	assert.Equal(t, `{"type":"flow.block","schemaVersion":1,"steps":[`+
		`{"type":"chat.say","schemaVersion":2,"message":"old"},`+
		`{"type":"obs.scene","scene":"BRB"},`+
		`{"type":"flow.block","schemaVersion":1,"steps":[{"type":"flow.wait","schemaVersion":1,"millis":5}]}]}`,
		string(out), "nested documents get their header, unknown ones stay unchanged")

	out, err = r.Encode(block{})
	require.NoError(t, err)
	assert.Equal(t, `{"type":"flow.block","schemaVersion":1,"steps":[]}`, string(out))
}

func TestNestedDocumentErrors(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	for name, doc := range map[string]string{
		"null":               `{"type":"flow.block","steps":[null]}`,
		"not an object":      `{"type":"flow.block","steps":[7]}`,
		"missing type":       `{"type":"flow.block","steps":[{"millis":1}]}`,
		"unknown field":      `{"type":"flow.block","steps":[{"type":"flow.wait","milis":1}]}`,
		"validation":         `{"type":"flow.block","steps":[{"type":"flow.wait","millis":-1}]}`,
		"deeper error":       `{"type":"flow.block","steps":[{"type":"flow.block","steps":[{"type":7}]}]}`,
		"steps not an array": `{"type":"flow.block","steps":{"type":"flow.wait","millis":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := r.Decode([]byte(doc))
			assert.Error(t, err)
		})
	}
}

// nestedDoc returns a block document that nests depth levels deep.
func nestedDoc(depth int) string {
	return strings.Repeat(`{"type":"flow.block","steps":[`, depth-1) +
		`{"type":"flow.block","steps":[]}` + strings.Repeat(`]}`, depth-1)
}

// nestedBlock returns a block value that nests depth levels deep.
func nestedBlock(depth int) step {
	v := block{Steps: []step{}}
	for range depth - 1 {
		v = block{Steps: []step{v}}
	}
	return v
}

func TestMaxDepth(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)

	v, err := r.Decode([]byte(nestedDoc(polydoc.MaxDepth)))
	require.NoError(t, err)
	assert.Equal(t, nestedBlock(polydoc.MaxDepth), v)
	_, err = r.Encode(v)
	require.NoError(t, err)

	_, err = r.Decode([]byte(nestedDoc(polydoc.MaxDepth + 1)))
	require.ErrorIs(t, err, polydoc.ErrTooDeep)
	_, err = r.Encode(nestedBlock(polydoc.MaxDepth + 1))
	require.ErrorIs(t, err, polydoc.ErrTooDeep)

	// A placeholder for an unknown type is not looked into, so it does not
	// count beyond its own level.
	_, err = r.Decode([]byte(strings.Replace(nestedDoc(polydoc.MaxDepth), `{"type":"flow.block","steps":[]}`, `{"type":"obs.scene","steps":[{"type":"x"}]}`, 1)))
	require.NoError(t, err)
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"type":"chat.say","schemaVersion":1,"text":"old"}`))
	f.Add([]byte(`{"type":"flow.block","steps":[{"type":"flow.wait","millis":1},{"type":"obs.scene"},{"type":"flow.block","steps":[]}]}`))
	f.Add([]byte(nestedDoc(polydoc.MaxDepth + 1)))
	f.Add([]byte(`{"type":"flow.wait","millis":1500}`))
	f.Add([]byte(`{"type":"obs.scene","scene":"BRB"}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		r := newRegistry(t)
		v, err := r.Decode(data)
		if err != nil {
			return
		}
		// Whatever decodes must encode, decode again and encode to the same
		// bytes. Bytes, not values: a missing list decodes as nil and comes
		// back as an empty one.
		out, err := r.Encode(v)
		require.NoError(t, err)
		again, err := r.Decode(out)
		require.NoError(t, err)
		assert.Equal(t, v.DocType(), again.DocType())
		out2, err := r.Encode(again)
		require.NoError(t, err)
		assert.Equal(t, string(out), string(out2))
	})
}
