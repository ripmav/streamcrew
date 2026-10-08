// SPDX-License-Identifier: MIT

package template

import (
	"context"
	"fmt"
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

// Rendered is a template after a render.
type Rendered struct {
	Text string
	// Replaced reports whether every token got a value (B3, B4); a text
	// without tokens counts as replaced. The comparison "replaced" of the
	// conditional action reads it (spec actions.md, B24).
	Replaced bool
}

// RenderEach renders each template of ts with the encoding Text, all in one
// render, so each identifier is resolved at most once across them (B21).
// Expressions use it to get the values of their identifiers (B51), the
// conditional action to get all values of its clauses (actions.md, B27).
// Like Render, it fails only for a scope that lacks what a render needs and
// when ctx is done.
func (e *Engine) RenderEach(ctx context.Context, ts []Template, s *Scope) ([]Rendered, error) {
	if err := s.check(); err != nil {
		return nil, fmt.Errorf("render templates: %w", err)
	}
	s = s.forRender()
	out := make([]Rendered, len(ts))
	for i, t := range ts {
		r, err := e.render(ctx, t, s, Text)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}
