// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/feature/plural"
	xmessage "golang.org/x/text/message"
	"golang.org/x/text/number"
)

// pluralForms returns the plural forms of lang after CLDR, which
// golang.org/x/text/feature/plural selects from (ADR-0022, point 4): of
// cardinal numbers, or of ordinal numbers for selectordinal. A test checks
// the list against the rules.
func pluralForms(lang Language, ordinal bool) []plural.Form {
	switch {
	case lang == English && ordinal:
		return []plural.Form{plural.One, plural.Two, plural.Few, plural.Other}
	case lang == German && ordinal:
		return []plural.Form{plural.Other}
	default:
		return []plural.Form{plural.One, plural.Other}
	}
}

// maxOperand reduces plural operands that do not fit into an int; the
// rules allow them modulo this number (golang.org/x/text/feature/plural,
// Rules.MatchPlural). Operands that fit are passed whole, because a rule
// such as "one" in English looks at the whole number.
const maxOperand = 10_000_000

// renderer writes a message in a language with the values of a Message.
type renderer struct {
	catalog *Catalog
	lang    Language
	args    map[string]Value
}

// message writes m; pound is the number of the plural whose case m is, nil
// outside of one.
func (r renderer) message(b *strings.Builder, m message, pound *Value) error {
	for _, pt := range m {
		var err error
		switch pt := pt.(type) {
		case textPart:
			b.WriteString(string(pt))
		case argPart:
			err = r.plain(b, pt.name)
		case numberPart:
			err = r.number(b, pt.name, pt.style)
		case pluralPart:
			err = r.plural(b, pt)
		case selectPart:
			err = r.choice(b, pt)
		case poundPart:
			// The parser makes # only in the cases of a plural.
			err = r.formatNumber(b, *pound, styleDecimal)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// value returns the value name.
func (r renderer) value(name string) (Value, error) {
	v, ok := r.args[name]
	if !ok || v.kind == kindNone {
		return Value{}, fmt.Errorf("%w %q", ErrMissingValue, name)
	}
	return v, nil
}

// numeric returns the value name, which must be a finite number.
func (r renderer) numeric(name string) (Value, error) {
	v, err := r.value(name)
	if err != nil {
		return Value{}, err
	}
	switch v.kind {
	case kindInteger:
		return v, nil
	case kindDecimal:
		if math.IsNaN(v.decimal) || math.IsInf(v.decimal, 0) {
			return Value{}, fmt.Errorf("%w: %q is %v, not a finite number", ErrValueType, name, v.decimal)
		}
		return v, nil
	default:
		return Value{}, fmt.Errorf("%w: %q is not a number", ErrValueType, name)
	}
}

// plain writes {name}: a text as it is, a number without the format of the
// language, a duration in words.
func (r renderer) plain(b *strings.Builder, name string) error {
	v, err := r.value(name)
	if err != nil {
		return err
	}
	switch v.kind {
	case kindText:
		b.WriteString(v.text)
	case kindInteger:
		b.WriteString(strconv.FormatInt(v.integer, 10))
	case kindDecimal:
		b.WriteString(strconv.FormatFloat(v.decimal, 'f', -1, 64))
	case kindDuration:
		return r.duration(b, name, v.duration)
	default:
		return fmt.Errorf("%w %q", ErrMissingValue, name)
	}
	return nil
}

// number writes {name, number, style}.
func (r renderer) number(b *strings.Builder, name string, style numberStyle) error {
	v, err := r.numeric(name)
	if err != nil {
		return err
	}
	return r.formatNumber(b, v, style)
}

// formatNumber writes the number v in the format of the language: at most
// three fraction digits, none for integer, as a percentage for percent,
// as ICU does.
func (r renderer) formatNumber(b *strings.Builder, v Value, style numberStyle) error {
	var x any = v.integer
	if v.kind == kindDecimal {
		x = v.decimal
	}
	var f number.Formatter
	switch style {
	case styleInteger:
		f = number.Decimal(x, number.MaxFractionDigits(0))
	case stylePercent:
		f = number.Percent(x)
	default:
		f = number.Decimal(x)
	}
	b.WriteString(xmessage.NewPrinter(r.lang.tag()).Sprint(f))
	return nil
}

// plural writes the case of a plural or selectordinal that fits its
// number: an exact case first, else the plural form of the number.
func (r renderer) plural(b *strings.Builder, pp pluralPart) error {
	v, err := r.numeric(pp.name)
	if err != nil {
		return err
	}
	for _, c := range pp.exact {
		if v.equals(c.value) {
			return r.message(b, c.msg, &v)
		}
	}
	rules := plural.Cardinal
	if pp.ordinal {
		rules = plural.Ordinal
	}
	i, digits, fraction := v.operands()
	form := rules.MatchPlural(r.lang.tag(), i, digits, digits, fraction, fraction)
	m, ok := pp.forms[form]
	if !ok {
		m = pp.forms[plural.Other]
	}
	return r.message(b, m, &v)
}

// choice writes the case of a select for its text, or the case other.
func (r renderer) choice(b *strings.Builder, sp selectPart) error {
	v, err := r.value(sp.name)
	if err != nil {
		return err
	}
	if v.kind != kindText {
		return fmt.Errorf("%w: %q is not a text", ErrValueType, sp.name)
	}
	m, ok := sp.cases[v.text]
	if !ok {
		m = sp.cases["other"]
	}
	return r.message(b, m, nil)
}

// equals reports whether the number v is n.
func (v Value) equals(n int64) bool {
	if v.kind == kindDecimal {
		return v.decimal == float64(n)
	}
	return v.integer == n
}

// operands returns the plural operands of the number v: its integer digits
// i, the count of its fraction digits and the fraction digits as a number
// (CLDR, operands i, v and f). A number from float64 has no trailing zeros,
// so the operands w and t equal v and f.
func (v Value) operands() (i, digits, fraction int) {
	if v.kind != kindDecimal {
		if v.integer == math.MinInt64 {
			return int(-(v.integer % maxOperand)), 0, 0
		}
		return int(max(v.integer, -v.integer)), 0, 0
	}
	whole, frac, _ := strings.Cut(strconv.FormatFloat(math.Abs(v.decimal), 'f', -1, 64), ".")
	return operand(whole), len(frac), operand(frac)
}

// operand returns the number of the digits s; modulo maxOperand if it does
// not fit into an int; 0 for none.
func operand(s string) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if len(s) > 7 {
		s = s[len(s)-7:]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// durationUnits returns the units of durations from the largest, with
// their message.
func durationUnits() []struct {
	size time.Duration
	key  Key
} {
	return []struct {
		size time.Duration
		key  Key
	}{
		{24 * time.Hour, KeyDurationDays},
		{time.Hour, KeyDurationHours},
		{time.Minute, KeyDurationMinutes},
		{time.Second, KeyDurationSeconds},
	}
}

// duration writes d in whole seconds, rounded up, with every unit that is
// not 0, e.g. "1 minute 5 seconds"; 0 is "0 seconds" (ADR-0022, point 6).
func (r renderer) duration(b *strings.Builder, name string, d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("%w: %q is a negative duration", ErrValueType, name)
	}
	rest := (d + time.Second - 1) / time.Second * time.Second
	var parts []string
	for _, u := range durationUnits() {
		n := rest / u.size
		rest -= n * u.size
		if n == 0 && (u.size != time.Second || len(parts) > 0) {
			continue
		}
		s, err := r.catalog.Render(r.lang, Message{Key: u.key, Args: map[string]Value{"n": Int(int64(n))}})
		if err != nil {
			return err
		}
		parts = append(parts, s)
	}
	b.WriteString(strings.Join(parts, " "))
	return nil
}
