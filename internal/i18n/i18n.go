// SPDX-License-Identifier: Apache-2.0

// Package i18n renders the texts of the core in the language of the profile
// (ADR-0022). Texts are ICU MessageFormat v1 messages in a subset of it; the
// catalogs of English and German are embedded JSON files, which the
// frontends read as well. A message is a value of a key and named values;
// it becomes text where it leaves the core, e.g. as a chat message of the
// bot. Logs and the command line stay in English and do not use it.
package i18n

import (
	"embed"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/language"
)

var (
	// ErrInvalidCatalog is returned by New and Load for a catalog that is
	// not complete or not valid.
	ErrInvalidCatalog = errors.New("invalid message catalog")
	// ErrUnknownLanguage is returned for a language without a catalog.
	ErrUnknownLanguage = errors.New("language without a catalog")
	// ErrUnknownKey is returned for a key that no catalog has.
	ErrUnknownKey = errors.New("unknown message key")
	// ErrMissingValue is returned when a message needs a value that is not
	// given.
	ErrMissingValue = errors.New("missing message value")
	// ErrValueType is returned for a value of the wrong kind, e.g. a text
	// for a plural.
	ErrValueType = errors.New("message value of the wrong kind")
)

// Language is a language with a catalog (ADR-0022, point 1).
type Language string

// The languages with a catalog.
const (
	// English is the source language.
	English Language = "en"
	// German is a translation.
	German Language = "de"
)

// Languages returns the languages with a catalog, English first.
func Languages() []Language {
	return []Language{English, German}
}

// Valid reports whether l has a catalog.
func (l Language) Valid() bool {
	return slices.Contains(Languages(), l)
}

// tag returns the language tag of l.
func (l Language) tag() language.Tag {
	switch l {
	case German:
		return language.German
	default:
		return language.English
	}
}

// Key names a message, e.g. "requirement.cooldown.user" (ADR-0022, point
// 2): parts of lowercase letters, digits and "_", separated by dots. The
// keys are the constants in keys.go.
type Key string

// validKey reports whether k has the form of a key.
func validKey(k Key) bool {
	parts := strings.SplitSeq(string(k), ".")
	for part := range parts {
		if part == "" || strings.ContainsFunc(part, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_'
		}) {
			return false
		}
	}
	return true
}

// valueKind says what a Value holds.
type valueKind int

// The kinds of values.
const (
	kindNone valueKind = iota
	kindText
	kindInteger
	kindDecimal
	kindDuration
)

// Value is a value of a message (ADR-0022, point 6): a text, a number or a
// duration. The zero value is no value; rendering it is an error.
type Value struct {
	kind     valueKind
	text     string
	integer  int64
	decimal  float64
	duration time.Duration
}

// Text returns a text value. It fits {name} and select.
func Text(s string) Value {
	return Value{kind: kindText, text: s}
}

// Int returns a whole number. It fits {name}, number, plural and
// selectordinal.
func Int(n int64) Value {
	return Value{kind: kindInteger, integer: n}
}

// Decimal returns a number that may have a fraction. It fits where Int
// fits; it must be finite.
func Decimal(f float64) Value {
	return Value{kind: kindDecimal, decimal: f}
}

// Duration returns a duration, written in whole seconds, rounded up, and
// the larger units, e.g. "2 minutes 5 seconds". It fits {name} and must not
// be negative.
func Duration(d time.Duration) Value {
	return Value{kind: kindDuration, duration: d}
}

// Message is a message with its values (ADR-0022, point 5).
type Message struct {
	Key  Key
	Args map[string]Value
}

// locales has the catalogs, one file per language.
//
//go:embed locales/*.json
var locales embed.FS

// Catalog holds the parsed messages of every language.
type Catalog struct {
	messages map[Language]map[Key]message
}

// Load returns the catalog of the embedded files locales/<language>.json.
func Load() (*Catalog, error) {
	sources, err := embedded()
	if err != nil {
		return nil, err
	}
	return New(sources)
}

