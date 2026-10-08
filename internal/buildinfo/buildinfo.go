// SPDX-License-Identifier: MIT

// Package buildinfo reports the version of the running binary.
//
// The version comes from the build information that the Go toolchain embeds:
// the module version derived from the VCS tag (or a pseudo-version) and the VCS
// revision. No linker flags or package variables are needed.
package buildinfo

import (
	"cmp"
	"runtime"
	"runtime/debug"
	"strings"
)

// DevelVersion is the version of a binary built without VCS information,
// for example from a source archive or with -buildvcs=false.
const DevelVersion = "(devel)"

// Info describes the running binary.
type Info struct {
	// Version is the module version, e.g. "v0.1.0" or a pseudo-version.
	Version string `json:"version"`
	// Revision is the VCS commit the binary was built from, if known.
	Revision string `json:"revision,omitempty"`
	// Time is the commit time in RFC 3339 format, if known.
	Time string `json:"time,omitempty"`
	// Modified reports whether the working tree had uncommitted changes.
	Modified bool `json:"modified,omitzero"`
	// GoVersion is the Go toolchain that built the binary.
	GoVersion string `json:"go_version"`
	// Platform is the target as GOOS/GOARCH.
	Platform string `json:"platform"`
}

// Read returns the build information of the running binary.
func Read() Info {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return FromBuildInfo(nil)
	}
	return FromBuildInfo(bi)
}

// FromBuildInfo converts Go build information into an Info. A nil bi yields
// an Info with DevelVersion and the version of the running toolchain.
func FromBuildInfo(bi *debug.BuildInfo) Info {
	info := Info{
		Version:   DevelVersion,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi == nil {
		return info
	}
	info.Version = cmp.Or(bi.Main.Version, DevelVersion)
	info.GoVersion = cmp.Or(bi.GoVersion, info.GoVersion)
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Revision = s.Value
		case "vcs.time":
			info.Time = s.Value
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}

// String formats the information as a single line, e.g.
// "v0.1.0 (a1b2c3d4e5f6, 2026-09-29T10:00:00Z, go1.27.1, linux/amd64)".
func (i Info) String() string {
	details := make([]string, 0, 4)
	if i.Revision != "" {
		rev := i.Revision[:min(12, len(i.Revision))]
		if i.Modified {
			rev += "-dirty"
		}
		details = append(details, rev)
	}
	if i.Time != "" {
		details = append(details, i.Time)
	}
	details = append(details, i.GoVersion, i.Platform)
	return i.Version + " (" + strings.Join(details, ", ") + ")"
}
