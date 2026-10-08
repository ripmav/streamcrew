// SPDX-License-Identifier: MIT

package logging

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// backupTimeLayout is the timestamp in the names of rotated files. It has a
// fixed width, so that the names sort chronologically.
const backupTimeLayout = "20060102T150405.000000000Z"

// RotatingFile is an io.WriteCloser for a log file. Before a write would make
// the file larger than its maximum size, the file is renamed to
// <name>-<UTC timestamp><ext> and a new one is started. Only the newest
// rotated files are kept.
//
// The directory is created with mode 0700, files with mode 0600. All file
// operations stay inside the directory (os.Root). RotatingFile is safe for
// concurrent use.
type RotatingFile struct {
	mu       sync.Mutex
	root     *os.Root
	name     string
	maxSize  int64
	maxFiles int
	file     *os.File
	size     int64
}

// OpenRotatingFile opens the log file at path for appending, creating it and
// its directory if necessary. maxSize is in bytes; maxFiles is the number of
// rotated files to keep.
func OpenRotatingFile(path string, maxSize int64, maxFiles int) (*RotatingFile, error) {
	if maxSize < 1 {
		return nil, fmt.Errorf("log file max size must be positive, got %d", maxSize)
	}
	dir, name := filepath.Split(path)
	if name == "" {
		return nil, fmt.Errorf("log file path %q has no file name", path)
	}
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open log directory: %w", err)
	}
	f := &RotatingFile{root: root, name: name, maxSize: maxSize, maxFiles: max(maxFiles, 0)}
	if err := f.open(); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return f, nil
}

// Write implements io.Writer. A single write larger than the maximum size
// goes into a file of its own.
func (f *RotatingFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return 0, errClosed
	}
	if f.size > 0 && f.size+int64(len(p)) > f.maxSize {
		if err := f.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := f.file.Write(p)
	f.size += int64(n)
	return n, err
}

// Close closes the log file. Later writes fail.
func (f *RotatingFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.root == nil {
		return nil
	}
	var err error
	if f.file != nil {
		err = f.file.Close()
		f.file = nil
	}
	err = errors.Join(err, f.root.Close())
	f.root = nil
	return err
}

func (f *RotatingFile) open() error {
	file, err := f.root.OpenFile(f.name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return errors.Join(fmt.Errorf("stat log file: %w", err), file.Close())
	}
	f.file, f.size = file, info.Size()
	return nil
}

func (f *RotatingFile) rotate() error {
	if err := f.file.Close(); err != nil {
		return fmt.Errorf("close log file: %w", err)
	}
	f.file = nil

	backup, err := f.backupName()
	if err == nil {
		err = f.root.Rename(f.name, backup)
	}
	// Reopen in any case, so that logging continues after a failed rename.
	if openErr := f.open(); openErr != nil {
		return errors.Join(err, openErr)
	}
	if err != nil {
		return fmt.Errorf("rotate log file: %w", err)
	}
	return f.prune()
}

// backupName returns an unused name for the rotated file. If a file with the
// current timestamp exists, the timestamp is moved forward by a nanosecond,
// which keeps the names in chronological order.
func (f *RotatingFile) backupName() (string, error) {
	stem, ext := f.split()
	ts := time.Now().UTC()
	for {
		name := stem + "-" + ts.Format(backupTimeLayout) + ext
		_, err := f.root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			return name, nil
		}
		if err != nil {
			return "", fmt.Errorf("check rotated log file: %w", err)
		}
		ts = ts.Add(time.Nanosecond)
	}
}

// prune removes the oldest rotated files beyond maxFiles.
func (f *RotatingFile) prune() error {
	entries, err := fs.ReadDir(f.root.FS(), ".")
	if err != nil {
		return fmt.Errorf("list log directory: %w", err)
	}
	stem, ext := f.split()
	var backups []string
	for _, e := range entries {
		ts, ok := strings.CutPrefix(e.Name(), stem+"-")
		if !ok || e.IsDir() {
			continue
		}
		ts, ok = strings.CutSuffix(ts, ext)
		if !ok || len(ts) != len(backupTimeLayout) {
			continue
		}
		if _, err := time.Parse(backupTimeLayout, ts); err != nil {
			continue
		}
		backups = append(backups, e.Name())
	}
	slices.Sort(backups)

	var errs []error
	for _, name := range backups[:max(len(backups)-f.maxFiles, 0)] {
		if err := f.root.Remove(name); err != nil {
			errs = append(errs, fmt.Errorf("remove rotated log file: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (f *RotatingFile) split() (stem, ext string) {
	ext = filepath.Ext(f.name)
	return strings.TrimSuffix(f.name, ext), ext
}
