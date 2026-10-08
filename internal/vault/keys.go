// SPDX-License-Identifier: MIT

package vault

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/zalando/go-keyring"
)

// keySize is the AES-256 key length in bytes.
const keySize = 32

// keyringService is the service name of the entries in the system keyring.
const keyringService = "streamcrew"

// KeyFileName is the key file in the data directory, used without a system
// keyring.
const KeyFileName = "secret.key"

// Where a key set comes from.
const (
	SourceEnv     = "environment"
	SourceKeyring = "keyring"
	SourceFile    = "file"
)

// ErrKeyNotFound is returned by a Keyring that has no entry.
var ErrKeyNotFound = errors.New("key not found")

// Keyring is the system keyring; SystemKeyring implements it. Tests use a
// fake, so that they never touch the keyring of the developer.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, value string) error
}

// SystemKeyring is the keyring of the operating system (Windows Credential
// Manager, macOS Keychain, Secret Service on Linux).
type SystemKeyring struct{}

// Get implements Keyring.
func (SystemKeyring) Get(service, user string) (string, error) {
	v, err := keyring.Get(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrKeyNotFound
	}
	return v, err
}

// Set implements Keyring.
func (SystemKeyring) Set(service, user, value string) error {
	return keyring.Set(service, user, value)
}

// KeySet holds the current key and older keys still needed to decrypt.
type KeySet struct {
	Current string
	keys    map[string][]byte
	source  string
}

// Source says where the key set came from: SourceEnv, SourceKeyring or
// SourceFile.
func (ks *KeySet) Source() string {
	return ks.source
}

// IDs returns the IDs of all keys in the set, sorted.
func (ks *KeySet) IDs() []string {
	return slices.Sorted(maps.Keys(ks.keys))
}

func (ks *KeySet) key(id string) ([]byte, bool) {
	k, ok := ks.keys[id]
	return k, ok
}

func newKey() (id string, key []byte) {
	key = make([]byte, keySize)
	_, _ = rand.Read(key) // crypto/rand.Read never fails (Go 1.24+)
	return keyID(key), key
}

// keyID names a key by a short hash, so that the ID reveals nothing.
func keyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}

// keySetJSON is the stored form of a key set in the keyring and the key file.
type keySetJSON struct {
	Current string            `json:"current"`
	Keys    map[string]string `json:"keys"`
}

func (ks *KeySet) marshal() ([]byte, error) {
	out := keySetJSON{Current: ks.Current, Keys: map[string]string{}}
	for id, k := range ks.keys {
		out.Keys[id] = base64.StdEncoding.EncodeToString(k)
	}
	return json.Marshal(out)
}

func parseKeySet(data []byte, source string) (*KeySet, error) {
	var in keySetJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("key set from %s: %w", source, err)
	}
	ks := &KeySet{Current: in.Current, keys: map[string][]byte{}, source: source}
	for id, enc := range in.Keys {
		k, err := base64.StdEncoding.DecodeString(enc)
		if err != nil || len(k) != keySize || keyID(k) != id {
			return nil, fmt.Errorf("key set from %s: invalid key %q", source, id)
		}
		ks.keys[id] = k
	}
	if _, ok := ks.keys[ks.Current]; !ok {
		return nil, fmt.Errorf("key set from %s: current key %q missing", source, ks.Current)
	}
	return ks, nil
}

// Keys finds, creates and saves the key set of a data directory (ADR-0012):
// the environment variable first, then the system keyring, then the key file.
type Keys struct {
	envKey  string
	keyring Keyring
	file    string
	user    string
	logger  *slog.Logger
}

// NewKeys returns the key management for dataDir. envKey is the value of
// STREAMCREW_SECRET_KEY (base64), empty if unset; kr is the system keyring,
// nil to skip it.
func NewKeys(dataDir, envKey string, kr Keyring, logger *slog.Logger) *Keys {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		abs = dataDir
	}
	return &Keys{
		envKey:  envKey,
		keyring: kr,
		file:    filepath.Join(dataDir, KeyFileName),
		user:    "key:" + abs,
		logger:  logger,
	}
}

// Load returns the key set, creating a key on first use in the keyring or,
// without a usable keyring, in the key file.
func (k *Keys) Load(ctx context.Context) (*KeySet, error) {
	if k.envKey != "" {
		key, err := base64.StdEncoding.DecodeString(k.envKey)
		if err != nil || len(key) != keySize {
			return nil, errors.New("STREAMCREW_SECRET_KEY must be 32 bytes in base64")
		}
		id := keyID(key)
		return &KeySet{Current: id, keys: map[string][]byte{id: key}, source: SourceEnv}, nil
	}

	keyringUsable := false
	if k.keyring != nil {
		v, err := k.keyring.Get(keyringService, k.user)
		switch {
		case err == nil:
			return parseKeySet([]byte(v), SourceKeyring)
		case errors.Is(err, ErrKeyNotFound):
			keyringUsable = true
		default:
			k.logger.WarnContext(ctx, "system keyring not available, using the key file", "error", err)
		}
	}

	data, err := os.ReadFile(k.file)
	if err == nil {
		k.warnFile(ctx)
		return parseKeySet(data, SourceFile)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read key file: %w", err)
	}

	id, key := newKey()
	ks := &KeySet{Current: id, keys: map[string][]byte{id: key}}
	if keyringUsable {
		ks.source = SourceKeyring
		err := k.Save(ctx, ks)
		if err == nil {
			k.logger.InfoContext(ctx, "vault key created in the system keyring")
			return ks, nil
		}
		k.logger.WarnContext(ctx, "storing the vault key in the system keyring failed, using the key file", "error", err)
	}
	ks.source = SourceFile
	if err := k.Save(ctx, ks); err != nil {
		return nil, err
	}
	k.warnFile(ctx)
	return ks, nil
}

// Save stores the key set where it came from.
func (k *Keys) Save(_ context.Context, ks *KeySet) error {
	data, err := ks.marshal()
	if err != nil {
		return err
	}
	switch ks.source {
	case SourceKeyring:
		return k.keyring.Set(keyringService, k.user, string(data))
	case SourceFile:
		return writePrivateFile(k.file, data)
	default:
		return errors.New("the key from STREAMCREW_SECRET_KEY cannot be changed by streamcrew")
	}
}

func (k *Keys) warnFile(ctx context.Context) {
	k.logger.WarnContext(ctx, "vault key stored in a file next to the data; it protects backups and copies only without that file",
		"path", k.file)
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-key-*")
	if err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	_, werr := f.Write(data)
	err = errors.Join(werr, f.Chmod(0o600), f.Sync(), f.Close())
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("write key file: %w", err)
	}
	return nil
}
