// SPDX-License-Identifier: MIT

// Package actiontest is the conformance test of the action types
// (Code-ADR-0013, point 9). Only tests import it, so the JSON Schema
// validator it uses, github.com/santhosh-tekuri/jsonschema/v6, stays out of
// the binary.
package actiontest

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// Example is a document of an action type for the conformance test.
type Example struct {
	Name string
	// Doc is the document, as stored or handwritten.
	Doc string
	// Valid says whether the type accepts the document; its schema must
	// agree.
	Valid bool
}

// Suite is the conformance test of one action type (Code-ADR-0013,
// point 9).
type Suite struct {
	Descriptor action.Descriptor
	// Examples are documents of the type; at least one is valid.
	Examples []Example
	// Update writes the golden files of the current version before they
	// are compared. Tests set it from STREAMCREW_UPDATE_GOLDEN
	// (Code-ADR-0006).
	Update bool
}

// Run runs the conformance test:
//
//   - The schema passes the meta-schema of draft 2020-12; "x-ui" is a
//     vocabulary of its own, so an unknown hint fails.
//   - The schema accepts an example exactly when d decodes and validates
//     it. At least one example is valid.
//   - A valid example encodes, decodes and encodes to the same bytes, and
//     its encoding passes the schema.
//   - The members of the encoded documents and the properties of the
//     schema match: every member is a property, and every property occurs
//     in the encoding of a valid example or of a new action.
//   - The golden files of all versions hold (Golden).
func (s Suite) Run(t *testing.T) {
	t.Helper()
	d, examples := s.Descriptor, s.Examples
	reg, err := action.NewRegistry(capability.Set{}, d)
	require.NoError(t, err, "the registry takes the descriptor")
	d, _ = reg.Descriptor(d.Type)
	docs := registry(t, reg)
	validator, err := Compile(d.Type, d.Schema)
	require.NoError(t, err, "the schema passes the meta-schema")

	seen := map[string]bool{polydoc.KeyType: true, polydoc.KeySchemaVersion: true}
	newDoc, err := docs.Encode(d.New())
	require.NoError(t, err)
	for name := range members(t, newDoc) {
		seen[name] = true
	}

	valid := 0
	for _, ex := range examples {
		t.Run(ex.Name, func(t *testing.T) {
			schemaErr := validate(validator, []byte(ex.Doc))
			a, typeErr := docs.Decode([]byte(ex.Doc))
			if typeErr == nil {
				typeErr = command.ValidateActions([]command.Action{a})
			}
			assert.Equal(t, ex.Valid, schemaErr == nil, "schema: %v", schemaErr)
			assert.Equal(t, ex.Valid, typeErr == nil, "type: %v", typeErr)
			if !ex.Valid || typeErr != nil {
				return
			}
			valid++

			out, err := docs.Encode(a)
			require.NoError(t, err)
			again, err := docs.Decode(out)
			require.NoError(t, err)
			out2, err := docs.Encode(again)
			require.NoError(t, err)
			assert.Equal(t, string(out), string(out2), "encoding is stable")
			require.NoError(t, validate(validator, out), "the schema accepts the encoding")
			for name := range members(t, out) {
				_, ok := d.Schema.Properties.Lookup(name)
				assert.True(t, ok, "member %q is a property of the schema", name)
				seen[name] = true
			}
		})
	}
	require.Positive(t, valid, "at least one valid example")
	for _, name := range d.Schema.Properties.Names() {
		assert.True(t, seen[name], "property %q occurs in no example and not in a new action", name)
	}
	s.Golden(t)
}

