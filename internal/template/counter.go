// SPDX-License-Identifier: Apache-2.0

package template

import (
	"context"
	"fmt"
	"strings"

	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/counter"
)

// [Interop] The identifier names in this file follow the original (spec
// counters-and-quotes.md, B1, B4) and may be replaced after the legal
// assessment (roadmap Gate O, O.1).

// counterDisplaySuffix turns the name of a counter into the name of its
// formatted value.
const counterDisplaySuffix = "display"

// Counters lists the counters of the profile; *store.Store implements it.
type Counters interface {
	Counters(ctx context.Context) ([]counter.Counter, error)
}

// CounterSource returns the source of the counter identifiers: $<name> is the
// value of the counter, $<name>display the value with thousands separators,
// and with exactly two decimal places unless it is whole (spec
// counters-and-quotes.md, B1, B4). Names are case-insensitive. The
// source lists the counters once per render, so a new or renamed counter
// applies from the next render on.
func CounterSource(counters Counters) Source {
	return counterSource{counters: counters}
}

// counterSource implements CounterSource.
type counterSource struct {
	counters Counters
}

// namedCounter is a counter with its name in lower case.
type namedCounter struct {
	name  string
	value decimal.Decimal
}

// Match finds the counter with the longest name that token starts with,
// directly or followed by "display". For names of the same length a counter
// wins over the formatted value of another one, e.g. a counter "xdisplay"
// over "x" followed by "display".
func (c counterSource) Match(ctx context.Context, s *Scope, token string) (int, Resolver, error) {
	list, err := s.Memo("counter.list", func() ([]namedCounter, error) {
		counters, err := c.counters.Counters(ctx)
		if err != nil {
			return nil, fmt.Errorf("list counters: %w", err)
		}
		list := make([]namedCounter, len(counters))
		for i, ctr := range counters {
			list[i] = namedCounter{name: strings.ToLower(ctr.Name), value: ctr.Value}
		}
		return list, nil
	})
	if err != nil {
		return 0, nil, err
	}
	best, exact := 0, false
	var resolve Resolver
	for _, ctr := range list {
		rest, ok := strings.CutPrefix(token, ctr.name)
		if !ok {
			continue
		}
		if n := len(ctr.name); n > best || n == best && !exact {
			best, exact, resolve = n, true, constant(NumberValue(ctr.value))
		}
		if strings.HasPrefix(rest, counterDisplaySuffix) {
			if n := len(ctr.name) + len(counterDisplaySuffix); n > best {
				v := NumberValue(ctr.value)
				text, err := display(ctr.value)
				if err != nil {
					return 0, nil, err
				}
				v.Text = text
				best, exact, resolve = n, false, constant(v)
			}
		}
	}
	return best, resolve, nil
}

// displayPlaces are the decimal places of $<name>display for a value that
// is not whole (counters-and-quotes.md, B4).
const displayPlaces = 2

// display formats a counter value for $<name>display (B4; Code-ADR-0020,
// point 10): a whole value with thousands separators, any other also with
// exactly two decimal places, rounded half away from zero.
func display(v decimal.Decimal) (string, error) {
	if v.IsWhole() {
		return groupThousands(v.String()), nil
	}
	text, err := v.Fixed(displayPlaces)
	if err != nil {
		return "", fmt.Errorf("display %s: %w", v, err)
	}
	return groupThousands(text), nil
}

// groupThousands puts commas between groups of three digits of the whole
// part of a number in canonical or fixed form, as in English (USA) until the
// profile has a locale setting (B41).
func groupThousands(number string) string {
	digits, negative := strings.CutPrefix(number, "-")
	whole, fraction, hasFraction := strings.Cut(digits, ".")
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	for i := range len(whole) {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(whole[i])
	}
	if hasFraction {
		b.WriteString("." + fraction)
	}
	return b.String()
}
