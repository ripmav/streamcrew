// SPDX-License-Identifier: MIT

package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/template"
)

// TestRender_Encoding_B30_B31_B75 encodes special characters in values but
// never in the text of the template.
func TestRender_Encoding_B30_B31_B75(t *testing.T) {
	t.Parallel()
	const value = "a b+c&d=%26/?#<e>\"f'g\\h\n~._-ü😀 \x00"
	const text = `<p a="1">q=$value&x=1 "$" 'ü'</p>`
	s := scope()
	s.SetValue("value", template.TextValue(value))
	s.SetValue("broken", template.TextValue("a\xffb"))

	tests := []struct {
		enc  template.Encoding
		name string
		want string
	}{
		{template.Text, "text", `<p a="1">q=` + value + `&x=1 "$" 'ü'</p>`},
		{template.URL, "url", `<p a="1">q=a%20b%2Bc%26d%3D%2526%2F%3F%23%3Ce%3E%22f%27g%5Ch%0A~._-%C3%BC%F0%9F%98%80%E2%80%A8%00&x=1 "$" 'ü'</p>`},
		{template.HTML, "html", `<p a="1">q=a b+c&amp;d=%26/?#&lt;e&gt;&#34;f&#39;g\h` + "\n" + `~._-ü😀` + " \x00" + `&x=1 "$" 'ü'</p>`},
		{template.JSON, "json", `<p a="1">q=a b+c&d=%26/?#<e>\"f'g\\h\n~._-ü😀` + " " + `\u0000&x=1 "$" 'ü'</p>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.name, tc.enc.String())
			out, err := template.New(nil).Render(t.Context(), template.Parse(text), &s, tc.enc)
			require.NoError(t, err)
			assert.Equal(t, tc.want, out)
		})
	}

	out, err := template.New(nil).Render(t.Context(), template.Parse(`"$broken"`), &s, template.JSON)
	require.NoError(t, err)
	assert.Equal(t, "\"a�b\"", out, "invalid UTF-8 becomes U+FFFD in JSON")
}
