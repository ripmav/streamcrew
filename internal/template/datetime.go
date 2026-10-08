// SPDX-License-Identifier: MIT

package template

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// [Interop] The identifier names in this file follow the original (spec
// template.md, purpose and scope) and may be replaced after the legal
// assessment (roadmap Gate O, O.1).

// DateTimeFamily returns the identifiers of the current date and time in the
// time zone of the profile (B40, B41): $datetime, $date, $dateyear,
// $datemonth, $datemonthname, $dateday, $dayoftheweek, $time, $timehour
// (00 to 23), $timeminute, $timesecond and $timedigits (hours and minutes as
// in 1345). All of them use the same instant within a render. The formats of
// date, time, month and weekday names follow the locale of the scope (B41),
// en-US for the zero value.
func DateTimeFamily() Family {
	text := func(format func(time.Time, localePattern) string) Resolver {
		return func(_ context.Context, s *Scope) (Value, bool, error) {
			return TextValue(format(s.now(), s.pattern())), true, nil
		}
	}
	digits := func(width int, part func(time.Time) int) Resolver {
		return func(_ context.Context, s *Scope) (Value, bool, error) {
			return paddedValue(part(s.now()), width), true, nil
		}
	}
	return Family{
		Name: "datetime",
		Identifiers: []Identifier{
			{Name: "datetime", Resolve: text(func(t time.Time, p localePattern) string { return t.Format(p.datetime) })},
			{Name: "date", Resolve: text(func(t time.Time, p localePattern) string { return t.Format(p.date) })},
			{Name: "dateyear", Resolve: digits(4, time.Time.Year)},
			{Name: "datemonth", Resolve: digits(2, func(t time.Time) int { return int(t.Month()) })},
			{Name: "datemonthname", Resolve: text(func(t time.Time, p localePattern) string { return p.months[int(t.Month())-1] })},
			{Name: "dateday", Resolve: digits(2, time.Time.Day)},
			{Name: "dayoftheweek", Resolve: text(func(t time.Time, p localePattern) string { return p.weekdays[int(t.Weekday())] })},
			{Name: "time", Resolve: text(func(t time.Time, p localePattern) string { return t.Format(p.time) })},
			{Name: "timehour", Resolve: digits(2, time.Time.Hour)},
			{Name: "timeminute", Resolve: digits(2, time.Time.Minute)},
			{Name: "timesecond", Resolve: digits(2, time.Time.Second)},
			{Name: "timedigits", Resolve: digits(4, func(t time.Time) int { return t.Hour()*100 + t.Minute() })},
		},
	}
}

// paddedValue returns n as a number whose text has at least width digits,
// e.g. "06" for June.
func paddedValue(n, width int) Value {
	return Value{Text: fmt.Sprintf("%0*d", width, n), Number: decimal.New(int64(n)), IsNumber: true}
}

// now returns the current time in the time zone of the profile, the same
// instant for the whole render.
func (s *Scope) now() time.Time {
	t, _ := s.Memo("datetime.now", func() (time.Time, error) {
		return time.Now(), nil
	})
	return t.In(s.location())
}

// location returns the time zone of the profile, which Render has checked.
func (s *Scope) location() *time.Location {
	return s.Location
}

// span is the time between two instants in calendar units (B42).
type span struct {
	// months are the whole months, years included.
	months int
	// days are the days after the whole months.
	days int
	// totalDays are all days between the two dates.
	totalDays int
}

// spanBetween returns the calendar span from from to to, both taken as dates
// in loc. A month after the 31st ends on the last day of a shorter month, so
// 31 January to 1 March is 1 month and 1 day in a common year. A from after
// to gives the zero span.
func spanBetween(from, to time.Time, loc *time.Location) span {
	a, b := dateOf(from.In(loc)), dateOf(to.In(loc))
	if b.Before(a) {
		return span{}
	}
	months := (b.Year()-a.Year())*12 + int(b.Month()-a.Month())
	if addMonths(a, months).After(b) {
		months--
	}
	return span{
		months:    months,
		days:      daysBetween(addMonths(a, months), b),
		totalDays: daysBetween(a, b),
	}
}

// String formats the span as years, months and days in English, leaving out
// units without value, e.g. "1 Year, 4 Months, 12 Days"; the zero span is
// "0 Days" (B42).
func (s span) String() string {
	var parts []string
	for _, u := range []struct {
		n          int
		one, other string
	}{
		{s.months / 12, "Year", "Years"},
		{s.months % 12, "Month", "Months"},
		{s.days, "Day", "Days"},
	} {
		if u.n > 0 {
			parts = append(parts, plural(u.n, u.one, u.other))
		}
	}
	if parts == nil {
		return "0 Days"
	}
	return strings.Join(parts, ", ")
}

// FormatSpan returns the calendar span from from to to as text, as the
// identifiers of ages write it (B42), e.g. "1 Year, 4 Months, 12 Days";
// both are taken as dates in loc. A from after to gives "0 Days". The text
// functions datefrom and dateto of the special identifier action use it
// (spec actions.md, B54).
func FormatSpan(from, to time.Time, loc *time.Location) string {
	return spanBetween(from, to, loc).String()
}

// plural returns n with the singular or plural unit, e.g. "1 Day", "2 Days".
func plural[T ~int | ~int64](n T, one, other string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.FormatInt(int64(n), 10) + " " + other
}

// dateOf returns the calendar date of t as midnight UTC.
func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// addMonths adds n months to the date t and ends on the last day of the
// target month if it is shorter.
func addMonths(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(t.Day(), last), 0, 0, 0, 0, time.UTC)
}

// daysBetween returns the days from the date a to the date b.
func daysBetween(a, b time.Time) int {
	return int(b.Sub(a) / (24 * time.Hour))
}