// embedded returns the messages of the embedded files by language and key.
// encoding/json/v2 rejects a key that a file has twice.
func embedded() (map[Language]map[Key]string, error) {
	sources := make(map[Language]map[Key]string, len(Languages()))
	for _, lang := range Languages() {
		name := "locales/" + string(lang) + ".json"
		data, err := locales.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidCatalog, err)
		}
		var messages map[Key]string
		if err := json.Unmarshal(data, &messages); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidCatalog, name, err)
		}
		sources[lang] = messages
	}
	return sources, nil
}

// New returns a catalog of the messages in sources, by language and key
// (ADR-0022, point 8). Every language of Languages must be there with the
// same keys. Each message must be valid ICU in the subset of ADR-0022 and
// have every plural form its language needs, and it must use the same
// values in every language, each as the same kind: text, number or
// choice.
func New(sources map[Language]map[Key]string) (*Catalog, error) {
	c := &Catalog{messages: make(map[Language]map[Key]message, len(sources))}
	var errs []error
	for lang := range sources {
		if !lang.Valid() {
			errs = append(errs, fmt.Errorf("%w %q", ErrUnknownLanguage, lang))
		}
	}
	for _, lang := range Languages() {
		source, ok := sources[lang]
		if !ok {
			errs = append(errs, fmt.Errorf("no messages for %s", lang))
			continue
		}
		messages := make(map[Key]message, len(source))
		for _, key := range slices.Sorted(maps.Keys(source)) {
			m, err := compile(lang, key, source[key])
			if err != nil {
				errs = append(errs, err)
				continue
			}
			messages[key] = m
		}
		c.messages[lang] = messages
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCatalog, err)
	}
	if err := c.compare(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCatalog, err)
	}
	return c, nil
}

// compile parses the message of key in lang and checks it.
func compile(lang Language, key Key, src string) (message, error) {
	if !validKey(key) {
		return nil, fmt.Errorf("%s: invalid key %q", lang, key)
	}
	m, err := parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", lang, key, err)
	}
	if err := checkForms(lang, m); err != nil {
		return nil, fmt.Errorf("%s %s: %w", lang, key, err)
	}
	if _, err := argClasses(m); err != nil {
		return nil, fmt.Errorf("%s %s: %w", lang, key, err)
	}
	return m, nil
}

// compare checks that every language has the keys of English, and that
// each message uses the same values as in English.
func (c *Catalog) compare() error {
	english := c.messages[English]
	var errs []error
	for _, lang := range Languages()[1:] {
		messages := c.messages[lang]
		for _, key := range slices.Sorted(maps.Keys(english)) {
			m, ok := messages[key]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: no message %s", lang, key))
				continue
			}
			if err := sameArgs(english[key], m); err != nil {
				errs = append(errs, fmt.Errorf("%s %s: %w", lang, key, err))
			}
		}
		for _, key := range slices.Sorted(maps.Keys(messages)) {
			if _, ok := english[key]; !ok {
				errs = append(errs, fmt.Errorf("%s: message %s that English does not have", lang, key))
			}
		}
	}
	return errors.Join(errs...)
}

// Keys returns the keys of the catalog, sorted.
func (c *Catalog) Keys() []Key {
	return slices.Sorted(maps.Keys(c.messages[English]))
}

// Render returns m in lang. A missing or unfitting value is an error, and
// the message is not to be sent (ADR-0022, point 9).
func (c *Catalog) Render(lang Language, m Message) (string, error) {
	if !lang.Valid() {
		return "", fmt.Errorf("%w %q", ErrUnknownLanguage, lang)
	}
	msg, ok := c.messages[lang][m.Key]
	if !ok {
		return "", fmt.Errorf("%w %q", ErrUnknownKey, m.Key)
	}
	r := renderer{catalog: c, lang: lang, args: m.Args}
	var b strings.Builder
	if err := r.message(&b, msg, nil); err != nil {
		return "", fmt.Errorf("render %s in %s: %w", m.Key, lang, err)
	}
	return b.String(), nil
}
