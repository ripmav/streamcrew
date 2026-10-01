// SPDX-License-Identifier: Apache-2.0

package template_test

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/template"
)

// TestGlobals_B10_B56: global values apply to every later render, names
// regardless of case and by the longest prefix; values of the run rank
// before them, counters after them.
func TestGlobals_B10_B56(t *testing.T) {
	t.Parallel()
	globals := template.NewGlobals()
	counters := fakeCounters{calls: new(atomic.Int64), list: []counter.Counter{{Name: "score", Value: 3}, {Name: "scorexy", Value: 4}}}
	e := template.New(newRegistry(t), template.WithSources(globals, template.CounterSource(counters)))

	assert.Equal(t, "$top 3", render(t, e, "$top $score", new(scope())), "no global values yet")
	require.NoError(t, globals.Set("Top", template.TextValue("Alice")))
	require.NoError(t, globals.Set("score", template.IntValue(10)))
	require.NoError(t, globals.Set("scorex", template.TextValue("long")))
	assert.Equal(t, "Alice 10 long longz 4", render(t, e, "$TOP $score $scorex $scorexz $scorexy", new(scope())))

	local := scope()
	local.SetValue("score", template.TextValue("run"))
	assert.Equal(t, "run", render(t, e, "$score", &local), "a value of the run hides a global one")

	require.NoError(t, globals.Set("top", template.TextValue("Bob")))
	assert.Equal(t, "Bob", render(t, e, "$top", new(scope())), "the later value wins")
	assert.Equal(t, 3, globals.Len())
}

// TestGlobals_Limit_B57: a new name beyond MaxGlobals fails, existing names
// can still be changed.
func TestGlobals_Limit_B57(t *testing.T) {
	t.Parallel()
	globals := template.NewGlobals()
	for i := range template.MaxGlobals {
		require.NoError(t, globals.Set("v"+strconv.Itoa(i), template.IntValue(int64(i))))
	}
	require.ErrorIs(t, globals.Set("new", template.TextValue("x")), template.ErrTooManyGlobals)
	require.NoError(t, globals.Set("V0", template.TextValue("changed")))
	assert.Equal(t, template.MaxGlobals, globals.Len())

	e := template.New(nil, template.WithSources(globals))
	assert.Equal(t, "changed $new", render(t, e, "$v0 $new", new(scope())))
}

// TestGlobals_Concurrent_B211: instances set and read global values at the
// same time; afterwards the value set last applies.
func TestGlobals_Concurrent_B211(t *testing.T) {
	t.Parallel()
	globals := template.NewGlobals()
	e := template.New(nil, template.WithSources(globals))
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 100 {
				assert.NoError(t, globals.Set("last", template.IntValue(int64(w*100+i))))
				_ = render(t, e, "$last", new(scope()))
			}
		})
	}
	wg.Wait()
	require.NoError(t, globals.Set("last", template.TextValue("final")))
	assert.Equal(t, "final", render(t, e, "$last", new(scope())))
}

func TestFormatSpan_B42(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Europe/Berlin")
	require.NoError(t, err)
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, loc) }
	assert.Equal(t, "1 Year, 9 Months", template.FormatSpan(day(2025, 1, 1), day(2026, 10, 1), loc))
	assert.Equal(t, "1 Month, 1 Day", template.FormatSpan(day(2026, 1, 31), day(2026, 3, 1), loc))
	assert.Equal(t, "0 Days", template.FormatSpan(day(2026, 10, 1), day(2026, 10, 1), loc))
	assert.Equal(t, "0 Days", template.FormatSpan(day(2026, 10, 2), day(2026, 10, 1), loc), "from after to")
}
