// SPDX-License-Identifier: MIT

package i18n

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/feature/plural"
)

// ErrSyntax is wrapped by the errors of messages that are not valid ICU
// MessageFormat in the subset of ADR-0022, point 3.
var ErrSyntax = errors.New("invalid message")

// message is a parsed message: its parts in order.
type message []part

// part is a piece of a message: textPart, argPart, numberPart, pluralPart,
// selectPart or poundPart.
type part interface {
	isPart()
}

// textPart is text, already without the quoting.
type textPart string

// argPart is {name}: the value as text.
type argPart struct {
	name string
}

// numberStyle is the style of a number argument.
type numberStyle int

// The styles of {name, number, style}.
const (
	styleDecimal numberStyle = iota // {name, number}
	styleInteger                    // {name, number, integer}
	stylePercent                    // {name, number, percent}
)

// numberPart is {name, number} with a style.
type numberPart struct {
	name  string
	style numberStyle
}

// exactCase is a case =N of a plural.
type exactCase struct {
	value int64
	msg   message
}

// pluralPart is {name, plural, ...} or {name, selectordinal, ...}.
type pluralPart struct {
	name    string
	ordinal bool
	exact   []exactCase
	forms   map[plural.Form]message
}

// selectPart is {name, select, ...}; cases has "other".
type selectPart struct {
	name  string
	cases map[string]message
}

// poundPart is # in a case of a plural: its number.
type poundPart struct{}

func (textPart) isPart()   {}
func (argPart) isPart()    {}
func (numberPart) isPart() {}
func (pluralPart) isPart() {}
func (selectPart) isPart() {}
func (poundPart) isPart()  {}

// formNames are the plural forms by their names in messages.
func formNames() map[string]plural.Form {
	return map[string]plural.Form{
		"zero": plural.Zero, "one": plural.One, "two": plural.Two,
		"few": plural.Few, "many": plural.Many, "other": plural.Other,
	}
}

// parser reads a message. The syntax characters are ASCII, so it works on
// bytes and copies all others unchanged.
type parser struct {
	src string
	pos int
}

// parse parses src as a message in the subset of ADR-0022, point 3.
func parse(src string) (message, error) {
	p := &parser{src: src}
	m, err := p.message(false)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.src) {
		return nil, p.errorf("unexpected %q", p.src[p.pos])
	}
	return m, nil
}

// errorf returns a syntax error at the current position.
func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("%w at byte %d: %s", ErrSyntax, p.pos, fmt.Sprintf(format, args...))
}

// peek returns the byte at offset from the current position; 0 beyond the
// end.
func (p *parser) peek(offset int) byte {
	if p.pos+offset >= len(p.src) {
		return 0
	}
	return p.src[p.pos+offset]
}

// message reads parts up to the end or the "}" that closes a case, which
// it leaves. In the cases of a plural, # is its number.
func (p *parser) message(inPlural bool) (message, error) {
	var (
		m    message
		text strings.Builder
	)
	flush := func() {
		if text.Len() > 0 {
			m = append(m, textPart(text.String()))
			text.Reset()
		}
	}
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == '}':
			flush()
			return m, nil
		case c == '{':
			flush()
			a, err := p.argument()
			if err != nil {
				return nil, err
			}
			m = append(m, a)
		case c == '#' && inPlural:
			flush()
			m = append(m, poundPart{})
			p.pos++
		case c == '\'':
			p.apostrophe(&text, inPlural)
		default:
			text.WriteByte(c)
			p.pos++
		}
	}
	flush()
	return m, nil
}

// apostrophe reads an apostrophe in text as ICU does since 4.8: two are
// one apostrophe; one right before "{", "}" or, in the cases of a plural,
// "#" quotes the text up to the next single apostrophe or the end; any
// other is a plain character.
func (p *parser) apostrophe(text *strings.Builder, inPlural bool) {
	switch next := p.peek(1); {
	case next == '\'':
		text.WriteByte('\'')
		p.pos += 2
	case next == '{' || next == '}' || next == '#' && inPlural:
		p.pos++
		for p.pos < len(p.src) {
			c := p.src[p.pos]
			if c != '\'' {
				text.WriteByte(c)
				p.pos++
				continue
			}
			if p.peek(1) != '\'' {
				p.pos++
				return
			}
			text.WriteByte('\'')
			p.pos += 2
		}
	default:
		text.WriteByte('\'')
		p.pos++
	}
}

