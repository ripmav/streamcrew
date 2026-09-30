// SPDX-License-Identifier: Apache-2.0

package template

import (
	"encoding/json/jsontext"
	"html"
	"net/url"
	"strings"
)

// Encoding says how inserted values are escaped for the place a text goes
// to (B30). Only inserted values are encoded, never the text of the template
// (B31).
type Encoding int

// The encodings.
const (
	// Text inserts values unchanged: chat messages, files.
	Text Encoding = iota
	// URL escapes everything but ASCII letters, digits and "-._~", with
	// "%20" for a space, so a value fits into the path and the query of the
	// address of a web request.
	URL
	// HTML escapes <, >, &, " and ' for overlays.
	HTML
	// JSON escapes like the content of a JSON string, without adding quotes,
	// for the body of a web request. Invalid UTF-8 becomes U+FFFD.
	JSON
)

// String returns the name of e.
func (e Encoding) String() string {
	switch e {
	case Text:
		return "text"
	case URL:
		return "url"
	case HTML:
		return "html"
	case JSON:
		return "json"
	default:
		return "unknown"
	}
}

// valid reports whether e is one of the encodings.
func (e Encoding) valid() bool {
	return e >= Text && e <= JSON
}

// write writes the encoded value s to b.
func (e Encoding) write(b *strings.Builder, s string) {
	switch e {
	case URL:
		// QueryEscape writes a space as "+" and a "+" as "%2B", so every
		// remaining "+" was a space.
		b.WriteString(strings.ReplaceAll(url.QueryEscape(s), "+", "%20"))
	case HTML:
		b.WriteString(html.EscapeString(s))
	case JSON:
		// The error only reports invalid UTF-8, which AppendQuote has
		// already replaced with U+FFFD.
		quoted, _ := jsontext.AppendQuote(nil, s)
		b.Write(quoted[1 : len(quoted)-1])
	default:
		b.WriteString(s)
	}
}
