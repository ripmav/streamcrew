// SPDX-License-Identifier: Apache-2.0

// Package backup creates, lists, prunes and restores profile backups
// (ADR-0012): a ZIP file <profile-id>-<UTC timestamp>.zip with a consistent
// copy of the database made by VACUUM INTO and a manifest with versions and
// checksum.
package backup

import (
	"archive/zip"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// FormatVersion is the version of the backup format written by this code.
const FormatVersion = 1

// Names inside the ZIP file.
const (
	manifestName = "manifest.json"
	databaseName = "profile.db"
)

// maxDatabaseSize limits what Restore extracts, against corrupt or hostile
// archives.
const maxDatabaseSize = 16 << 30

// Kind says why a backup was made. Retention only removes KindScheduled.
type Kind string

// Kinds of backups.
const (
	KindScheduled    Kind = "scheduled"
	KindManual       Kind = "manual"
	KindPreMigration Kind = "pre-migration"
	KindPreRestore   Kind = "pre-restore"
	KindPreDelete    Kind = "pre-delete"
)

var (
	// ErrSchemaTooNew is returned by Restore for a backup of a newer schema
	// than this version supports.
	ErrSchemaTooNew = errors.New("backup has a newer schema than this version of streamcrew supports")
	// ErrCorrupt is returned for an archive that fails the checks.
	ErrCorrupt = errors.New("backup is corrupt")
)

// Manifest describes a backup.
type Manifest struct {
	FormatVersion int       `json:"formatVersion"`
	AppVersion    string    `json:"appVersion"`
	SchemaVersion int64     `json:"schemaVersion"`
	ProfileID     string    `json:"profileId"`
	ProfileName   string    `json:"profileName"`
	Kind          Kind      `json:"kind"`
	CreatedAt     time.Time `json:"createdAt"`
	SHA256        string    `json:"sha256"`
}

// Info is a backup file with its manifest.
type Info struct {
	Path     string   `json:"path"`
	Size     int64    `json:"size"`
	Manifest Manifest `json:"manifest"`
}

// Source is the open profile database to back up; *store.Store implements
// it.
type Source interface {
	VacuumInto(ctx context.Context, dest string) error
	SchemaVersion() int64
}

// Request names the profile and the reason of a backup.
type Request struct {
	ProfileID   string
	ProfileName string
	Kind        Kind
	AppVersion  string
}

// Create writes a backup of src into dir.
func Create(ctx context.Context, src Source, dir string, req Request) (Info, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Info{}, fmt.Errorf("create backup directory: %w", err)
	}
	tmpDir, err := os.MkdirTemp(dir, ".tmp-")
	if err != nil {
		return Info{}, fmt.Errorf("create backup: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, databaseName)
	if err := src.VacuumInto(ctx, dbPath); err != nil {
		return Info{}, fmt.Errorf("create backup: %w", err)
	}
	sum, err := hashFile(dbPath)
	if err != nil {
		return Info{}, fmt.Errorf("create backup: %w", err)
	}
	m := Manifest{
		FormatVersion: FormatVersion,
		AppVersion:    req.AppVersion,
		SchemaVersion: src.SchemaVersion(),
		ProfileID:     req.ProfileID,
		ProfileName:   req.ProfileName,
		Kind:          cmp.Or(req.Kind, KindManual),
		CreatedAt:     time.Now().UTC().Truncate(time.Millisecond),
		SHA256:        sum,
	}

	zipPath := filepath.Join(tmpDir, "backup.zip")
	if err := writeZip(zipPath, m, dbPath); err != nil {
		return Info{}, fmt.Errorf("create backup: %w", err)
	}
	final, err := claimName(dir, req.ProfileID, m.CreatedAt)
	if err != nil {
		return Info{}, err
	}
	// Replaces the empty file claimName created, never another backup.
	if err := os.Rename(zipPath, final); err != nil {
		return Info{}, errors.Join(fmt.Errorf("create backup: %w", err), os.Remove(final))
	}
	st, err := os.Stat(final)
	if err != nil {
		return Info{}, fmt.Errorf("create backup: %w", err)
	}
	return Info{Path: final, Size: st.Size(), Manifest: m}, nil
}

func writeZip(path string, m Manifest, dbPath string) (err error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()

	zw := zip.NewWriter(f)
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: manifestName, Method: zip.Deflate, Modified: m.CreatedAt})
	if err != nil {
		return err
	}
	if _, err := w.Write(append(manifest, '\n')); err != nil {
		return err
	}
	w, err = zw.CreateHeader(&zip.FileHeader{Name: databaseName, Method: zip.Deflate, Modified: m.CreatedAt})
	if err != nil {
		return err
	}
	db, err := os.Open(filepath.Clean(dbPath))
	if err != nil {
		return err
	}
	_, err = io.Copy(w, db)
	if err = errors.Join(err, db.Close()); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Sync()
}