// space skips white space.
func (p *parser) space() {
	for p.pos < len(p.src) && strings.IndexByte(" \t\r\n", p.src[p.pos]) >= 0 {
		p.pos++
	}
}

// word reads lowercase letters, digits and "_", the characters of names,
// types, styles and selectors.
func (p *parser) word() string {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			break
		}
		p.pos++
	}
	return p.src[start:p.pos]
}

// expect reads c after white space.
func (p *parser) expect(c byte) error {
	p.space()
	if p.peek(0) != c {
		return p.errorf("want %q", c)
	}
	p.pos++
	return nil
}

// argument reads an argument from its "{" to its "}".
func (p *parser) argument() (part, error) {
	p.pos++
	p.space()
	name := p.word()
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return nil, p.errorf("want an argument name of lowercase letters, digits and _, starting with a letter")
	}
	p.space()
	switch p.peek(0) {
	case '}':
		p.pos++
		return argPart{name: name}, nil
	case ',':
		p.pos++
	default:
		return nil, p.errorf("want \"}\" or \",\" after the argument name")
	}
	p.space()
	switch typ := p.word(); typ {
	case "number":
		return p.number(name)
	case "plural":
		return p.plural(name, false)
	case "selectordinal":
		return p.plural(name, true)
	case "select":
		return p.choice(name)
	default:
		return nil, p.errorf("argument type %q is not supported", typ)
	}
}

// number reads the rest of {name, number} or {name, number, style}.
func (p *parser) number(name string) (part, error) {
	p.space()
	if p.peek(0) == '}' {
		p.pos++
		return numberPart{name: name, style: styleDecimal}, nil
	}
	if err := p.expect(','); err != nil {
		return nil, err
	}
	p.space()
	var style numberStyle
	switch s := p.word(); s {
	case "integer":
		style = styleInteger
	case "percent":
		style = stylePercent
	default:
		return nil, p.errorf("number style %q is not supported", s)
	}
	if err := p.expect('}'); err != nil {
		return nil, err
	}
	return numberPart{name: name, style: style}, nil
}

// plural reads the cases of {name, plural, ...} or {name, selectordinal,
// ...} and its "}".
func (p *parser) plural(name string, ordinal bool) (part, error) {
	if err := p.expect(','); err != nil {
		return nil, err
	}
	pp := pluralPart{name: name, ordinal: ordinal, forms: make(map[plural.Form]message)}
	for {
		p.space()
		switch p.peek(0) {
		case '}':
			p.pos++
			if _, ok := pp.forms[plural.Other]; !ok {
				return nil, p.errorf("plural %q without the case other", name)
			}
			return pp, nil
		case '=':
			if err := p.exactCase(&pp); err != nil {
				return nil, err
			}
		default:
			if err := p.formCase(&pp); err != nil {
				return nil, err
			}
		}
	}
}

// exactCase reads a case =N of a plural.
func (p *parser) exactCase(pp *pluralPart) error {
	p.pos++
	digits := p.word()
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 0 {
		return p.errorf("want a whole number of 0 or more after \"=\", not %q", digits)
	}
	if slices.ContainsFunc(pp.exact, func(c exactCase) bool { return c.value == n }) {
		return p.errorf("case =%d twice", n)
	}
	m, err := p.caseMessage(true)
	if err != nil {
		return err
	}
	pp.exact = append(pp.exact, exactCase{value: n, msg: m})
	return nil
}

// formCase reads a case of a plural form, e.g. "one".
func (p *parser) formCase(pp *pluralPart) error {
	selector := p.word()
	if selector == "offset" && p.peek(0) == ':' {
		return p.errorf("offset is not supported")
	}
	form, ok := formNames()[selector]
	if !ok {
		return p.errorf("want a plural form or =N, not %q", selector)
	}
	if _, dup := pp.forms[form]; dup {
		return p.errorf("case %s twice", selector)
	}
	m, err := p.caseMessage(true)
	if err != nil {
		return err
	}
	pp.forms[form] = m
	return nil
}

