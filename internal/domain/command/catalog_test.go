// SPDX-License-Identifier: MIT

package command_test

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"strconv"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// requirementExample is the members of a requirement document without
// "type" and "schemaVersion", and whether the type accepts them.
type requirementExample struct {
	doc   string
	valid bool
}

// someID is an ID for the references of the examples.
const someID = `"0190a8f4-1c2d-7e3f-8a9b-0c1d2e3f4a5b"`

// requirementExamples are the examples of the requirement types; a pair
// the schema and the Go code would judge differently, e.g. a duration of
// "0s", has no place here (Code-ADR-0013, point 9).
func requirementExamples() map[string][]requirementExample {
	return map[string][]requirementExample{
		command.TypeRole: {
			{`{"role":"follower"}`, true},
			{`{"role":"king"}`, false},
			{`{}`, false},
			{`{"role":"follower","extra":1}`, false},
		},
		command.TypeCooldown: {
			{`{"scope":"standard","duration":"30s"}`, true},
			{`{"scope":"per_user","duration":"1m30s"}`, true},
			{`{"scope":"group","group":` + someID + `}`, true},
			{`{"scope":"per_user_group","group":` + someID + `}`, true},
			{`{"scope":"standard"}`, false},
			{`{"scope":"standard","duration":"30 seconds"}`, false},
			{`{"scope":"standard","duration":"30s","group":` + someID + `}`, false},
			{`{"scope":"group","group":` + someID + `,"duration":"30s"}`, false},
			{`{"scope":"group"}`, false},
			{`{"scope":"group","group":"cooldowns"}`, false},
			{`{"scope":"weekly","duration":"30s"}`, false},
		},
		command.TypeArguments: {
			{`{"arguments":[{"name":"user","type":"user","required":true,"identifier":"target"},{"name":"amount","type":"integer"}]}`, true},
			{`{"arguments":[]}`, false},
			{`{"arguments":[{"type":"text"}]}`, false},
			{`{"arguments":[{"name":"","type":"text"}]}`, false},
			{`{"arguments":[{"name":"when"}]}`, false},
			{`{"arguments":[{"name":"when","type":"date"}]}`, false},
			{`{"arguments":[{"name":"user","type":"user","identifier":"Target"}]}`, false},
		},
		command.TypeRank: {
			{`{"rank":` + someID + `,"match":"at_least"}`, true},
			{`{"rank":` + someID + `,"match":"above"}`, false},
			{`{"match":"exactly"}`, false},
		},
		command.TypeCurrency: {
			{`{"currency":` + someID + `,"mode":"required","amount":10}`, true},
			{`{"currency":` + someID + `,"mode":"range","amount":1,"maximum":100}`, true},
			{`{"currency":` + someID + `,"mode":"minimum","amount":0}`, true},
			{`{"currency":` + someID + `,"mode":"required","amount":-1}`, false},
			{`{"currency":` + someID + `,"mode":"required","amount":1.5}`, false},
			{`{"currency":` + someID + `,"mode":"minimum","amount":5,"maximum":9}`, false},
			{`{"currency":` + someID + `,"mode":"range","amount":5}`, false},
			{`{"mode":"required","amount":10}`, false},
		},
		command.TypeInventory: {
			{`{"item":` + someID + `,"amount":2}`, true},
			{`{"item":` + someID + `,"amount":0}`, false},
		},
		command.TypeThreshold: {
			{`{"users":3,"within":"1m","runForEachUser":true}`, true},
			{`{"users":0,"within":"1m"}`, false},
			{`{"users":2}`, false},
		},
		command.TypeSettings: {
			{`{"deleteTriggerMessage":true,"showInChatMenu":true}`, true},
			{`{}`, true},
			{`{"showInChatMenu":"yes"}`, false},
		},
	}
}

