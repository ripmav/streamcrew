// SPDX-License-Identifier: MIT

package vault

import (
	"bytes"
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKeySetMarshalDeterministic: the key file has the same bytes for the
// same keys, with the keys sorted, and reading it is strict about duplicate
// names but ignores unknown fields (Code-ADR-0018).
func TestKeySetMarshalDeterministic(t *testing.T) {
	t.Parallel()
	ks := &KeySet{keys: map[string][]byte{}}
	for b := range byte(3) {
		k := bytes.Repeat([]byte{b + 1}, keySize)
		ks.keys[keyID(k)] = k
	}
	ids := ks.IDs()
	ks.Current = ids[1]
	var want strings.Builder
	want.WriteString(`{"current":"` + ks.Current + `","keys":{`)
	for i, id := range slices.Sorted(slices.Values(ids)) {
		if i > 0 {
			want.WriteByte(',')
		}
		want.WriteString(`"` + id + `":"` + base64.StdEncoding.EncodeToString(ks.keys[id]) + `"`)
	}
	want.WriteString(`}}`)
	for range 20 {
		data, err := ks.marshal()
		require.NoError(t, err)
		assert.Equal(t, want.String(), string(data))
	}

	newer := strings.Replace(want.String(), `{"current"`, `{"future":true,"current"`, 1)
	back, err := parseKeySet([]byte(newer), "test")
	require.NoError(t, err, "unknown fields are ignored")
	assert.Equal(t, ids, back.IDs())

	duplicate := strings.Replace(want.String(), `{"current"`, `{"current":"x","current"`, 1)
	_, err = parseKeySet([]byte(duplicate), "test")
	require.Error(t, err, "duplicate names are rejected")
}
