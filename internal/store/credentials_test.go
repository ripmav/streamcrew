// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/vault"
)

// TestRewriteSecretsIsOneTransaction covers the repository side of the key
// rotation (review of PR #23): fn sees all records, its result is stored,
// and an error from fn changes nothing.
func TestRewriteSecretsIsOneTransaction(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, name := range []string{"a", "b"} {
		require.NoError(t, s.PutSecret(ctx, vault.Record{Name: name, KeyID: "k1", Nonce: []byte{1}, Ciphertext: []byte(name), UpdatedAt: now}))
	}

	err := s.RewriteSecrets(ctx, func(recs []vault.Record) ([]vault.Record, error) {
		assert.Len(t, recs, 2)
		for i := range recs {
			recs[i].KeyID = "k2"
		}
		return recs, nil
	})
	require.NoError(t, err)
	recs, err := s.ListSecrets(ctx)
	require.NoError(t, err)
	for _, rec := range recs {
		assert.Equal(t, "k2", rec.KeyID, rec.Name)
	}

	err = s.RewriteSecrets(ctx, func(recs []vault.Record) ([]vault.Record, error) {
		for i := range recs {
			recs[i].KeyID = "k3"
		}
		return nil, assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)
	recs, err = s.ListSecrets(ctx)
	require.NoError(t, err)
	for _, rec := range recs {
		assert.Equal(t, "k2", rec.KeyID, "unchanged after the error")
	}
}
