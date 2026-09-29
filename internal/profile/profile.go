// SPDX-License-Identifier: Apache-2.0

// Package profile manages the profiles in a data directory (ADR-0012): one
// SQLite database per profile in <data-dir>/profiles/<id>.db, a display name
// in the database and the active profile in <data-dir>/profiles/active.
//
// The ID of a profile never changes; renaming changes the display name only.
// Callers hold the data directory lock (package lockfile) while they change
// profiles.
package profile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/store"
)

// DefaultID is the profile the core creates on the first start.
const DefaultID = "default"

// Metadata keys in the profile database.
const (
	// MetaName holds the display name.
	MetaName = "profile.name"
	// MetaCreatedAt holds the creation time in Unix milliseconds, UTC
	// (Code-ADR-0009).
	MetaCreatedAt = "profile.created_at"
)

const (
	maxIDLength = 32
	dbExt       = ".db"
	activeFile  = "active"
)

var (
	// ErrNotFound is returned for a profile that does not exist.
	ErrNotFound = errors.New("profile not found")
	// ErrExists is returned when a profile with the ID exists already.
	ErrExists = errors.New("profile exists already")
	// ErrInvalidID is returned for an ID that breaks the naming rule.
	ErrInvalidID = errors.New("invalid profile ID")
)

// Profile describes a profile.
type Profile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	Path      string    `json:"path"`
	Active    bool      `json:"active"`
}

// Manager manages the profiles of a data directory.
type Manager struct {
	dir       string
	storeOpts []store.Option
}

// NewManager returns a manager for <dataDir>/profiles. storeOpts apply when
// the manager opens a profile database, e.g. a backup before migrations.
func NewManager(dataDir string, storeOpts ...store.Option) *Manager {
	return &Manager{dir: filepath.Join(dataDir, "profiles"), storeOpts: storeOpts}
}

// Dir returns the profile directory.
func (m *Manager) Dir() string {
	return m.dir
}

// Path returns the database file of a profile.
func (m *Manager) Path(id string) string {
	return filepath.Join(m.dir, id+dbExt)
}

