// SPDX-License-Identifier: MIT

package template

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
// value of the counter, $<name>display the value with thousands separators
// (spec counters-and-quotes.md, B1, B4). Names are case-insensitive. The
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
	value int64
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
			best, exact, resolve = n, true, constant(IntValue(ctr.value))
		}
		if strings.HasPrefix(rest, counterDisplaySuffix) {
			if n := len(ctr.name) + len(counterDisplaySuffix); n > best {
				v := IntValue(ctr.value)
				v.Text = groupThousands(ctr.value)
				best, exact, resolve = n, false, constant(v)
			}
		}
	}
	return best, resolve, nil
}

// groupThousands formats n with commas between groups of three digits, as in
// English (USA) until the profile has a locale setting (B41).
func groupThousands(n int64) string {
	digits, negative := strings.CutPrefix(strconv.FormatInt(n, 10), "-")
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	for i := range len(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(digits[i])
	}
	return b.String()
}
