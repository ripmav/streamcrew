// SPDX-License-Identifier: MIT

// Package appcreds provides the built-in platform app credentials
// (ADR-0024): the client ID and secret of the project's confidential
// Twitch app, sealed into the binary with ML-KEM-768 + HKDF-SHA256 +
// AES-256-GCM (Go standard library only) and opened once per process at
// the first login. The plaintext never reaches logs or persistent
// storage; a missing embedded record simply leaves the built-in app
// disabled (the login falls back to the error of ADR-0023).
package appcreds

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"sync"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/logging"
)

// hkdfInfo is the HKDF context of the app record key (ADR-0024); a change
// breaks the sealed record, so it is versioned.
const hkdfInfo = "streamcrew/appcreds/v1"

// Record is the built-in app of one platform.
type Record struct {
	// ClientID is the client ID of the app.
	ClientID string
	// ClientSecret is the client secret of the app, redacted in logs.
	ClientSecret logging.Secret
}

// wireRecord is the JSON form of the sealed record; the secret is a
// plain string on the wire, because logging.Secret would redact itself
// in JSON (the seal is the protection, not the redaction).
type wireRecord struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// SealedRecord is the JSON bytes the generator seals and the runtime
// opens (ADR-0024); it pins the wire form shared by both.
func SealedRecord(clientID, clientSecret string) ([]byte, error) {
	return json.Marshal(wireRecord{ClientID: clientID, ClientSecret: clientSecret})
}

// embedded is the opened record; ok is false without one.
//
//nolint:gochecknoglobals // process-lifetime cache of the opened record (ADR-0024)
var embedded = struct {
	rec Record
	ok  bool
}{}

// openOnce keeps the opening attempt to once per process; the tests of
// this package replace it with their reset (appcreds_test.go).
//
//nolint:gochecknoglobals // once per process, as openOnce says
var openOnce sync.Once

// open runs the sealed opening once per process: ML-KEM-768 decapsulation
// of the embedded ciphertext, HKDF key derivation, and the AES-256-GCM
// open (ADR-0024). An empty payload (generator not run) or a corrupted
// record leaves the built-in app disabled.
func open() {
	openOnce.Do(func() {
		if embeddedPayloadHex == "" {
			return
		}
		priv, err := hex.DecodeString(embeddedMLKEMPrivateKeyHex)
		if err != nil {
			return
		}
		ct, err := hex.DecodeString(embeddedMLKEMCiphertextHex)
		if err != nil {
			return
		}
		nonce, err := hex.DecodeString(embeddedGCMNonceHex)
		if err != nil {
			return
		}
		payload, err := hex.DecodeString(embeddedPayloadHex)
		if err != nil {
			return
		}
		dk, err := mlkem.NewDecapsulationKey768(priv)
		if err != nil {
			return
		}
		ss, err := dk.Decapsulate(ct)
		if err != nil {
			return
		}
		data, err := Open(ss, nonce, payload)
		if err != nil {
			return
		}
		var w wireRecord
		if err := json.Unmarshal(data, &w); err != nil {
			return
		}
		embedded.rec, embedded.ok = Record{ClientID: w.ClientID, ClientSecret: logging.Secret(w.ClientSecret)}, true
	})
}

// Get returns the built-in record of the platform; ok is false for other
// platforms and while the embedded record is disabled (ADR-0024).
func Get(p platform.Name) (Record, bool) {
	if p != platform.Twitch {
		return Record{}, false
	}
	open()
	return embedded.rec, embedded.ok
}

// key derives the 32-byte AES-256-GCM key of the app record from the
// ML-KEM shared secret (ADR-0024).
func key(sharedSecret []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, sharedSecret, nil, hkdfInfo, 32)
}

// Seal seals record with AES-256-GCM under the ML-KEM shared secret and
// returns the nonce and the payload (ADR-0024).
func Seal(record []byte, sharedSecret []byte) (nonce, payload []byte, err error) {
	k, err := key(sharedSecret)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, record, nil), nil
}

// Open reverses Seal (ADR-0024); a tampered nonce or payload is an error.
func Open(sharedSecret, nonce, payload []byte) ([]byte, error) {
	k, err := key(sharedSecret)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, payload, nil)
}
