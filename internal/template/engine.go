// SPDX-License-Identifier: MIT

package template

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Source provides identifiers whose names change at run time (B10): the
// global values of the special identifier action and dynamic names such as
// counters. The engine asks it on every render, so new names apply at once.
type Source interface {
	// Match returns the length of the longest beginning of token that is a
	// name of the source, and its resolver; 0 if there is none. token is in
	// lower case, without "$". A source that loads its names can use
	// Scope.Memo to load them once per render. After an error the engine
	// logs a warning and skips the source for the rest of the render (B23).
	Match(ctx context.Context, s *Scope, token string) (int, Resolver, error)
}

// Engine renders templates. It is safe for concurrent use.
type Engine struct {
	registry *Registry
	sources  []Source
	logger   *slog.Logger
}

// Option configures an Engine.
type Option func(*Engine)

// WithLogger sets the logger for identifiers that cannot be resolved (B23);
// the default discards.
func WithLogger(l *slog.Logger) Option {
	return func(e *Engine) { e.logger = l }
}

// WithSources sets the sources of dynamic names in the order of B10: first
// the global values, then dynamic names such as counters. For names of the
// same length an earlier source wins over a later one (B11).
func WithSources(sources ...Source) Option {
	return func(e *Engine) { e.sources = sources }
}

// New returns an engine with the built-in identifiers of registry; a nil
// registry has none.
func New(registry *Registry, opts ...Option) *Engine {
	e := &Engine{registry: registry}
	for _, opt := range opts {
		opt(e)
	}
	if e.registry == nil {
		e.registry = &Registry{}
	}
	if e.logger == nil {
		e.logger = slog.New(slog.DiscardHandler)
	}
	return e
}

// Render renders t with the values of s, which may be nil, and encodes each
// inserted value with enc (B30, B31).
//
// For each token the longest known name wins across all sources, for equal
// lengths the source that ranks first (B2, B10, B11); the rest of the token
// stays as written. Render resolves an identifier only if it occurs (B20)
// and at most once (B21). A token without a known name, an identifier
// without a value and one whose resolver fails stay as written (B3, B4,
// B23). Render fails only for an unknown encoding and when ctx is canceled
// or its deadline passes (B24).
func (e *Engine) Render(ctx context.Context, t Template, s *Scope, enc Encoding) (string, error) {
	if !enc.valid() {
		return "", fmt.Errorf("render template: unknown encoding %d", int(enc))
	}
	s = s.forRender()
	var b strings.Builder
	b.Grow(len(t.src))
	for _, p := range t.pieces {
		if p.name == "" {
			b.WriteString(p.text)
			continue
		}
		n, v, ok, err := e.resolve(ctx, s, p.name)
		if err != nil {
			return "", err
		}
		if !ok {
			b.WriteByte('$')
			b.WriteString(p.text)
			continue
		}
		enc.write(&b, v.Text)
		b.WriteString(p.text[n:])
	}
	return b.String(), nil
}

// resolve returns the length of the identifier that token starts with and
// its value; ok is false if the token stays as written. The error is set
// only if ctx is done.
func (e *Engine) resolve(ctx context.Context, s *Scope, token string) (n int, v Value, ok bool, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, Value{}, false, fmt.Errorf("render template: %w", ctxErr)
	}
	h, err := e.match(ctx, s, token)
	if err != nil || h.n == 0 {
		return 0, Value{}, false, err
	}
	name := token[:h.n]
	if !h.uncached {
		if r, cached := s.render.cache[name]; cached {
			return h.n, r.v, r.ok, nil
		}
	}
	v, ok, err = h.resolve(ctx, s)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, Value{}, false, fmt.Errorf("render template: resolve $%s: %w", name, ctxErr)
		}
		e.logger.WarnContext(ctx, "identifier left unresolved", "identifier", name, "error", err)
		v, ok = Value{}, false
	}
	if !h.uncached {
		if s.render.cache == nil {
			s.render.cache = make(map[string]result)
		}
		s.render.cache[name] = result{v: v, ok: ok}
	}
	return h.n, v, ok, nil
}

// match finds the identifier with the longest name that token starts with,
// in the order of B10: values of the run, sources, built-in identifiers. The
// error is set only if ctx is done.
func (e *Engine) match(ctx context.Context, s *Scope, token string) (hit, error) {
	var h hit
	if n, v := s.value(token); n > 0 {
		h = hit{n: n, resolve: constant(v)}
	}
	for i, src := range e.sources {
		if s.render.failed[i] {
			continue
		}
		n, resolve, err := src.Match(ctx, s, token)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return hit{}, fmt.Errorf("render template: %w", ctxErr)
			}
			e.logger.WarnContext(ctx, "identifier source skipped", "source", i, "error", err)
			if s.render.failed == nil {
				s.render.failed = make(map[int]bool)
			}
			s.render.failed[i] = true
			continue
		}
		if n > h.n && n <= len(token) && resolve != nil {
			h = hit{n: n, resolve: resolve}
		}
	}
	if b := e.registry.match(token); b.n > h.n {
		h = b
	}
	return h, nil
}