// TestRequirementCatalog is the conformance test of the requirement types
// (Code-ADR-0013, point 9): the catalog has each type of the codec in its
// current version, its schema passes the meta-schema and accepts an
// example exactly when the codec decodes it and the requirement validates,
// and the members of the encoding are the properties of the schema.
func TestRequirementCatalog(t *testing.T) {
	t.Parallel()
	codec, err := command.NewCodec()
	require.NoError(t, err)
	catalog := command.RequirementCatalog()
	types := make([]string, len(catalog))
	for i, d := range catalog {
		types[i] = d.Type
	}
	assert.ElementsMatch(t, codec.RequirementTypes(), types)
	assert.Equal(t, command.TypeSettings, types[len(types)-1], "the settings come last")
	examples := requirementExamples()

	for _, d := range catalog {
		t.Run(d.Type, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, d.Schema.Validate())
			validator, err := actiontest.Compile("requirement-"+d.Type, d.Schema)
			require.NoError(t, err, "the schema passes the meta-schema")
			seen := map[string]bool{}
			valid := 0
			for _, ex := range examples[d.Type] {
				schemaErr := validateJSON(validator, ex.doc)
				r, typeErr := decodeRequirement(codec, d.Type, ex.doc)
				if typeErr == nil {
					typeErr = r.Validate()
				}
				assert.Equal(t, ex.valid, schemaErr == nil, "%s: schema: %v", ex.doc, schemaErr)
				assert.Equal(t, ex.valid, typeErr == nil, "%s: type: %v", ex.doc, typeErr)
				if !ex.valid || typeErr != nil {
					continue
				}
				valid++
				members := encodeRequirement(t, codec, r)
				assert.Equal(t, `"`+d.Type+`"`, string(members[polydoc.KeyType]))
				assert.Equal(t, strconv.Itoa(d.Version), string(members[polydoc.KeySchemaVersion]), "the codec writes the version of the catalog")
				delete(members, polydoc.KeyType)
				delete(members, polydoc.KeySchemaVersion)
				out, err := json.Marshal(members, json.Deterministic(true))
				require.NoError(t, err)
				require.NoError(t, validateJSON(validator, string(out)), "the schema accepts the encoding %s", out)
				for name := range members {
					_, ok := d.Schema.Properties.Lookup(name)
					assert.True(t, ok, "member %q is a property of the schema", name)
					seen[name] = true
				}
			}
			require.Positive(t, valid, "at least one valid example")
			for _, name := range d.Schema.Properties.Names() {
				assert.True(t, seen[name], "property %q occurs in no valid example", name)
			}
		})
	}
}

// decodeRequirement decodes the members doc as a requirement of type typ,
// stored without a version, so in the current one.
func decodeRequirement(codec *command.Codec, typ, doc string) (command.Requirement, error) {
	var members map[string]jsontext.Value
	if err := json.Unmarshal([]byte(doc), &members); err != nil {
		return nil, err
	}
	members[polydoc.KeyType] = jsontext.Value(`"` + typ + `"`)
	stored, err := json.Marshal([]map[string]jsontext.Value{members})
	if err != nil {
		return nil, err
	}
	cmd, err := codec.Command(command.Record{Requirements: stored})
	if err != nil {
		return nil, err
	}
	if _, unknown := cmd.Requirements[0].(command.UnknownRequirement); unknown {
		return nil, errors.New("unknown requirement type")
	}
	return cmd.Requirements[0], nil
}

// encodeRequirement returns the members of the stored form of r.
func encodeRequirement(t *testing.T, codec *command.Codec, r command.Requirement) map[string]jsontext.Value {
	t.Helper()
	rec, err := codec.Record(command.Command{Requirements: []command.Requirement{r}})
	require.NoError(t, err)
	var docs []map[string]jsontext.Value
	require.NoError(t, json.Unmarshal(rec.Requirements, &docs))
	require.Len(t, docs, 1)
	return docs[0]
}

// validateJSON validates the JSON document doc against s.
func validateJSON(s *jsonschema.Schema, doc string) error {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(doc)))
	if err != nil {
		return err
	}
	return s.Validate(v)
}
