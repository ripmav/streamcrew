// SPDX-License-Identifier: MIT

// Package vault keeps tokens and other secrets encrypted at rest (ADR-0012):
// AES-256-GCM with a fresh nonce per value and the entry's name as
// additional authenticated data, so that ciphertexts cannot be swapped
// between entries. The key comes from STREAMCREW_SECRET_KEY, the system
// keyring or a key file (Keys).
package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/ripmav/streamcrew/internal/logging"
)

// ErrNotFound is returned for an entry that does not exist.
var ErrNotFound = errors.New("vault entry not found")

// Record is an encrypted entry as the repository stores it.
type Record struct {
	Name       string
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
	UpdatedAt  time.Time
}

// Repository stores records; *store.Store implements it.
type Repository interface {
	GetSecret(ctx context.Context, name string) (rec Record, found bool, err error)
	PutSecret(ctx context.Context, rec Record) error
	DeleteSecret(ctx context.Context, name string) (deleted bool, err error)
	// RewriteSecrets reads all records and stores the records fn returns,
	// in one write transaction, so that no other write comes in between.
	// An error from fn discards the transaction.
	RewriteSecrets(ctx context.Context, fn func([]Record) ([]Record, error)) error
}

// Vault encrypts and decrypts the secrets of a profile.
type Vault struct {
	repo Repository
	keys *KeySet
}

// New returns a vault that uses the current key of ks for new values.
func New(repo Repository, ks *KeySet) *Vault {
	return &Vault{repo: repo, keys: ks}
}

// Put encrypts and stores a secret.
func (v *Vault) Put(ctx context.Context, name string, value logging.Secret) error {
	rec, err := v.seal(name, []byte(value.Reveal()))
	if err != nil {
		return err
	}
	return v.repo.PutSecret(ctx, rec)
}

// Get decrypts a secret.
func (v *Vault) Get(ctx context.Context, name string) (logging.Secret, error) {
	rec, found, err := v.repo.GetSecret(ctx, name)
	if err != nil {
		return "", fmt.Errorf("vault entry %q: %w", name, err)
	}
	if !found {
		return "", fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	plain, err := v.open(rec)
	if err != nil {
		return "", err
	}
	return logging.Secret(plain), nil
}

// Delete removes a secret; deleting a missing one is not an error.
func (v *Vault) Delete(ctx context.Context, name string) error {
	_, err := v.repo.DeleteSecret(ctx, name)
	return err
}

// Rotate creates a new key, re-encrypts the entries of every repository with
// it and removes the old keys. The key belongs to the data directory, so
// repos must be the databases of all its profiles. The steps are ordered so
// that an interruption leaves every entry readable: the new key is saved
// before any entry uses it, and old keys are removed only after all entries
// have been re-encrypted. It returns the new key set.
//
// Each repository is read and re-encrypted in one write transaction
// (ADR-0012). The caller must still hold the data directory lock: a vault
// that keeps using the old key could otherwise store an entry after the
// re-encryption, and removing the old key would make it unreadable.
func Rotate(ctx context.Context, keys *Keys, current *KeySet, repos ...Repository) (*KeySet, error) {
	if current.Source() == SourceEnv {
		return nil, errors.New("the key from STREAMCREW_SECRET_KEY cannot be rotated by streamcrew; set a new value and sign in again")
	}
	id, key := newKey()
	next := &KeySet{Current: id, keys: maps.Clone(current.keys), source: current.source}
	next.keys[id] = key
	if err := keys.Save(ctx, next); err != nil {
		return nil, fmt.Errorf("rotate: save new key: %w", err)
	}

	oldVault := &Vault{keys: current}
	newVault := &Vault{keys: next}
	reencrypt := func(recs []Record) ([]Record, error) {
		out := make([]Record, 0, len(recs))
		for _, rec := range recs {
			plain, err := oldVault.open(rec)
			if err != nil {
				return nil, err
			}
			r, err := newVault.seal(rec.Name, plain)
			clear(plain)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, nil
	}
	for _, repo := range repos {
		if err := repo.RewriteSecrets(ctx, reencrypt); err != nil {
			return nil, fmt.Errorf("rotate: %w", err)
		}
	}

	final := &KeySet{Current: id, keys: map[string][]byte{id: key}, source: next.source}
	if err := keys.Save(ctx, final); err != nil {
		return nil, fmt.Errorf("rotate: remove old keys: %w", err)
	}
	return final, nil
}

func (v *Vault) seal(name string, plain []byte) (Record, error) {
	key, ok := v.keys.key(v.keys.Current)
	if !ok {
		return Record{}, errors.New("no current key")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return Record{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce) // crypto/rand.Read never fails (Go 1.24+)
	return Record{
		Name:       name,
		KeyID:      v.keys.Current,
		Nonce:      nonce,
		Ciphertext: aead.Seal(nil, nonce, plain, []byte(name)),
		UpdatedAt:  time.Now().UTC(),
	}, nil
}

func (v *Vault) open(rec Record) ([]byte, error) {
	key, ok := v.keys.key(rec.KeyID)
	if !ok {
		return nil, fmt.Errorf("vault entry %q: key %q is not available", rec.Name, rec.KeyID)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, rec.Nonce, rec.Ciphertext, []byte(rec.Name))
	if err != nil {
		return nil, fmt.Errorf("vault entry %q: decryption failed", rec.Name)
	}
	return plain, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
