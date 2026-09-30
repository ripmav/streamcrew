// SPDX-License-Identifier: MIT

package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/template"
)

// TestCharacterFamily_B32_B33 covers $linebreak and $unicode<n>.
func TestCharacterFamily_B32_B33(t *testing.T) {
	t.Parallel()
	e := template.New(newRegistry(t))
	tests := []struct {
		text string
		want string
	}{
		{"a$linebreak", "a\n"},
		{"$LineBreak!", "\n!"},
		{"$unicode937", "Ω"},
		{"$unicode0937", "Ω"},
		{"$unicode128512", "😀"},
		{"$unicode1114111", "\U0010FFFF"},
		{"$unicode10", "\n"},
		{"$unicode36username", "$username"}, // B78
		{"$unicode65:x", "A:x"},
		// Control characters other than the line break, surrogates and
		// numbers beyond the last code point are no match (B6).
		{"$unicode0", "$unicode0"},
		{"$unicode9", "$unicode9"},
		{"$unicode127", "$unicode127"},
		{"$unicode55296", "$unicode55296"},
		{"$unicode1114112", "$unicode1114112"},
		{"$unicode00000000000065", "A"},
		{"$unicodeA", "$unicodeA"},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, render(t, e, tc.text, new(scope())))
		})
	}
}
