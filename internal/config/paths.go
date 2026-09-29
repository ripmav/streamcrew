// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// AppDirName is the name of the data directory below os.UserConfigDir.
	AppDirName = "streamcrew"
	// ConfigFileName is the name of the default configuration file in the
	// default data directory.
	ConfigFileName = "config.yaml"
	// PortableMarker is the file next to the executable that switches on the
	// portable mode.
	PortableMarker = "streamcrew.portable"
	// PortableDataDir is the data directory next to the executable in portable
	// mode.
	PortableDataDir = "streamcrew-data"
)

// Defaults are the values that depend on the environment of the process and
// therefore cannot be kong defaults written in the struct tags.
type Defaults struct {
	// DataDir is the default data directory; empty if none could be
	// determined, then --data-dir is required.
	DataDir string
	// Portable reports whether DataDir is the portable data directory.
	Portable bool
}

// ConfigFile returns the configuration file that is read when --config is
// not set, or "" if there is no default data directory.
func (d Defaults) ConfigFile() string {
	if d.DataDir == "" {
		return ""
	}
	return filepath.Join(d.DataDir, ConfigFileName)
}

// DetectDefaults determines the default data directory for the executable at
// the given path: <executable dir>/streamcrew-data if the portable marker
// exists there, <user config dir>/streamcrew otherwise. An empty executable
// skips the portable check.
func DetectDefaults(executable string) (Defaults, error) {
	if executable != "" {
		dir := filepath.Dir(executable)
		_, err := os.Stat(filepath.Join(dir, PortableMarker))
		switch {
		case err == nil:
			return Defaults{DataDir: filepath.Join(dir, PortableDataDir), Portable: true}, nil
		case !errors.Is(err, fs.ErrNotExist):
			return Defaults{}, fmt.Errorf("check portable mode: %w", err)
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return Defaults{}, fmt.Errorf("determine default data directory: %w", err)
	}
	return Defaults{DataDir: filepath.Join(dir, AppDirName)}, nil
}
