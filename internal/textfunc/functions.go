// SPDX-License-Identifier: MIT

package textfunc

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ripmav/streamcrew/internal/template"
)

// [Interop] The function names in this file follow the original (spec
// actions.md, B52) and may be replaced after the legal assessment (roadmap
// Gate O, O.1).

// layoutDate is the form of the dates of datefrom and dateto (B54).
const layoutDate = "2006-01-02"

// function is a text function.
type function struct {
	// params is the number of parameters.
	params int
	// apply returns the result for the rendered parameters.
	apply func(args []string, now time.Time, loc *time.Location) (string, error)
	// check checks parameter i if it has neither identifiers nor calls, so
	// that saving rejects it (B55); nil if there is nothing to check.
	check func(i int, text string) error
}

// functions returns the functions by name (B54).
func functions() map[string]function {
	return map[string]function{
		"tolower":      unary(strings.ToLower),
		"toupper":      unary(strings.ToUpper),
		"removespaces": unary(removeSpaces),
		"removecommas": unary(func(s string) string { return strings.ReplaceAll(s, ",", "") }),
		"length":       unary(func(s string) string { return strconv.Itoa(utf8.RuneCountInString(s)) }),
		"count": {
			params: 2,
			apply: func(args []string, _ time.Time, _ *time.Location) (string, error) {
				re, err := pattern(args[1])
				if err != nil {
					return "", err
				}
				return strconv.Itoa(len(re.FindAllStringIndex(args[0], -1))), nil
			},
			check: func(i int, text string) error {
				if i != 1 {
					return nil
				}
				_, err := pattern(text)
				return err
			},
		},
		"replace": {
			params: 3,
			apply: func(args []string, _ time.Time, _ *time.Location) (string, error) {
				return replace(args[0], args[1], args[2])
			},
		},
		"urlencode": unary(url.QueryEscape),
		"uriescape": unary(func(s string) string {
			// QueryEscape writes a space as "+" and a "+" as "%2B", so every
			// remaining "+" was a space.
			return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
		}),
		"datefrom": date(func(d, today time.Time, loc *time.Location) (string, error) {
			if d.After(today) {
				return "", fmt.Errorf("%s is in the future", d.Format(layoutDate))
			}
			return template.FormatSpan(d, today, loc), nil
		}),
		"dateto": date(func(d, today time.Time, loc *time.Location) (string, error) {
			if d.Before(today) {
				return "", fmt.Errorf("%s is in the past", d.Format(layoutDate))
			}
			return template.FormatSpan(today, d, loc), nil
		}),
	}
}

// unary returns a function of one parameter that cannot fail.
func unary(fn func(string) string) function {
	return function{
		params: 1,
		apply: func(args []string, _ time.Time, _ *time.Location) (string, error) {
			return fn(args[0]), nil
		},
	}
}

// removeSpaces removes all white space, also tabs, line breaks and
// non-breaking spaces (B54).
func removeSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// pattern compiles the pattern of count, a regular expression in RE2
// syntax.
func pattern(text string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(text)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	return re, nil
}

// errTooLong is returned by replace for a result beyond MaxResult.
var errTooLong = errors.New("result too long")

// replace replaces every occurrence of search in text; an empty search
// leaves the text as it is (B54). It computes the length first, so that a
// result beyond MaxResult is never built.
func replace(text, search, replacement string) (string, error) {
	if search == "" {
		return text, nil
	}
	n := strings.Count(text, search)
	if size := len(text) + n*(len(replacement)-len(search)); size > MaxResult {
		return "", fmt.Errorf("%w: %d bytes, at most %d", errTooLong, size, MaxResult)
	}
	return strings.ReplaceAll(text, search, replacement), nil
}

// date returns a function of one date in the form YYYY-MM-DD, which fn
// compares with today, both as dates in the time zone of the profile.
func date(fn func(d, today time.Time, loc *time.Location) (string, error)) function {
	return function{
		params: 1,
		apply: func(args []string, now time.Time, loc *time.Location) (string, error) {
			d, err := parseDate(args[0], loc)
			if err != nil {
				return "", err
			}
			y, m, day := now.In(loc).Date()
			return fn(d, time.Date(y, m, day, 0, 0, 0, 0, loc), loc)
		},
		check: func(_ int, text string) error {
			_, err := parseDate(text, time.UTC)
			return err
		},
	}
}

// parseDate reads a date in the form YYYY-MM-DD as midnight in loc.
func parseDate(text string, loc *time.Location) (time.Time, error) {
	d, err := time.ParseInLocation(layoutDate, text, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: want YYYY-MM-DD", text)
	}
	return d, nil
}
