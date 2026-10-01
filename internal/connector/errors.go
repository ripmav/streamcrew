// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"strings"

	"github.com/ripmav/streamcrew/internal/domain/platform"
)

// OpError is the error of an operation that failed on some of the
// platforms it ran on, e.g. a chat message that did not reach every
// platform (actions.md B66, B86). Its message names the platforms, and
// errors.Is and errors.As see the error of each one.
type OpError struct {
	// Op says what failed, e.g. "not sent".
	Op string
	// Failures are the platforms it failed on, in order, with their
	// errors; at least one.
	Failures []Failure
}

// Failure is the error of an operation on one platform.
type Failure struct {
	Platform platform.Name
	Err      error
}

// JoinErrors returns the errors of the operation op on ps, where errs[i]
// is the error on ps[i], as an *OpError that names the platforms with an
// error; nil if there is none.
func JoinErrors(op string, ps []Platform, errs []error) error {
	e := &OpError{Op: op}
	for i, err := range errs {
		if err != nil {
			e.Failures = append(e.Failures, Failure{Platform: ps[i].Name(), Err: err})
		}
	}
	if len(e.Failures) == 0 {
		return nil
	}
	return e
}

// Error returns, e.g., "not sent on twitch, kick: twitch: …; kick: …".
func (e *OpError) Error() string {
	names := make([]string, len(e.Failures))
	reasons := make([]string, len(e.Failures))
	for i, f := range e.Failures {
		names[i] = string(f.Platform)
		reasons[i] = string(f.Platform) + ": " + f.Err.Error()
	}
	return e.Op + " on " + strings.Join(names, ", ") + ": " + strings.Join(reasons, "; ")
}

// Unwrap returns the error of each platform.
func (e *OpError) Unwrap() []error {
	errs := make([]error, len(e.Failures))
	for i, f := range e.Failures {
		errs[i] = f.Err
	}
	return errs
}