// claimName reserves an unused file name for a backup created at t by
// creating an empty file with O_EXCL. Two backups of a profile started in
// the same second, e.g. the scheduled and a manual one, thus never get the
// same name; List skips the empty file until Create replaces it.
func claimName(dir, profileID string, t time.Time) (string, error) {
	base := profileID + "-" + t.UTC().Format("20060102T150405Z")
	for n := 1; n < 1000; n++ {
		name := base + ".zip"
		if n > 1 {
			name = base + "-" + strconv.Itoa(n) + ".zip"
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create backup: %w", err)
		}
		return path, f.Close()
	}
	return "", fmt.Errorf("create backup: no free file name for %s", base)
}

// Read returns the manifest of a backup file.
func Read(path string) (Info, error) {
	zr, err := zip.OpenReader(filepath.Clean(path))
	if err != nil {
		return Info{}, fmt.Errorf("%w: %s: %w", ErrCorrupt, path, err)
	}
	defer zr.Close()
	m, err := readManifest(&zr.Reader)
	if err != nil {
		return Info{}, fmt.Errorf("%s: %w", path, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return Info{}, err
	}
	return Info{Path: path, Size: st.Size(), Manifest: m}, nil
}

func readManifest(zr *zip.Reader) (Manifest, error) {
	f, err := zr.Open(manifestName)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: no manifest", ErrCorrupt)
	}
	defer f.Close()
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: manifest: %w", ErrCorrupt, err)
	}
	if m.FormatVersion < 1 || m.FormatVersion > FormatVersion {
		return Manifest{}, fmt.Errorf("%w: unsupported format version %d", ErrCorrupt, m.FormatVersion)
	}
	return m, nil
}

// List returns the backups of a profile in dir, newest first. Files that are
// not readable backups are skipped.
func List(dir, profileID string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	var out []Info
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, profileID+"-") || !strings.HasSuffix(name, ".zip") {
			continue
		}
		info, err := Read(filepath.Join(dir, name))
		if err != nil || info.Manifest.ProfileID != profileID {
			continue
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b Info) int {
		return cmp.Or(b.Manifest.CreatedAt.Compare(a.Manifest.CreatedAt), strings.Compare(b.Path, a.Path))
	})
	return out, nil
}

// Restore replaces the database at target with the one in the backup. The
// profile must not be open. It checks the format, the checksum and that the
// schema is not newer than maxSchema; an older schema is migrated when the
// profile is opened next.
func Restore(ctx context.Context, backupPath, target string, maxSchema int64) (Manifest, error) {
	zr, err := zip.OpenReader(filepath.Clean(backupPath))
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	defer zr.Close()
	m, err := readManifest(&zr.Reader)
	if err != nil {
		return Manifest{}, err
	}
	if m.SchemaVersion > maxSchema {
		return m, fmt.Errorf("%w: backup %d, supported %d", ErrSchemaTooNew, m.SchemaVersion, maxSchema)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return m, fmt.Errorf("restore: %w", err)
	}
	tmp, err := extractDatabase(ctx, &zr.Reader, filepath.Dir(target), m.SHA256)
	if err != nil {
		return m, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(target + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return m, errors.Join(fmt.Errorf("restore: %w", err), os.Remove(tmp))
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		return m, errors.Join(fmt.Errorf("restore: %w", err), os.Remove(tmp))
	}
	return m, nil
}

// extractDatabase copies the database next to the target and verifies its
// checksum; it returns the temporary file.
func extractDatabase(ctx context.Context, zr *zip.Reader, dir, wantSum string) (path string, err error) {
	src, err := zr.Open(databaseName)
	if err != nil {
		return "", fmt.Errorf("%w: no database", ErrCorrupt)
	}
	defer src.Close()
	dst, err := os.CreateTemp(dir, ".restore-*.db")
	if err != nil {
		return "", fmt.Errorf("restore: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dst.Name())
		}
	}()
	h := sha256.New()
	n, err := copyWithContext(ctx, io.MultiWriter(dst, h), src, maxDatabaseSize+1)
	err = errors.Join(err, dst.Sync(), dst.Close())
	if err != nil {
		return "", fmt.Errorf("restore: %w", err)
	}
	if n > maxDatabaseSize {
		return "", fmt.Errorf("%w: database larger than %d bytes", ErrCorrupt, int64(maxDatabaseSize))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSum {
		return "", fmt.Errorf("%w: checksum mismatch", ErrCorrupt)
	}
	if err := os.Chmod(dst.Name(), 0o600); err != nil {
		return "", fmt.Errorf("restore: %w", err)
	}
	return dst.Name(), nil
}

// copyWithContext copies at most limit bytes and stops when ctx ends.
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	buf := make([]byte, 1<<20)
	var total int64
	for total < limit {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := src.Read(buf[:min(int64(len(buf)), limit-total)])
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
