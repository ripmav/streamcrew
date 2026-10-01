// SPDX-License-Identifier: Apache-2.0

// Package textfunc evaluates the text functions in the value of the special
// identifier action (spec actions.md, B52 to B55), such as tolower(…) or
// replace(…,…,…), nested as deep as needed.
//
// Parse reads the structure from the value before any identifier is
// inserted (B53): function names, parentheses, commas and parameters in
// double quotes. The text between them consists of templates, which the
// caller renders in one render with the other templates of the action
// (Templates; actions.md, B3). EvalWithTexts then applies the functions to
// the rendered texts. An inserted value thus never becomes a function or a
// separator, even if it contains parentheses or commas.
//
// A function is a name of ASCII letters, regardless of case, directly
// followed by "(", that does not continue a word or a $ token. Parameters
// are separated by commas and are not trimmed: a space after a comma
// belongs to the parameter. Parentheses without a name pair up inside a
// parameter and are text, so are the commas between them. A parameter that
// starts and ends with a double quote, directly after "(" or "," and
// directly before "," or ")", is text with commas and parentheses; $
// identifiers in it are inserted, as in quoted text of an expression. A
// name without its closing parenthesis stays text; an unknown name with one
// is an error.
package textfunc

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/template"
)

var (
	// ErrInvalid is wrapped by the errors of Parse: an unknown function, a
	// wrong number of parameters, a fixed pattern or date that is invalid,
	// or functions nested too deeply (B55).
	ErrInvalid = errors.New("invalid text functions")
	// ErrEvaluation is wrapped by the errors of EvalWithTexts: an invalid
	// pattern or date, a date in the wrong direction (B55), or a result
	// beyond MaxResult.
	ErrEvaluation = errors.New("text functions cannot be evaluated")
)

// Limits of text functions.
const (
	// MaxDepth is how deep functions may be nested.
	MaxDepth = 100
	// MaxResult is the largest result of one function in bytes, the size of
	// the answer of a web request (actions.md, B73), so that nested
	// replacements cannot exhaust the memory.
	MaxResult = 1 << 20
)

// Text is a parsed value with text functions. It is immutable and safe for
// concurrent use.
type Text struct {
	src   string
	nodes []node
}

// node is text, a template rendered by the caller, or a call of a
// function.
type node struct {
	text template.Template
	call *call
}

// call is a function with its parameters, each a sequence of nodes.
type call struct {
	name   string
	fn     function
	params [][]node
}

// Parse reads the structure of the functions in src (B53). It returns an
// error wrapping ErrInvalid for an unknown function, a wrong number of
// parameters, a pattern of count or a date of datefrom or dateto that has
// no identifiers and is invalid, and functions nested deeper than MaxDepth.
func Parse(src string) (*Text, error) {
	p := &parser{src: src, calls: make(map[int]parsedCall)}
	nodes, _, err := p.sequence(0, false)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return &Text{src: src, nodes: nodes}, nil
}

// String returns the text t was parsed from.
func (t *Text) String() string {
	return t.src
}

// Templates returns the templates of t in the order EvalWithTexts takes
// their texts.
func (t *Text) Templates() []template.Template {
	var out []template.Template
	var walk func(nodes []node)
	walk = func(nodes []node) {
		for _, n := range nodes {
			if n.call == nil {
				out = append(out, n.text)
				continue
			}
			for _, param := range n.call.params {
				walk(param)
			}
		}
	}
	walk(t.nodes)
	return out
}

// EvalWithTexts applies the functions of t to texts, the rendered
// templates of Templates (B54). now and loc are the current time and the
// time zone of the profile for datefrom and dateto. It returns an error
// wrapping ErrEvaluation if a function fails (B55) or the number of texts
// is not that of the templates.
func (t *Text) EvalWithTexts(texts []string, now time.Time, loc *time.Location) (string, error) {
	e := evaluation{texts: texts, now: now, loc: loc}
	out, err := e.sequence(t.nodes)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrEvaluation, err)
	}
	if e.next != len(texts) {
		return "", fmt.Errorf("%w: %d texts for %d templates", ErrEvaluation, len(texts), e.next)
	}
	return out, nil
}

// evaluation is the state of EvalWithTexts.
type evaluation struct {
	texts []string
	// next is the index of the next text.
	next int
	now  time.Time
	loc  *time.Location
}

// sequence returns the text of nodes.
func (e *evaluation) sequence(nodes []node) (string, error) {
	var b strings.Builder
	for _, n := range nodes {
		if n.call == nil {
			if e.next >= len(e.texts) {
				return "", fmt.Errorf("too few texts: %d", len(e.texts))
			}
			b.WriteString(e.texts[e.next])
			e.next++
			continue
		}
		args := make([]string, len(n.call.params))
		for i, param := range n.call.params {
			arg, err := e.sequence(param)
			if err != nil {
				return "", err
			}
			args[i] = arg
		}
		out, err := n.call.fn.apply(args, e.now, e.loc)
		if err != nil {
			return "", fmt.Errorf("%s: %w", n.call.name, err)
		}
		if len(out) > MaxResult {
			return "", fmt.Errorf("%s: result longer than %d bytes", n.call.name, MaxResult)
		}
		b.WriteString(out)
	}
	return b.String(), nil
}

