// SPDX-License-Identifier: MIT

package appcreds

import (
	"crypto/mlkem"
	"encoding/hex"
	"encoding/json/v2"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/logging"
)

// TestSealOpenRoundTrip checks the AES-256-GCM seal under the ML-KEM
// shared secret (ADR-0024).
func TestSealOpenRoundTrip(t *testing.T) {
	ss := []byte("the shared secret of the run")
	in := []byte(`{"ClientID":"abc","ClientSecret":"s3cr3t"}`)
	nonce, ct, err := Seal(in, ss)
	require.NoError(t, err)
	out, err := Open(ss, nonce, ct)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

// TestOpenTampered checks that a tampered payload or a wrong secret is an
// error (the built-in app stays disabled, nothing is half-opened).
func TestOpenTampered(t *testing.T) {
	ss := []byte("the shared secret of the run")
	nonce, ct, err := Seal([]byte("record"), ss)
	require.NoError(t, err)
	ct[0] ^= 1
	_, err = Open(ss, nonce, ct)
	assert.Error(t, err)
	nonce2, ct2, err := Seal([]byte("record"), ss)
	require.NoError(t, err)
	_, err = Open([]byte("another shared secret of the run"), nonce2, ct2)
	assert.Error(t, err)
}

// TestGetDisabled checks that without a generated embedded.go the
// built-in app is off and Get reports it (the login falls back to the
// error of ADR-0023).
func TestGetDisabled(t *testing.T) {
	resetEmbedded()
	embeddedMLKEMPrivateKeyHex = ""
	embeddedMLKEMCiphertextHex = ""
	embeddedGCMNonceHex = ""
	embeddedPayloadHex = ""
	_, ok := Get(platform.Twitch)
	assert.False(t, ok)
	_, ok = Get(platform.Name("other"))
	assert.False(t, ok)
}

// TestGetEmbedded walks the full opening path with a sealed record like
// the generator writes it: ML-KEM-768 decapsulation, HKDF, GCM open,
// JSON record.
func TestGetEmbedded(t *testing.T) {
	want := Record{ClientID: "client-id-1", ClientSecret: logging.Secret("the app secret")}
	dk, err := mlkem.GenerateKey768()
	require.NoError(t, err)
	ss, ct := dk.EncapsulationKey().Encapsulate()
	record, err := SealedRecord(want.ClientID, string(want.ClientSecret))
	require.NoError(t, err)
	nonce, payload, err := Seal(record, ss)
	require.NoError(t, err)

	resetEmbedded()
	embeddedMLKEMPrivateKeyHex = hex.EncodeToString(dk.Bytes())
	embeddedMLKEMCiphertextHex = hex.EncodeToString(ct)
	embeddedGCMNonceHex = hex.EncodeToString(nonce)
	embeddedPayloadHex = hex.EncodeToString(payload)
	got, ok := Get(platform.Twitch)
	require.True(t, ok)
	assert.Equal(t, want, got)
	// The opening is once per process: a second call returns the cache.
	again, ok := Get(platform.Twitch)
	require.True(t, ok)
	assert.Equal(t, want, again)
}

// TestGetCorrupted checks that a corrupted embedded record leaves the
// built-in app disabled instead of opening a wrong record.
func TestGetCorrupted(t *testing.T) {
	dk, err := mlkem.GenerateKey768()
	require.NoError(t, err)
	ss, ct := dk.EncapsulationKey().Encapsulate()
	nonce, payload, err := Seal([]byte(`{"ClientID":"x"}`), ss)
	require.NoError(t, err)
	payload[0] ^= 1

	resetEmbedded()
	embeddedMLKEMPrivateKeyHex = hex.EncodeToString(dk.Bytes())
	embeddedMLKEMCiphertextHex = hex.EncodeToString(ct)
	embeddedGCMNonceHex = hex.EncodeToString(nonce)
	embeddedPayloadHex = hex.EncodeToString(payload)
	_, ok := Get(platform.Twitch)
	assert.False(t, ok)
}

// TestRecordJSONShape pins the field names of the sealed wire record:
// the generator and the runtime share them through SealedRecord, and a
// rename would silently break every generated embedded.go.
func TestRecordJSONShape(t *testing.T) {
	b, err := SealedRecord("id", "s")
	require.NoError(t, err)
	assert.Equal(t, `{"client_id":"id","client_secret":"s"}`, string(b))
	var w wireRecord
	require.NoError(t, json.Unmarshal(b, &w))
	assert.Equal(t, "s", w.ClientSecret) // plain on the wire, not redacted
}

// resetEmbedded restores the pristine (disabled) state between tests,
// including the once of the opening.
func resetEmbedded() {
	embedded = struct {
		rec Record
		ok  bool
	}{}
	openOnce = sync.Once{}
}
