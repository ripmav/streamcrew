// SPDX-License-Identifier: MIT

package polydoc_test

import (
	json "encoding/json/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/ripmav/streamcrew/internal/polydoc"
)

func TestDurationJSONAndYAML(t *testing.T) {
	t.Parallel()
	type doc struct {
		Cooldown polydoc.Duration `json:"cooldown" yaml:"cooldown"`
	}
	in := doc{Cooldown: polydoc.Duration(90 * time.Second)}

	data, err := json.Marshal(in)
	require.NoError(t, err)
	assert.JSONEq(t, `{"cooldown":"1m30s"}`, string(data))
	var back doc
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, 90*time.Second, back.Cooldown.Std())

	var fromYAML doc
	require.NoError(t, yaml.Unmarshal([]byte("cooldown: 30s\n"), &fromYAML))
	assert.Equal(t, 30*time.Second, fromYAML.Cooldown.Std())

	require.Error(t, json.Unmarshal([]byte(`{"cooldown":"30"}`), &back), "a unit is required")
	require.Error(t, json.Unmarshal([]byte(`{"cooldown":30}`), &back), "numbers are not durations")
}
