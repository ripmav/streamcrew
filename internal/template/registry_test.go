// SPDX-License-Identifier: Apache-2.0

package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/template"
)

func TestNewRegistry_Rejects(t *testing.T) {
	t.Parallel()
	ok := constant("x")
	match := func(string) (int, template.Resolver) { return 0, nil }
	tests := []struct {
		name     string
		families []template.Family
	}{
		{"empty name", []template.Family{{Name: "f", Identifiers: []template.Identifier{{Resolve: ok}}}}},
		{"upper case", []template.Family{{Name: "f", Identifiers: []template.Identifier{{Name: "UserName", Resolve: ok}}}}},
		{"colon", []template.Family{{Name: "f", Identifiers: []template.Identifier{{Name: "a:b", Resolve: ok}}}}},
		{"no resolver", []template.Family{{Name: "f", Identifiers: []template.Identifier{{Name: "a"}}}}},
		{"duplicate", []template.Family{
			{Name: "f", Identifiers: []template.Identifier{{Name: "a", Resolve: ok}}},
			{Name: "g", Identifiers: []template.Identifier{{Name: "a", Resolve: ok}}},
		}},
		{"pattern without match", []template.Family{{Name: "f", Patterns: []template.Pattern{{Name: "p", Prefixes: []string{"p"}}}}}},
		{"pattern without prefix", []template.Family{{Name: "f", Patterns: []template.Pattern{{Name: "p", Match: match}}}}},
		{"invalid prefix", []template.Family{{Name: "f", Patterns: []template.Pattern{{Name: "p", Prefixes: []string{"p q"}, Match: match}}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := template.NewRegistry(tc.families...)
			require.ErrorIs(t, err, template.ErrInvalidRegistry)
		})
	}
}

// TestRegistry_Reserved_B12_B74 checks custom names against the built-in
// identifiers.
func TestRegistry_Reserved_B12_B74(t *testing.T) {
	t.Parallel()
	r := newRegistry(t)
	tests := []struct {
		name string
		with string // empty if the name is free
	}{
		{"deaths", ""},
		{"wins", ""},
		{"users", ""},
		{"ti", "time"},              // the beginning of a built-in name
		{"user", "username"},        // B74
		{"User", "username"},        // case does not matter
		{"username", "username"},    // a built-in name
		{"usernames", "username"},   // starts with a built-in name
		{"timer", "time"},           // would hide $time in "$timer"
		{"arg", "arg<n>text"},       // the fixed beginning of a pattern
		{"ar", "arg<n>text"},        // the beginning of a pattern's prefix
		{"arguments", "arg<n>text"}, // starts with a pattern's prefix
		{"unicodes", "unicode<n>"},
		{"linebreaks", "linebreak"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			with, reserved := r.Reserved(tc.name)
			assert.Equal(t, tc.with != "", reserved)
			assert.Equal(t, tc.with, with)
		})
	}

	var empty template.Registry
	_, reserved := empty.Reserved("user")
	assert.False(t, reserved, "an empty registry reserves nothing")
}
