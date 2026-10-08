// SPDX-License-Identifier: MIT

package schema_test

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action/schema"
)

// TestEmptyValues checks that a default or const that is an empty text,
// list or object stays in the schema: it is a value, not a missing keyword.
func TestEmptyValues(t *testing.T) {
	t.Parallel()
	for _, v := range []string{`""`, `[]`, `{}`} {
		s := &schema.Schema{Default: jsontext.Value(v), Const: jsontext.Value(v)}
		data, err := s.JSON()
		require.NoError(t, err)
		assert.JSONEq(t, `{"const":`+v+`,"default":`+v+`}`, string(data))
	}
}

// TestDefs checks that the definitions keep their order and that a
// reference names one.
func TestDefs(t *testing.T) {
	t.Parallel()
	s := &schema.Schema{Ref: "#/$defs/b", Defs: schema.Defs{
		{Name: "b", Schema: schema.Switch()},
		{Name: "a", Schema: schema.DefRef("b")},
	}}
	data, err := s.JSON()
	require.NoError(t, err)
	assert.Equal(t, `{"$ref":"#/$defs/b","$defs":{"b":{"type":"boolean","x-ui":"switch"},"a":{"$ref":"#/$defs/b"}}}`, string(data))

	c := s.Clone()
	c.Defs[0].Schema.UI = schema.UIText
	assert.Equal(t, schema.UISwitch, s.Defs[0].Schema.UI, "the clone has its own definitions")
	require.NoError(t, s.Validate())
	require.Error(t, (&schema.Schema{Defs: schema.Defs{{Name: "x"}}}).Validate(), "a definition without a schema")
	bad := &schema.Schema{Defs: schema.Defs{{Name: "x", Schema: &schema.Schema{UI: "nonsense"}}}}
	require.Error(t, bad.Validate(), "a definition with an unknown hint")
}
