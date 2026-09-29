// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alecthomas/kong"
	"go.yaml.in/yaml/v3"
)

// EnvPrefix is the prefix of the environment variables: --data-dir is
// STREAMCREW_DATA_DIR.
const EnvPrefix = "STREAMCREW"

// configFlag is the flag that names the configuration file. It cannot be set
// in the file itself.
const configFlag = "config"

// KongOptions returns the kong options that bind Config to its defaults, the
// STREAMCREW_* environment variables and the configuration file.
func KongOptions(d Defaults, file *FileResolver) []kong.Option {
	return []kong.Option{
		kong.DefaultEnvars(EnvPrefix),
		kong.Vars{"default_data_dir": d.DataDir},
		kong.Resolvers(file),
	}
}

// FileResolver is a kong.Resolver that supplies flag values from the YAML
// configuration file. The keys of the file are the global flag names in
// snake_case (--log-level is log_level).
//
// It differs from kong's own configuration loaders in three ways:
//   - Environment variables take precedence over the file, so that the
//     order is flag, environment, file, default.
//   - Unknown keys are an error instead of being ignored.
//   - An explicitly named file (--config, STREAMCREW_CONFIG) replaces the
//     default file instead of being read in addition to it.
//
// The file is read on the first lookup. Problems with it are not returned to
// kong, which would report them under the name of an unrelated flag; the
// caller checks Err after parsing instead.
type FileResolver struct {
	defaultPath string

	loaded bool
	path   string
	values map[string]any
	err    error
}

var _ kong.Resolver = (*FileResolver)(nil)

// NewFileResolver returns a resolver that reads defaultPath if it exists and
// no other file is named with --config or STREAMCREW_CONFIG. An empty
// defaultPath means there is no default file.
func NewFileResolver(defaultPath string) *FileResolver {
	return &FileResolver{defaultPath: defaultPath}
}

// Path returns the configuration file that was read, or "" if none was.
func (r *FileResolver) Path() string {
	return r.path
}

// Err returns the error from reading or checking the configuration file.
func (r *FileResolver) Err() error {
	return r.err
}

// Validate implements kong.Resolver. The keys are checked when the file is
// read, because only then is it known which file applies.
func (r *FileResolver) Validate(*kong.Application) error {
	return nil
}

// Resolve implements kong.Resolver.
func (r *FileResolver) Resolve(kctx *kong.Context, _ *kong.Path, flag *kong.Flag) (any, error) {
	if !r.loaded {
		r.loaded = true
		// After an error, values stays nil and the file supplies nothing.
		r.err = r.load(kctx)
	}
	value, ok := r.values[fileKey(flag.Name)]
	if !ok || value == nil {
		return nil, nil
	}
	for _, env := range flag.Tag.Envs {
		if _, set := os.LookupEnv(env); set {
			return nil, nil
		}
	}
	return value, nil
}

func (r *FileResolver) load(kctx *kong.Context) error {
	path, explicit := r.defaultPath, false
	if p := explicitConfigFile(kctx); p != "" {
		path, explicit = p, true
	}
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if !explicit && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config file: %w", err)
	}
	r.path = path

	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	allowed := fileKeys(kctx.Model.Node)
	var errs []error
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if !allowed[key] {
			errs = append(errs, fmt.Errorf("config file %s: unknown key %q", path, key))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	r.values = values
	return nil
}

// explicitConfigFile returns the file named with --config or
// STREAMCREW_CONFIG, or "" if there is none.
func explicitConfigFile(kctx *kong.Context) string {
	for _, flag := range kctx.Model.Flags {
		if flag.Name == configFlag {
			if s, ok := kctx.FlagValue(flag).(string); ok {
				return s
			}
		}
	}
	return ""
}

// fileKeys returns the keys allowed in the configuration file: the global
// flags except --help and --config.
func fileKeys(root *kong.Node) map[string]bool {
	keys := make(map[string]bool, len(root.Flags))
	for _, flag := range root.Flags {
		if flag.Name == "help" || flag.Name == configFlag {
			continue
		}
		keys[fileKey(flag.Name)] = true
	}
	return keys
}

func fileKey(flagName string) string {
	return strings.ReplaceAll(flagName, "-", "_")
}