// Golden checks the stored form of each version of the type
// (Code-ADR-0010, point 8). testdata/<type>/v<version>.golden is the
// encoding of the first valid example in the current version. Each file of
// an older version, v<n>.golden, must decode and encode to
// v<n>.migrated.golden. With Update, Golden writes the files of the current
// version first; files of older versions are never written, they are what
// older cores stored.
func (s Suite) Golden(t *testing.T) {
	t.Helper()
	d, examples := s.Descriptor, s.Examples
	reg, err := action.NewRegistry(capability.Set{}, d)
	require.NoError(t, err)
	docs := registry(t, reg)
	require.NoError(t, os.MkdirAll("testdata", 0o750))
	root, err := os.OpenRoot("testdata")
	require.NoError(t, err)
	defer root.Close()
	dir := d.Type
	g := goldenFiles{root: root, update: s.Update}

	i := slices.IndexFunc(examples, func(ex Example) bool { return ex.Valid })
	require.GreaterOrEqual(t, i, 0, "a valid example for the golden file")
	a, err := docs.Decode([]byte(examples[i].Doc))
	require.NoError(t, err)
	current, err := docs.Encode(a)
	require.NoError(t, err)
	g.check(t, filepath.Join(dir, "v"+strconv.Itoa(d.Version)+".golden"), current)

	for v := 1; v < d.Version; v++ {
		stored, err := root.ReadFile(filepath.Join(dir, "v"+strconv.Itoa(v)+".golden"))
		require.NoError(t, err, "the stored form of version %d", v)
		old, err := docs.Decode(stored)
		require.NoError(t, err, "version %d decodes", v)
		migrated, err := docs.Encode(old)
		require.NoError(t, err)
		g.check(t, filepath.Join(dir, "v"+strconv.Itoa(v)+".migrated.golden"), migrated)
	}
}

// Compile compiles s, the schema of the action type typ, with the
// validator, as the conformance test does: the meta-schema of draft 2020-12
// must accept it, with "x-ui" as a vocabulary of known hints.
func Compile(typ string, s *schema.Schema) (*jsonschema.Schema, error) {
	data, err := s.JSON()
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	vocab, err := uiVocabulary()
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.RegisterVocabulary(vocab)
	c.AssertVocabs()
	url := "https://streamcrew.invalid/action/" + typ + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	compiled, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("schema of %q:\n%s\n%w", typ, data, err)
	}
	return compiled, nil
}

// uiVocabulary returns the vocabulary of the keyword "x-ui": its value must
// be a known hint.
func uiVocabulary() (*jsonschema.Vocabulary, error) {
	hints := make([]string, 0, len(schema.UIs()))
	for _, u := range schema.UIs() {
		hints = append(hints, strconv.Quote(string(u)))
	}
	const url = "https://streamcrew.invalid/vocab/ui"
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(
		`{"properties": {"x-ui": {"enum": [` + strings.Join(hints, ",") + `]}}}`))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	meta, err := c.Compile(url)
	if err != nil {
		return nil, err
	}
	return &jsonschema.Vocabulary{
		URL:    url,
		Schema: meta,
		Compile: func(*jsonschema.CompilerContext, map[string]any) (jsonschema.SchemaExt, error) {
			return nil, nil // a hint for editors, nothing to validate
		},
	}, nil
}

// registry returns the document registry of the action types of reg.
func registry(t *testing.T, reg *action.Registry) *polydoc.Registry[command.Action] {
	t.Helper()
	docs := polydoc.NewRegistry("action", func(u polydoc.Unknown) command.Action { return command.UnknownAction{Unknown: u} })
	for _, e := range reg.Entries() {
		require.NoError(t, docs.Register(e))
	}
	return docs
}

// validate validates the JSON document doc against s.
func validate(s *jsonschema.Schema, doc []byte) error {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return err
	}
	return s.Validate(v)
}

// members returns the members of the JSON object doc.
func members(t *testing.T, doc []byte) map[string]jsontext.Value {
	t.Helper()
	var m map[string]jsontext.Value
	require.NoError(t, json.Unmarshal(doc, &m))
	return m
}

// goldenFiles are the golden files under testdata.
type goldenFiles struct {
	root   *os.Root
	update bool
}

// check compares data, indented, with the file at path below testdata;
// with update it writes the file first (Code-ADR-0006).
func (g goldenFiles) check(t *testing.T, path string, data []byte) {
	t.Helper()
	pretty := jsontext.Value(slices.Clone(data))
	require.NoError(t, pretty.Indent(jsontext.WithIndent("  ")))
	pretty = append(pretty, '\n')
	if g.update {
		require.NoError(t, g.root.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, g.root.WriteFile(path, pretty, 0o600))
	}
	want, err := g.root.ReadFile(path)
	require.NoError(t, err, "create testdata/%s with STREAMCREW_UPDATE_GOLDEN=1", path)
	assert.Equal(t, string(want), string(pretty))
}
