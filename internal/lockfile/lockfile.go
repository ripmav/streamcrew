// SPDX-License-Identifier: Apache-2.0

// Package lockfile holds an exclusive operating-system lock on a file for
// the lifetime of a process (ADR-0012): flock on Unix, LockFileEx on
// Windows. The system releases the lock when the process ends, even after a
// crash, so there are no stale locks.
//
// The file records the PID and start time of the holder, so that a second
// process can name it in its error message.
package lockfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LockedError is returned when another process holds the lock.
type LockedError struct {
	Path  string
	PID   int       // 0 if unknown
	Since time.Time // zero if unknown
}

// Error implements error.
func (e *LockedError) Error() string {
	msg := "locked by another process: " + e.Path
	if e.PID != 0 {
		msg += fmt.Sprintf(" (PID %d", e.PID)
		if !e.Since.IsZero() {
			msg += ", since " + e.Since.Format(time.RFC3339)
		}
		msg += ")"
	}
	return msg
}

// Lock is a held lock. Release it when done.
type Lock struct {
	f *os.File
}

// Acquire takes the lock on path, creating the file and its directory. It
// does not wait: if another process holds the lock, it returns a
// *LockedError.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	locked, err := tryLock(f)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("lock %s: %w", path, err), f.Close())
	}
	if !locked {
		holder := readHolder(f)
		holder.Path = path
		return nil, errors.Join(&holder, f.Close())
	}
	if err := writeHolder(f); err != nil {
		return nil, errors.Join(err, unlock(f), f.Close())
	}
	return &Lock{f: f}, nil
}

// Release gives up the lock. The file stays; removing it could race with
// another process that is taking the lock.
func (l *Lock) Release() error {
	if l.f == nil {
		return nil
	}
	err := errors.Join(unlock(l.f), l.f.Close())
	l.f = nil
	return err
}

func writeHolder(f *os.File) error {
	content := fmt.Sprintf("pid=%d\nsince=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("write lock file: %w", err)
	}
	if _, err := f.WriteAt([]byte(content), 0); err != nil {
		return fmt.Errorf("write lock file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("write lock file: %w", err)
	}
	return nil
}

// readHolder reads what the holder wrote; unreadable content yields an
// error without details.
func readHolder(f *os.File) LockedError {
	var e LockedError
	buf := make([]byte, 256)
	n, _ := f.ReadAt(buf, 0)
	for line := range strings.Lines(string(buf[:n])) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch key {
		case "pid":
			e.PID, _ = strconv.Atoi(value)
		case "since":
			e.Since, _ = time.Parse(time.RFC3339, value)
		}
	}
	return e
}
