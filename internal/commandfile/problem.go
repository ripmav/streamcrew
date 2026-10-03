// SPDX-License-Identifier: Apache-2.0

package commandfile

import (
	"fmt"
	"strconv"
)

// Severity says whether a problem stops an import (B32).
type Severity string

// The severities.
const (
	// SeverityError: the file cannot be imported.
	SeverityError Severity = "error"
	// SeverityWarning: the file can be imported, but something will not
	// work on this core, e.g. an action that needs a capability it lacks.
	SeverityWarning Severity = "warning"
)

// Problem is an error or a warning about a file (B32).
type Problem struct {
	Severity Severity `json:"severity"`
	File     string   `json:"file"`
	// Line and Column are the place in the file, from 1. A syntax error of
	// YAML has no column, a problem of the whole file no line; then they
	// are 0.
	Line   int `json:"line"`
	Column int `json:"column"`
	// Path is the place in the document, e.g. "spec.actions[1].message",
	// lists counted from 0; empty for the whole document or file.
	Path    string `json:"path"`
	Message string `json:"message"`
}

// String writes the problem as "file:line:column: path: message", without
// what it does not know, e.g. "hug.yaml:12:9: spec.actions[1].message:
// missing member".
func (p Problem) String() string {
	s := p.File
	if p.Line > 0 {
		s += ":" + strconv.Itoa(p.Line)
		if p.Column > 0 {
			s += ":" + strconv.Itoa(p.Column)
		}
	}
	if p.Path != "" {
		s += ": " + p.Path
	}
	if p.Severity == SeverityWarning {
		return fmt.Sprintf("%s: warning: %s", s, p.Message)
	}
	return s + ": " + p.Message
}

// HasErrors reports whether problems has an error, not only warnings.
func HasErrors(problems []Problem) bool {
	for _, p := range problems {
		if p.Severity == SeverityError {
			return true
		}
	}
	return false
}
