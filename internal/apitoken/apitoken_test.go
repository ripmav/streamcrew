// SPDX-License-Identifier: MIT

package apitoken

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreRoundtrip covers creation, validation, listing and revocation of
// tokens and the 0600 mode of the token file.
func TestStoreRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := Open(dir)

	enabled, err := s.Enabled()
	require.NoError(t, err)
	assert.False(t, enabled, "a new store has no tokens")

	tok, value, err := s.Create("tui", []Scope{Admin})
	require.NoError(t, err)
	require.NotEmpty(t, tok.ID)
	assert.Equal(t, "tui", tok.Name)
	assert.Equal(t, []Scope{Admin}, tok.Scopes)
	assert.NotEmpty(t, tok.SHA256)
	assert.NotContains(t, tok.SHA256, value, "the hash must not reveal the value")
	assert.NotEqual(t, "", value)

	mode, err := os.Stat(filepath.Join(dir, FileName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), mode.Mode().Perm(), "the token file is private")

	_, ok := s.Validate(value)
	require.True(t, ok, "the created token validates")
	// the flipped last character must differ from the original one, or
	// the "wrong" token is the right one (hex digits are 0-9a-f).
	flip := '0'
	if value[len(value)-1] == '0' {
		flip = '1'
	}
	found, ok := s.Validate(value[:len(value)-1] + string(flip))
	require.False(t, ok)
	_ = found

	other, _, err := s.Create("overlay", []Scope{Overlay, Read})
	require.NoError(t, err)
	assert.Equal(t, "overlay", other.Name)

	tokens, err := s.List()
	require.NoError(t, err)
	require.Len(t, tokens, 2)
	assert.Equal(t, []Scope{Read, Overlay}, tokens[1].Scopes, "scopes are de-duplicated in a stable order")

	enabled, err = s.Enabled()
	require.NoError(t, err)
	assert.True(t, enabled)

	ok, err = s.Revoke("overlay")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.Revoke("overlay")
	require.NoError(t, err)
	require.False(t, ok, "a revoked token is gone")
}

// TestEnabledFailsClosed covers the fail-closed contract: a corrupt token
// file is an error, not an empty store (code review 2026-10-24, S1/V1).
func TestEnabledFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)

	enabled, err := s.Enabled()
	require.NoError(t, err)
	assert.False(t, enabled)

	require.NoError(t, os.WriteFile(filepath.Join(dir, FileName), []byte("{corrupt"), 0o600))
	enabled, err = s.Enabled()
	require.Error(t, err)
	assert.False(t, enabled)
}

// TestCreateWith covers the bootstrap token from a known value
// (STREAMCREW_TOKEN, code review 2026-10-24, S2).
func TestCreateWith(t *testing.T) {
	t.Parallel()
	s := Open(t.TempDir())

	_, err := s.CreateWith("bootstrap", []Scope{Admin}, "")
	require.Error(t, err)

	tok, err := s.CreateWith("bootstrap", []Scope{Admin}, "sc_fixed-value")
	require.NoError(t, err)
	assert.Equal(t, "bootstrap", tok.Name)

	_, ok := s.Validate("sc_fixed-value")
	require.True(t, ok, "the fixed value validates")

	enabled, err := s.Enabled()
	require.NoError(t, err)
	assert.True(t, enabled)
}

// TestCreateValidatesChecks the input validation of Create.
func TestCreateValidates(t *testing.T) {
	t.Parallel()
	s := Open(t.TempDir())

	_, _, err := s.Create("", []Scope{Read})
	require.Error(t, err)
	_, _, err = s.Create("x", nil)
	require.Error(t, err)
	_, _, err = s.Create("x", []Scope{"bogus"})
	require.ErrorIs(t, err, ErrInvalidScope)
}

// TestSatisfies covers the scope hierarchy.
func TestSatisfies(t *testing.T) {
	t.Parallel()
	admin := Token{Scopes: []Scope{Admin}}
	control := Token{Scopes: []Scope{Control}}
	read := Token{Scopes: []Scope{Read}}
	overlay := Token{Scopes: []Scope{Overlay}}
	none := Token{Scopes: []Scope{}}

	assert.True(t, admin.Satisfies(Read))
	assert.True(t, admin.Satisfies(Control))
	assert.True(t, admin.Satisfies(Admin))
	assert.True(t, control.Satisfies(Read))
	assert.False(t, control.Satisfies(Admin))
	assert.True(t, read.Satisfies(Read))
	assert.False(t, read.Satisfies(Control))
	assert.True(t, overlay.Satisfies(Read), "overlay counts as read")
	assert.False(t, overlay.Satisfies(Control))
	assert.False(t, none.Satisfies(Read))
}