// parser reads the structure of a text.
type parser struct {
	src string
	// calls holds the outcome of parsing a call per position of its name.
	// The outcome does not depend on where the call stands, so each
	// position is parsed once, even when an enclosing call turns out to
	// have no closing parenthesis.
	calls map[int]parsedCall
	depth int
}

// parsedCall is the outcome of parsing a call; ok is false if the name
// has no closing parenthesis and stays text.
type parsedCall struct {
	call *call
	end  int
	ok   bool
	err  error
}

// sequence parses text and calls from i: to the end of the text at the top
// level, inside a call to the next "," or ")" outside parentheses without
// a name. It returns the nodes and the position where it stopped, the
// length of the text if it reached the end.
func (p *parser) sequence(i int, inCall bool) ([]node, int, error) {
	var nodes []node
	start := i // start of the pending text
	flush := func(end int) {
		if start < end {
			nodes = append(nodes, node{text: template.Parse(p.src[start:end])})
		}
	}
	open := 0 // parentheses without a name
	for i < len(p.src) {
		c := p.src[i]
		switch {
		case c == '$':
			i = p.skipToken(i)
			continue
		case inCall && open == 0 && (c == ',' || c == ')'):
			flush(i)
			return nodes, i, nil
		case c == '(':
			open++
		case c == ')' && open > 0:
			open--
		case isLetter(c) && (i == 0 || !isWordByte(p.src[i-1])):
			end := i
			for end < len(p.src) && isLetter(p.src[end]) {
				end++
			}
			if end < len(p.src) && p.src[end] == '(' {
				pc := p.call(i, end)
				if pc.err != nil {
					return nil, 0, pc.err
				}
				if pc.ok {
					flush(i)
					nodes = append(nodes, node{call: pc.call})
					i, start = pc.end, pc.end
					continue
				}
			}
			i = end
			continue
		}
		i++
	}
	flush(len(p.src))
	return nodes, len(p.src), nil
}

// skipToken returns the position after the $ token at i, or after the "$"
// if no token starts there (template.md, B1).
func (p *parser) skipToken(i int) int {
	end := i + 1
	for end < len(p.src) && isTokenByte(p.src[end]) {
		end++
	}
	return end
}

// call parses the call whose name starts at at and whose "(" is at paren,
// once per position.
func (p *parser) call(at, paren int) parsedCall {
	if pc, ok := p.calls[at]; ok {
		return pc
	}
	pc := p.parseCall(at, paren)
	p.calls[at] = pc
	return pc
}

// parseCall parses the call whose name starts at at and whose "(" is at
// paren.
func (p *parser) parseCall(at, paren int) parsedCall {
	name := strings.ToLower(p.src[at:paren])
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > MaxDepth {
		return parsedCall{err: fmt.Errorf("functions nested deeper than %d", MaxDepth)}
	}
	var params [][]node
	i := paren + 1
	for {
		if q, ok := p.quoted(i); ok {
			params = append(params, []node{{text: template.Parse(p.src[i+1 : q])}})
			i = q + 1
		} else {
			nodes, stop, err := p.sequence(i, true)
			if err != nil {
				return parsedCall{err: err}
			}
			if stop == len(p.src) {
				return parsedCall{} // no closing parenthesis: text (B53)
			}
			params = append(params, nodes)
			i = stop
		}
		if p.src[i] == ')' {
			break
		}
		i++ // the comma
	}
	fn, known := functions()[name]
	switch {
	case !known:
		return parsedCall{err: fmt.Errorf("unknown function %q", name)}
	case len(params) != fn.params:
		return parsedCall{err: fmt.Errorf("%s takes %d parameters, not %d", name, fn.params, len(params))}
	}
	for i, param := range params {
		text, fixed := fixedText(param)
		if !fixed || fn.check == nil {
			continue
		}
		if err := fn.check(i, text); err != nil {
			return parsedCall{err: fmt.Errorf("%s: %w", name, err)}
		}
	}
	return parsedCall{call: &call{name: name, fn: fn, params: params}, end: i + 1, ok: true}
}

// quoted returns the position of the closing quote if a quoted parameter
// starts at i: a double quote, the next double quote and directly after it
// "," or ")".
func (p *parser) quoted(i int) (int, bool) {
	if i >= len(p.src) || p.src[i] != '"' {
		return 0, false
	}
	q := strings.IndexByte(p.src[i+1:], '"')
	if q < 0 {
		return 0, false
	}
	q += i + 1
	if q+1 >= len(p.src) || p.src[q+1] != ',' && p.src[q+1] != ')' {
		return 0, false
	}
	return q, true
}

// fixedText returns the text of a parameter that has neither identifiers
// nor calls.
func fixedText(param []node) (string, bool) {
	var b strings.Builder
	for _, n := range param {
		if n.call != nil {
			return "", false
		}
		for segment, token := range n.text.Segments() {
			if token {
				return "", false
			}
			b.WriteString(segment)
		}
	}
	return b.String(), true
}

// isLetter reports whether c is an ASCII letter, of which function names
// consist.
func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// isWordByte reports whether c continues a word, so that a function name
// cannot start after it.
func isWordByte(c byte) bool {
	return isLetter(c) || c >= '0' && c <= '9'
}

// isTokenByte reports whether c continues a $ token (template.md, B1).
func isTokenByte(c byte) bool {
	return isWordByte(c) || c == ':'
}
