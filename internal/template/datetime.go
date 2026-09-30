// SPDX-License-Identifier: Apache-2.0

package template

import (
	"context"
	"fmt"
	"time"
)

// [Interop] The identifier names in this file follow the original (spec
// template.md, purpose and scope) and may be replaced after the legal
// assessment (roadmap Gate O, O.1).

// The formats of the original, English (USA), until the profile has a locale
// setting (B41, roadmap 3.2).
const (
	layoutDate     = "1/2/2006"
	layoutTime     = "3:04 PM"
	layoutDateTime = layoutDate + " " + layoutTime
)

// DateTimeFamily returns the identifiers of the current date and time in the
// time zone of the profile (B40, B41): $datetime, $date, $dateyear,
// $datemonth, $datemonthname, $dateday, $dayoftheweek, $time, $timehour
// (00 to 23), $timeminute, $timesecond and $timedigits (hours and minutes as
// in 1345). All of them use the same instant within a render.
func DateTimeFamily() Family {
	text := func(format func(time.Time) string) Resolver {
		return func(_ context.Context, s *Scope) (Value, bool, error) {
			return TextValue(format(s.now())), true, nil
		}
	}
	layout := func(layout string) Resolver {
		return text(func(t time.Time) string { return t.Format(layout) })
	}
	digits := func(width int, part func(time.Time) int) Resolver {
		return func(_ context.Context, s *Scope) (Value, bool, error) {
			return paddedValue(part(s.now()), width), true, nil
		}
	}
	return Family{
		Name: "datetime",
		Identifiers: []Identifier{
			{Name: "datetime", Resolve: layout(layoutDateTime)},
			{Name: "date", Resolve: layout(layoutDate)},
			{Name: "dateyear", Resolve: digits(4, time.Time.Year)},
			{Name: "datemonth", Resolve: digits(2, func(t time.Time) int { return int(t.Month()) })},
			{Name: "datemonthname", Resolve: text(func(t time.Time) string { return t.Month().String() })},
			{Name: "dateday", Resolve: digits(2, time.Time.Day)},
			{Name: "dayoftheweek", Resolve: text(func(t time.Time) string { return t.Weekday().String() })},
			{Name: "time", Resolve: layout(layoutTime)},
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
	return Value{Text: fmt.Sprintf("%0*d", width, n), Number: float64(n), IsNumber: true}
}

// now returns the current time in the time zone of the profile, the same
// instant for the whole render.
func (s *Scope) now() time.Time {
	t, _ := s.Memo("datetime.now", func() (time.Time, error) {
		return time.Now(), nil
	})
	return t.In(s.location())
}

// location returns the time zone of the profile.
func (s *Scope) location() *time.Location {
	if s.Location == nil {
		return time.UTC
	}
	return s.Location
}