// choice reads the cases of {name, select, ...} and its "}".
func (p *parser) choice(name string) (part, error) {
	if err := p.expect(','); err != nil {
		return nil, err
	}
	sp := selectPart{name: name, cases: make(map[string]message)}
	for {
		p.space()
		if p.peek(0) == '}' {
			p.pos++
			if _, ok := sp.cases["other"]; !ok {
				return nil, p.errorf("select %q without the case other", name)
			}
			return sp, nil
		}
		selector := p.word()
		if selector == "" {
			return nil, p.errorf("want a case of lowercase letters, digits and _")
		}
		if _, dup := sp.cases[selector]; dup {
			return nil, p.errorf("case %s twice", selector)
		}
		m, err := p.caseMessage(false)
		if err != nil {
			return nil, err
		}
		sp.cases[selector] = m
	}
}

// caseMessage reads "{", the message of a case and "}".
func (p *parser) caseMessage(inPlural bool) (message, error) {
	if err := p.expect('{'); err != nil {
		return nil, err
	}
	m, err := p.message(inPlural)
	if err != nil {
		return nil, err
	}
	if p.peek(0) != '}' {
		return nil, p.errorf("case without its closing \"}\"")
	}
	p.pos++
	return m, nil
}

// checkForms checks the plurals of m against the plural forms of lang: no
// other forms, and all of them (ADR-0022, points 4 and 8).
func checkForms(lang Language, m message) error {
	var errs []error
	walk(m, func(pt part) {
		pp, ok := pt.(pluralPart)
		if !ok {
			return
		}
		want := pluralForms(lang, pp.ordinal)
		for form := range pp.forms {
			if !slices.Contains(want, form) {
				errs = append(errs, fmt.Errorf("%w: %s has no plural form %s", ErrSyntax, lang, formName(form)))
			}
		}
		for _, form := range want {
			if _, ok := pp.forms[form]; !ok {
				errs = append(errs, fmt.Errorf("%w: plural %q without the form %s that %s needs", ErrSyntax, pp.name, formName(form), lang))
			}
		}
	})
	return errors.Join(errs...)
}

// formName returns the name of form in messages.
func formName(form plural.Form) string {
	for name, f := range formNames() {
		if f == form {
			return name
		}
	}
	return "unknown"
}

// walk calls fn for every part of m, also in the cases of plurals and
// selects.
func walk(m message, fn func(part)) {
	for _, pt := range m {
		fn(pt)
		switch pt := pt.(type) {
		case pluralPart:
			for _, c := range pt.exact {
				walk(c.msg, fn)
			}
			for _, form := range slices.Sorted(maps.Keys(pt.forms)) {
				walk(pt.forms[form], fn)
			}
		case selectPart:
			for _, k := range slices.Sorted(maps.Keys(pt.cases)) {
				walk(pt.cases[k], fn)
			}
		}
	}
}

// argClass is how a message uses a value.
type argClass int

// The classes of values.
const (
	classText   argClass = iota // {name}: any value
	classNumber                 // number, plural, selectordinal
	classChoice                 // select
)

// argClasses returns the values of m with their class. A value used both
// as a number and as a choice is an error.
func argClasses(m message) (map[string]argClass, error) {
	classes := make(map[string]argClass)
	var errs []error
	use := func(name string, class argClass) {
		old, ok := classes[name]
		switch {
		case !ok || old == classText:
			classes[name] = class
		case class == classText || class == old:
		default:
			errs = append(errs, fmt.Errorf("%w: value %q used as a number and as a choice", ErrSyntax, name))
		}
	}
	walk(m, func(pt part) {
		switch pt := pt.(type) {
		case argPart:
			use(pt.name, classText)
		case numberPart:
			use(pt.name, classNumber)
		case pluralPart:
			use(pt.name, classNumber)
		case selectPart:
			use(pt.name, classChoice)
		}
	})
	return classes, errors.Join(errs...)
}

// sameArgs checks that m uses the same values as english, each in a class
// that fits the one in english.
func sameArgs(english, m message) error {
	want, err := argClasses(english)
	if err != nil {
		return err
	}
	got, err := argClasses(m)
	if err != nil {
		return err
	}
	if !slices.Equal(slices.Sorted(maps.Keys(want)), slices.Sorted(maps.Keys(got))) {
		return fmt.Errorf("values %v, but English has %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	for name, class := range got {
		if w := want[name]; class != w && class != classText && w != classText {
			return fmt.Errorf("value %q of another kind than in English", name)
		}
	}
	return nil
}
