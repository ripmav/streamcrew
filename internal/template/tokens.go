// SPDX-License-Identifier: MIT

package template

import (
	"context"
	"iter"
)

// Segments returns the parts of t in order: literal text with token false,
// and each token as written, with "$", with token true. Expressions use it
// to put variables in place of the tokens (Code-ADR-0012, point 8).
func (t Template) Segments() iter.Seq2[string, bool] {
	return func(yield func(string, bool) bool) {
		for _, p := range t.pieces {
			text := p.text
			if p.name != "" {
				text = "$" + p.text
			}
			if !yield(text, p.name != "") {
				return
			}
		}
	}
}

// RenderEach renders each template of ts with the encoding Text, all in one
// render, so each identifier is resolved at most once across them (B21).
// Expressions use it to get the values of their identifiers (B51). Like
// Render, it fails only when ctx is done.
func (e *Engine) RenderEach(ctx context.Context, ts []Template, s *Scope) ([]string, error) {
	s = s.forRender()
	texts := make([]string, len(ts))
	for i, t := range ts {
		text, err := e.render(ctx, t, s, Text)
		if err != nil {
			return nil, err
		}
		texts[i] = text
	}
	return texts, nil
}
