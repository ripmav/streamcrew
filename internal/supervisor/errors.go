// SPDX-License-Identifier: MIT

package supervisor

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Permanent marks err as permanent: a restart would not fix it, e.g. a port
// that is in use or rejected credentials. The supervisor does not restart a
// runnable that returns a permanent error. Permanent(nil) is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err or an error it wraps was marked with
// Permanent.
func IsPermanent(err error) bool {
	_, ok := errors.AsType[*permanentError](err)
	return ok
}

type permanentError struct {
	err error
}

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// PanicError is the error of a runnable that panicked.
type PanicError struct {
	Value any    // the value passed to panic
	Stack []byte // the stack trace of the panicking goroutine
}

// Error implements error.
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Value)
}

// jitter returns a random duration between d/2 and d. The random numbers come
// from crypto/rand; math/rand is flagged by gosec (G404, Code-ADR-0004).
func jitter(d time.Duration) time.Duration {
	half := d / 2
	if half <= 0 {
		return d
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(half)+1))
	if err != nil {
		return d
	}
	return d - half + time.Duration(n.Int64())
}