// ValidID reports whether id follows the rule: 1 to 32 characters of a-z,
// 0-9 and '-', not starting or ending with '-'.
func ValidID(id string) bool {
	if id == "" || len(id) > maxIDLength || strings.HasPrefix(id, "-") || strings.HasSuffix(id, "-") {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// Slug derives an ID from a display name: lowercase, letters and digits
// kept, everything else becomes '-'. Umlauts are transliterated. It returns
// "" if nothing usable remains.
func Slug(name string) string {
	replacer := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", "Ä", "ae", "Ö", "oe", "Ü", "ue")
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(replacer.Replace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimSuffix(b.String(), "-")
	if len(s) > maxIDLength {
		s = strings.TrimSuffix(s[:maxIDLength], "-")
	}
	return s
}

// Exists reports whether a profile database exists.
func (m *Manager) Exists(id string) bool {
	info, err := os.Stat(m.Path(id))
	return err == nil && info.Mode().IsRegular()
}

// List returns all profiles, sorted by ID. It reads them without migrating.
func (m *Manager) List(ctx context.Context) ([]Profile, error) {
	entries, err := os.ReadDir(m.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	active, err := m.Active()
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), dbExt)
		if !ok || e.IsDir() || !ValidID(id) {
			continue
		}
		p, err := m.describe(ctx, id)
		if err != nil {
			return nil, err
		}
		p.Active = id == active
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Profile) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Get returns one profile.
func (m *Manager) Get(ctx context.Context, id string) (Profile, error) {
	if !ValidID(id) {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	if !m.Exists(id) {
		return Profile{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	p, err := m.describe(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	active, err := m.Active()
	if err != nil {
		return Profile{}, err
	}
	p.Active = id == active
	return p, nil
}

func (m *Manager) describe(ctx context.Context, id string) (Profile, error) {
	info, err := store.Inspect(ctx, m.Path(id))
	if err != nil {
		return Profile{}, fmt.Errorf("profile %q: %w", id, err)
	}
	p := Profile{ID: id, Name: info.Meta[MetaName], Path: m.Path(id)}
	if p.Name == "" {
		p.Name = id
	}
	if ms, err := strconv.ParseInt(info.Meta[MetaCreatedAt], 10, 64); err == nil {
		p.CreatedAt = time.UnixMilli(ms).UTC()
	}
	return p, nil
}

// Create creates a profile with a display name. The ID is derived from the
// name unless id is given; a derived ID gets a number if it is taken.
func (m *Manager) Create(ctx context.Context, name, id string) (Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Profile{}, errors.New("profile name must not be empty")
	}
	if id == "" {
		id = m.freeID(Slug(name))
	}
	if !ValidID(id) {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	if m.Exists(id) {
		return Profile{}, fmt.Errorf("%w: %q", ErrExists, id)
	}
	s, err := store.Open(ctx, m.Path(id), m.storeOpts...)
	if err != nil {
		return Profile{}, fmt.Errorf("create profile %q: %w", id, err)
	}
	// Unix milliseconds like every time in the database (Code-ADR-0009).
	created := strconv.FormatInt(time.Now().UnixMilli(), 10)
	err = errors.Join(
		s.SetMeta(ctx, MetaName, name),
		s.SetMeta(ctx, MetaCreatedAt, created),
	)
	if err = errors.Join(err, s.Close()); err != nil {
		return Profile{}, fmt.Errorf("create profile %q: %w", id, err)
	}
	return m.Get(ctx, id)
}

// freeID returns base, or base-2, base-3 … if taken; "profile" if base is
// empty.
func (m *Manager) freeID(base string) string {
	if base == "" {
		base = "profile"
	}
	if !m.Exists(base) {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		candidate := strings.TrimSuffix(base[:min(len(base), maxIDLength-len(suffix))], "-") + suffix
		if !m.Exists(candidate) {
			return candidate
		}
	}
}

// Rename changes the display name.
func (m *Manager) Rename(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("profile name must not be empty")
	}
	if _, err := m.Get(ctx, id); err != nil {
		return err
	}
	s, err := store.Open(ctx, m.Path(id), m.storeOpts...)
	if err != nil {
		return fmt.Errorf("rename profile %q: %w", id, err)
	}
	return errors.Join(s.SetMeta(ctx, MetaName, name), s.Close())
}

// Delete removes a profile database. The active profile cannot be deleted.
func (m *Manager) Delete(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	if !m.Exists(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	active, err := m.Active()
	if err != nil {
		return err
	}
	if id == active {
		return fmt.Errorf("profile %q is active; switch to another profile first", id)
	}
	var errs []error
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(m.Path(id) + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Active returns the ID of the active profile; DefaultID if none is set.
func (m *Manager) Active() (string, error) {
	data, err := os.ReadFile(filepath.Join(m.dir, activeFile))
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultID, nil
	}
	if err != nil {
		return "", fmt.Errorf("read active profile: %w", err)
	}
	id := strings.TrimSpace(string(data))
	if !ValidID(id) {
		return "", fmt.Errorf("active profile: %w: %q", ErrInvalidID, id)
	}
	return id, nil
}

// SetActive makes an existing profile the active one.
func (m *Manager) SetActive(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	if !m.Exists(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return writeFileAtomic(filepath.Join(m.dir, activeFile), []byte(id+"\n"))
}

// Resolve returns the profile to run: override if given, otherwise the
// active one. The default profile is created if it does not exist yet; any
// other missing profile is an error.
func (m *Manager) Resolve(ctx context.Context, override string) (Profile, error) {
	id := override
	if id == "" {
		var err error
		if id, err = m.Active(); err != nil {
			return Profile{}, err
		}
	}
	if !ValidID(id) {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	if !m.Exists(id) {
		if id != DefaultID {
			return Profile{}, fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		if _, err := m.Create(ctx, "Default", DefaultID); err != nil {
			return Profile{}, err
		}
	}
	return m.Get(ctx, id)
}

// writeFileAtomic writes via a temporary file and a rename.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	_, werr := f.Write(data)
	err = errors.Join(werr, f.Sync(), f.Close())
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
