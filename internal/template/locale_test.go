// SPDX-License-Identifier: Apache-2.0

package template_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/template"
)

// TestResolveLocale_B41: a name of the list stays as is, regardless of case;
// an environment locale takes the first locale of its language; anything
// else takes en-US. The second result reports whether the name was one of
// the list.
func TestResolveLocale_B41(t *testing.T) {
	tests := []struct {
		name      string
		want      template.Locale
		wantKnown bool
	}{
		{name: "en-US", want: template.LocaleUSEnglish, wantKnown: true},
		{name: "en-GB", want: template.LocaleGBEnglish, wantKnown: true},
		{name: "de-DE", want: template.LocaleDEGerman, wantKnown: true},
		{name: "de-AT", want: template.LocaleATGerman, wantKnown: true},
		{name: "de-CH", want: template.LocaleCHGerman, wantKnown: true},
		{name: "DE-de", want: template.LocaleDEGerman, wantKnown: true},
		{name: "de_DE", want: template.LocaleDEGerman, wantKnown: true},
		{name: "en_GB", want: template.LocaleGBEnglish, wantKnown: true},
		{name: "de", want: template.LocaleDEGerman, wantKnown: false},
		{name: "en", want: template.LocaleUSEnglish, wantKnown: false},
		{name: "fr-FR", want: template.LocaleUSEnglish, wantKnown: false},
		{name: "xx-YY", want: template.LocaleUSEnglish, wantKnown: false},
		{name: "", want: template.LocaleUSEnglish, wantKnown: false},
	}
	for _, tc := range tests {
		l, known := template.ResolveLocale(tc.name)
		assert.Equal(t, tc.want, l, tc.name)
		assert.Equal(t, tc.wantKnown, known, tc.name)
	}
}

// TestKnownLocale_B41: the list of B41 is known, regardless of case.
func TestKnownLocale_B41(t *testing.T) {
	for _, name := range []string{"en-US", "en-GB", "de-DE", "de-AT", "de-CH", "de-de", "EN-us", "de_DE"} {
		assert.True(t, template.KnownLocale(name), name)
	}
	for _, name := range []string{"fr-FR", "de", "system", ""} {
		assert.False(t, template.KnownLocale(name), name)
	}
}

// TestLocales_B41: the list the core knows, in the order of B41.
func TestLocales_B41(t *testing.T) {
	assert.Equal(t, []template.Locale{
		template.LocaleUSEnglish, template.LocaleGBEnglish, template.LocaleDEGerman,
		template.LocaleATGerman, template.LocaleCHGerman,
	}, template.Locales())
}

// TestDateTimeFamily_Locales_B41: the formats of date, time, month and
// weekday names follow the locale of the scope; the zero value and a locale
// the core does not know take en-US.
func TestDateTimeFamily_Locales_B41(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sleepUntil(time.Date(2009, time.June, 15, 17, 45, 20, 0, time.UTC))
		e := template.New(mvpRegistry(t, nil))
		text := "$datetime|$date|$time|$datemonthname|$dayoftheweek"
		for locale, want := range map[template.Locale]string{
			"":                       "6/15/2009 5:45 PM|6/15/2009|5:45 PM|June|Monday",
			template.LocaleUSEnglish: "6/15/2009 5:45 PM|6/15/2009|5:45 PM|June|Monday",
			template.LocaleGBEnglish: "15/06/2009 17:45|15/06/2009|17:45|June|Monday",
			template.LocaleDEGerman:  "15.06.2009 17:45|15.06.2009|17:45|Juni|Montag",
			template.LocaleATGerman:  "15.06.2009 17:45|15.06.2009|17:45|Juni|Montag",
			template.LocaleCHGerman:  "15.06.2009 17:45|15.06.2009|17:45|Juni|Montag",
			"fr-FR":                  "6/15/2009 5:45 PM|6/15/2009|5:45 PM|June|Monday",
		} {
			s := scope()
			s.Locale = locale
			assert.Equal(t, want, render(t, e, text, &s), "locale "+string(locale))
		}

		// Single-digit day and month: en-US has no zero padding, the others
		// do (B41, A7).
		sleepUntil(time.Date(2009, time.July, 5, 17, 45, 20, 0, time.UTC))
		for locale, want := range map[template.Locale]string{
			"":                       "7/5/2009 5:45 PM|7/5/2009|5:45 PM|July|Sunday",
			template.LocaleUSEnglish: "7/5/2009 5:45 PM|7/5/2009|5:45 PM|July|Sunday",
			template.LocaleGBEnglish: "05/07/2009 17:45|05/07/2009|17:45|July|Sunday",
			template.LocaleDEGerman:  "05.07.2009 17:45|05.07.2009|17:45|Juli|Sonntag",
			template.LocaleATGerman:  "05.07.2009 17:45|05.07.2009|17:45|Juli|Sonntag",
			template.LocaleCHGerman:  "05.07.2009 17:45|05.07.2009|17:45|Juli|Sonntag",
		} {
			s := scope()
			s.Locale = locale
			assert.Equal(t, want, render(t, e, text, &s), "locale "+string(locale))
		}

		// Numbers do not follow the locale; they stay canonical (B41, A7).
		for _, locale := range []template.Locale{template.LocaleUSEnglish, template.LocaleDEGerman} {
			s := scope()
			s.Locale = locale
			s.SetValue("zahl", template.IntValue(1234))
			assert.Equal(t, "1234", render(t, e, "$zahl", &s), "locale "+string(locale))
		}
	})
}
