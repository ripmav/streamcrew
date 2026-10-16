// SPDX-License-Identifier: MIT

package moderation

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/ripmav/streamcrew/internal/settings"
)

// violates reports whether the message (text with its emotes) violates the
// filter f (spec moderation.md, B4 to B7). A threshold filter is off with a
// threshold of 0 (B7).
func violates(f settings.Filter, text string, emotes []string) bool {
	switch f.Kind {
	case settings.FilterWord:
		return wordViolates(text, f.Word)
	case settings.FilterLink:
		return linkViolates(text)
	case settings.FilterUppercase:
		return uppercaseViolates(text, f.Threshold, f.Percent)
	case settings.FilterPunctuation:
		return punctuationViolates(text, f.Threshold, f.Percent)
	case settings.FilterEmote:
		return emoteViolates(text, emotes, f.Threshold, f.Percent)
	default:
		return false
	}
}

// wordViolates reports whether a word of the text matches the pattern p,
// which is without case and may use `*` as a wildcard for any run of
// characters (spec moderation.md, B5).
func wordViolates(text, pattern string) bool {
	re, ok := wildcardRegex(pattern)
	if !ok {
		return false
	}
	for _, w := range strings.Fields(text) {
		if re.MatchString(w) {
			return true
		}
	}
	return false
}

// wildcardRegex compiles the case-insensitive match of p, a word pattern
// whose `*` is a wildcard. It reports ok=false for a pattern that does not
// compile.
func wildcardRegex(pattern string) (*regexp.Regexp, bool) {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range pattern {
		if r == '*' {
			b.WriteString(".*")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return re, err == nil
}

// linkViolates reports whether the text names a URL: an http(s) link or a
// bare domain with a dot (spec moderation.md, B6).
func linkViolates(text string) bool { return linkRe.MatchString(text) }

var linkRe = regexp.MustCompile(
	`(?i)(https?://\S+|\b[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*\.[a-z]{2,}\b)`)

// uppercaseViolates checks the amount of uppercase (spec moderation.md,
// B7): a count of the fully uppercase words, or a percentage of the
// letters. A threshold of 0 leaves the filter off.
func uppercaseViolates(text string, threshold int, percent bool) bool {
	if threshold == 0 {
		return false
	}
	if percent {
		upper, total := uppercaseLetters(text)
		return total > 0 && upper*100/total >= threshold
	}
	return allUpperWordCount(text) >= threshold
}

// punctuationViolates checks the amount of punctuation (spec
// moderation.md, B7): a count of the characters, or a percentage of the
// letters. A threshold of 0 leaves the filter off.
func punctuationViolates(text string, threshold int, percent bool) bool {
	if threshold == 0 {
		return false
	}
	punct, total := punctuationLetters(text)
	if percent {
		return total > 0 && punct*100/total >= threshold
	}
	return punct >= threshold
}

// emoteViolates checks the amount of emotes (spec moderation.md, B7): a
// count, or a percentage of the words. A threshold of 0 leaves the filter
// off.
func emoteViolates(text string, emotes []string, threshold int, percent bool) bool {
	if threshold == 0 {
		return false
	}
	if percent {
		words := wordCount(text)
		return words > 0 && len(emotes)*100/words >= threshold
	}
	return len(emotes) >= threshold
}

// allUpperWordCount counts the words that are fully uppercase (at least
// two letters).
func allUpperWordCount(text string) int {
	n := 0
	for _, w := range strings.Fields(text) {
		l := letters(w)
		if len(l) < 2 {
			continue
		}
		allUpper := true
		for _, c := range l {
			if !unicode.IsUpper(c) {
				allUpper = false
				break
			}
		}
		if allUpper {
			n++
		}
	}
	return n
}

// uppercaseLetters counts the uppercase letters and all letters.
func uppercaseLetters(text string) (upper, total int) {
	for _, r := range text {
		if unicode.IsLetter(r) {
			total++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	return upper, total
}

// punctuationLetters counts the punctuation characters and all letters.
func punctuationLetters(text string) (punct, total int) {
	for _, r := range text {
		switch {
		case unicode.IsPunct(r):
			punct++
		case unicode.IsLetter(r):
			total++
		}
	}
	return punct, total
}

// wordCount counts the words that have at least one letter.
func wordCount(text string) int {
	n := 0
	for _, w := range strings.Fields(text) {
		if letters(w) != "" {
			n++
		}
	}
	return n
}

// letters returns the letters of the word.
func letters(word string) string {
	var b strings.Builder
	for _, r := range word {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
