// SPDX-License-Identifier: Apache-2.0

package template

import (
	"strings"
)

// [Interop] The locale names in this file follow the regional setting names
// of Windows, which the original reads (spec template.md, B41, A7), and may
// be replaced after the legal assessment (roadmap Gate O, O.1).

// Locale is a locale the core knows the formats of: the short date and time
// formats of the regional setting of Windows for it (B41, A7).
type Locale string

// The locales of the core, in the order of the list of B41.
const (
	LocaleUSEnglish Locale = "en-US"
	LocaleGBEnglish Locale = "en-GB"
	LocaleDEGerman  Locale = "de-DE"
	LocaleATGerman  Locale = "de-AT"
	LocaleCHGerman  Locale = "de-CH"
)

// Locales returns the locales the core knows, in the order of the list of
// B41; the first of a language is the fallback for it.
func Locales() []Locale {
	return []Locale{LocaleUSEnglish, LocaleGBEnglish, LocaleDEGerman, LocaleATGerman, LocaleCHGerman}
}

// KnownLocale reports whether name is one of the locales of the core,
// regardless of case and of underscores as hyphens (B41).
func KnownLocale(name string) bool {
	_, ok := canonicalLocale(name)
	return ok
}

// ResolveLocale returns the locale that name stands for (B41): a name of the
// list as is, an environment locale, such as "de_DE" or "de", as the first
// locale of its language, and every other name as en-US. It reports whether
// name was one of the list, so the caller can warn about an environment
// locale outside of it.
func ResolveLocale(name string) (Locale, bool) {
	if l, ok := canonicalLocale(name); ok {
		return l, true
	}
	lang, _, _ := strings.Cut(normalizeLocaleName(name), "-")
	for _, l := range Locales() {
		if strings.HasPrefix(normalizeLocaleName(string(l)), lang) {
			return l, false
		}
	}
	return LocaleUSEnglish, false
}

// normalizeLocaleName makes a locale name comparable: lowercase, with
// underscores as hyphens, as the environment writes it.
func normalizeLocaleName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

// canonicalLocale returns the locale name stands for if it is one of the
// list, regardless of case and of underscores as hyphens.
func canonicalLocale(name string) (Locale, bool) {
	n := normalizeLocaleName(name)
	for _, l := range Locales() {
		if normalizeLocaleName(string(l)) == n {
			return l, true
		}
	}
	return "", false
}

// localePattern is the formats of one locale: the short date and time formats
// of the regional setting of Windows (A7) as Go layouts, and the names of
// the months and weekdays in the language of the locale.
type localePattern struct {
	date     string
	time     string
	datetime string
	months   []string // index 0 is January
	weekdays []string // index 0 is Sunday
}

// patternFor returns the formats of the locale, en-US for a locale the core
// does not know, such as the zero value (B41).
func patternFor(l Locale) localePattern {
	var date string
	var time string
	var months, weekdays []string
	switch l {
	case LocaleGBEnglish:
		date, time = "02/01/2006", "15:04"
		months, weekdays = englishMonths(), englishWeekdays()
	case LocaleDEGerman, LocaleATGerman, LocaleCHGerman:
		date, time = "02.01.2006", "15:04"
		months, weekdays = germanMonths(), germanWeekdays()
	default:
		date, time = "1/2/2006", "3:04 PM"
		months, weekdays = englishMonths(), englishWeekdays()
	}
	return localePattern{
		date:     date,
		time:     time,
		datetime: date + " " + time,
		months:   months,
		weekdays: weekdays,
	}
}

// pattern returns the formats of the locale of the scope, once per render.
func (s *Scope) pattern() localePattern {
	p, _ := s.Memo("datetime.pattern", func() (localePattern, error) {
		return patternFor(s.Locale), nil
	})
	return p
}

// englishMonths and germanMonths are the month names, January first.
func englishMonths() []string {
	return []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
}

func germanMonths() []string {
	return []string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}
}

// englishWeekdays and germanWeekdays are the weekday names, Sunday first, as
// time.Weekday and the DayOfWeek of the original number them.
func englishWeekdays() []string {
	return []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
}

func germanWeekdays() []string {
	return []string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}
}
