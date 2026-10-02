// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/netguard"
)

// rootNamePattern is the form of the names of released roots: 1 to 32
// lowercase letters, digits and "_" (Code-ADR-0019, point 3).
var rootNamePattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// DefaultCapabilities returns the capabilities of the mode m (ADR-0013,
// point 2; Code-ADR-0019, point 1). host:input is off in every mode until
// the agent exists.
func DefaultCapabilities(m Mode) capability.Set {
	var caps []capability.Capability
	switch m {
	case ModeDesktop, ModeDaemon:
		caps = []capability.Capability{
			capability.HostFS, capability.HostProcess, capability.HostAudio, capability.NetOutbound, capability.Script,
		}
	case ModeServer:
		caps = []capability.Capability{capability.NetOutbound, capability.Script}
	}
	s, _ := capability.NewSet(caps...) // the capabilities above are known
	return s
}

// Rights are the settings of Code-ADR-0019 that the core reloads from the
// configuration file without a restart: the capabilities, the released
// roots of the file action and the allowlist of network targets. A value
// does not change; a reload replaces it as a whole.
type Rights struct {
	// Mode is the operating mode the capabilities start from.
	Mode Mode
	// Capabilities are those of the mode with Grant and without Revoke.
	Capabilities capability.Set
	// Roots maps the name of each released root to its absolute
	// directory.
	Roots map[string]string
	// Outbound opens internal targets for web requests in server mode.
	Outbound netguard.Allowlist
}

// Rights returns the rights of c: the capabilities of the mode with
// --grant and without --revoke, the roots of --file-root and the allowlist
// of --outbound-allow (Code-ADR-0019, points 1 to 4). The error names every
// problem and the flag it belongs to.
func (c *Config) Rights() (Rights, error) {
	r := Rights{Mode: c.Mode, Roots: make(map[string]string, len(c.FileRoot))}
	var errs []error

	grant, err := capabilities("--grant", c.Grant)
	errs = append(errs, err)
	revoke, err := capabilities("--revoke", c.Revoke)
	errs = append(errs, err)
	for _, g := range grant {
		if slices.Contains(revoke, g) {
			errs = append(errs, fmt.Errorf("--grant, --revoke: %s is in both", g))
		}
	}
	caps := slices.DeleteFunc(append(DefaultCapabilities(c.Mode).List(), grant...),
		func(x capability.Capability) bool { return slices.Contains(revoke, x) })
	if r.Capabilities, err = capability.NewSet(caps...); err != nil {
		errs = append(errs, err)
	}

	for _, name := range slices.Sorted(maps.Keys(c.FileRoot)) {
		dir, err := rootDir(name, c.FileRoot[name], c.DataDir)
		if err != nil {
			errs = append(errs, fmt.Errorf("--file-root %s: %w", name, err))
			continue
		}
		r.Roots[name] = dir
	}

	if r.Outbound, err = netguard.ParseAllowlist(c.OutboundAllow); err != nil {
		errs = append(errs, fmt.Errorf("--outbound-allow: %w", err))
	}
	if err := errors.Join(errs...); err != nil {
		return Rights{}, err
	}
	return r, nil
}

// capabilities returns the capabilities named in list.
func capabilities(flag string, list []string) ([]capability.Capability, error) {
	caps := make([]capability.Capability, 0, len(list))
	var errs []error
	for _, name := range list {
		c := capability.Capability(strings.TrimSpace(name))
		if !c.Valid() {
			errs = append(errs, fmt.Errorf("%s: unknown capability %q", flag, name))
			continue
		}
		caps = append(caps, c)
	}
	return caps, errors.Join(errs...)
}

// rootDir checks the root name with the directory dir and returns dir in
// its clean form. dir must be absolute and must neither be, contain nor lie
// in the data directory (Code-ADR-0019, point 3).
func rootDir(name, dir, dataDir string) (string, error) {
	switch {
	case !rootNamePattern.MatchString(name):
		return "", errors.New("the name is not 1 to 32 lowercase letters, digits and \"_\"")
	case !filepath.IsAbs(dir):
		return "", fmt.Errorf("%q is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	if dataDir != "" && overlaps(dir, dataDir) {
		return "", fmt.Errorf("%q overlaps the data directory %q", dir, dataDir)
	}
	return dir, nil
}

// overlaps reports whether a and b are the same directory or one lies in
// the other, as written and with symbolic links resolved where they exist.
func overlaps(a, b string) bool {
	if absB, err := filepath.Abs(b); err == nil {
		b = absB
	}
	if nested(a, b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && nested(ra, rb)
}

// nested reports whether a and b are the same path or one lies in the
// other.
func nested(a, b string) bool {
	within := func(inner, outer string) bool {
		rel, err := filepath.Rel(outer, inner)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}

// Equal reports whether r and o grant the same.
func (r Rights) Equal(o Rights) bool {
	return r.Mode == o.Mode &&
		slices.Equal(r.Capabilities.List(), o.Capabilities.List()) &&
		maps.Equal(r.Roots, o.Roots) &&
		r.Outbound.Equal(o.Outbound)
}

// Warnings returns what the core warns about in the log and in
// "streamcrew doctor" (Code-ADR-0019, points 3, 4 and 7): host capabilities
// in server mode, roots whose directory is missing, roots without host:fs
// and an allowlist outside server mode.
func (r Rights) Warnings() []string {
	var ws []string
	if r.Mode == ModeServer {
		for _, c := range r.Capabilities.List() {
			if strings.HasPrefix(string(c), "host:") {
				ws = append(ws, fmt.Sprintf("%s is on in server mode", c))
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(r.Roots)) {
		dir := r.Roots[name]
		switch info, err := os.Stat(dir); {
		case err != nil:
			ws = append(ws, fmt.Sprintf("file root %s: %v", name, err))
		case !info.IsDir():
			ws = append(ws, fmt.Sprintf("file root %s: %s is not a directory", name, dir))
		}
	}
	if len(r.Roots) > 0 && !r.Capabilities.Has(capability.HostFS) {
		ws = append(ws, "the file roots have no effect without host:fs")
	}
	if r.Mode != ModeServer && len(r.Outbound.Entries()) > 0 {
		ws = append(ws, "the outbound allowlist has no effect outside server mode")
	}
	return ws
}

// Live holds the rights that apply now (Code-ADR-0019, point 5). The
// composition root hands it to the users of the rights; Watcher replaces
// them when the configuration file changes. It is safe for concurrent use.
type Live struct {
	p atomic.Pointer[Rights]
}

// NewLive returns a Live with the rights r.
func NewLive(r Rights) *Live {
	l := &Live{}
	l.Store(r)
	return l
}

// Rights returns the rights that apply now.
func (l *Live) Rights() Rights {
	return *l.p.Load()
}

// Store replaces the rights.
func (l *Live) Store(r Rights) {
	r.Roots = maps.Clone(r.Roots)
	l.p.Store(&r)
}

// Current implements capability.Source, e.g. for the action type registry.
func (l *Live) Current() capability.Set {
	return l.p.Load().Capabilities
}

// HasRoot reports whether a root of this name is released now; it
// implements command.Roots.
func (l *Live) HasRoot(name string) bool {
	_, ok := l.Root(name)
	return ok
}

// Root returns the directory of the released root name; ok is false if
// there is none now.
func (l *Live) Root(name string) (dir string, ok bool) {
	dir, ok = l.p.Load().Roots[name]
	return dir, ok
}

// Outbound returns the allowlist of network targets that applies now.
func (l *Live) Outbound() netguard.Allowlist {
	return l.p.Load().Outbound
}
