// SPDX-License-Identifier: MIT

//go:build windows

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// The locked byte range lies far beyond the content, so that a second
// process can still read who holds the lock.
const (
	lockOffset = 1 << 30
	lockLength = 1
)

func overlapped() *windows.Overlapped {
	return &windows.Overlapped{Offset: lockOffset}
}

// tryLock takes an exclusive byte-range lock without waiting.
func tryLock(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, lockLength, 0, overlapped())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockLength, 0, overlapped())
}
