// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
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
	// watch is the file that Watcher compares: the one named with
	// --config, else the default file, also if it does not exist yet.
	watch string
	// data is the content read at start; nil if no file was read.
	data []byte
	// allowed are the keys allowed in the file.
	allowed map[string]bool
	// fromFile are the flags whose value the file decides, because
	// neither the command line nor the environment sets them.
	fromFile map[string]bool
}

var _ kong.Resolver = (*FileResolver)(nil)

// NewFileResolver returns a resolver that reads defaultPath if it exists and
// no other file is named with --config or STREAMCREW_CONFIG. An empty
// defaultPath means there is no default file.
func NewFileResolver(defaultPath string) *FileResolver {
	return &FileResolver{defaultPath: defaultPath}
}

// Load prepares the resolver for the file at path like the CLI does, so
// that the watcher knows the allowed keys and which flags the file decides
// (Code-ADR-0019, point 5). Without a file at path, the resolver is ready
// for Update to create it.
func (r *FileResolver) Load(path string) error {
	r.watch = path
	if r.allowed == nil {
		r.allowed = maps.Clone(defaultKeys)
	}
	if r.fromFile == nil {
		r.fromFile = make(map[string]bool, len(r.allowed))
	}
	for key := range r.allowed {
		r.fromFile[flagOf(key)] = true
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		r.err = fmt.Errorf("read config file: %w", err)
		return r.err
	}
	values, err := parseFile(path, data, r.allowed)
	if err != nil {
		r.err = err
		return err
	}
	r.path, r.values, r.data = path, values, data
	return nil
}

// Path returns the configuration file that was read, or "" if none was.
func (r *FileResolver) Path() string {
	return r.path
}

// Err returns the error from reading or checking the configuration file.
func (r *FileResolver) Err() error {
	return r.err
}

// FromFile reports whether the file decides the value of the flag, because
// neither the command line nor the environment sets it (Code-ADR-0005).
func (r *FileResolver) FromFile(flag string) bool {
	return r.fromFile[flag]
}

// WatchPath returns the file the Watcher compares: the named file, else
// the default file, also if it does not exist yet.
func (r *FileResolver) WatchPath() string {
	return r.watch
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
	// kong asks resolvers only for flags the command line does not set.
	for _, env := range flag.Tag.Envs {
		if _, set := os.LookupEnv(env); set {
			return nil, nil
		}
	}
	if r.fromFile == nil {
		r.fromFile = make(map[string]bool)
	}
	r.fromFile[flag.Name] = true
	value, ok := r.values[fileKey(flag.Name)]
	if !ok || value == nil {
		return nil, nil
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
	r.watch = path
	r.allowed = fileKeys(kctx.Model.Node)

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if !explicit && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config file: %w", err)
	}
	r.path = path

	values, err := parseFile(path, data, r.allowed)
	if err != nil {
		return err
	}
	// log_file is a text flag; a bool in the file is the old form and
	// converts to its text.
	if b, ok := values["log_file"].(bool); ok {
		values["log_file"] = strconv.FormatBool(b)
	}
	r.values, r.data = values, data
	return nil
}

// parseFile reads the YAML data of the file at path; a key that allowed
// does not contain is an error.
func parseFile(path string, data []byte, allowed map[string]bool) (map[string]any, error) {
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	var errs []error
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if !allowed[key] {
			errs = append(errs, fmt.Errorf("config file %s: unknown key %q", path, key))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return values, nil
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

// defaultKeys are the keys allowed in the configuration file: the global
// flags of Config in their file form except log-level and --config. The
// flags of Config are static, so they are collected once.
//
//nolint:gochecknoglobals // static table derived from the flags of Config
var defaultKeys = func() map[string]bool {
	keys := fileKeys(nil)
	for name := range keys {
		keys[fileKey(name)] = true
	}
	return keys
}()

// flagLeaf returns the flag names of the fields of t, expanding embedded
// structs with their prefix. A struct field with a prefix (like Log
// `prefix:"log-"`, carried with `embed:""` for kong) is a group of flags
// with that prefix.
func flagLeaf(t reflect.Type, prefix string) map[string]bool {
	out := map[string]bool{}
	for _, f := range reflect.VisibleFields(t) {
		if f.Anonymous || (f.Type.Kind() == reflect.Struct && f.Tag.Get("prefix") != "") {
			for name := range flagLeaf(f.Type, f.Tag.Get("prefix")) {
				out[name] = true
			}
			continue
		}
		// With a name tag the flag is the prefix plus the name; without,
		// kong lowercases the field name.
		name := prefix + f.Tag.Get("name")
		if name == prefix {
			name = prefix + strings.ToLower(f.Name)
		}
		if name != "help" && name != configFlag {
			out[name] = true
		}
	}
	return out
}

// fileKeys returns the keys allowed in the configuration file: the global
// flags except --help and --config.
func fileKeys(root *kong.Node) map[string]bool {
	if root == nil {
		// Without the kong model the flags of Config are collected
		// directly; this keeps the file schema of the resolver and of
		// the CLI in one place.
		return flagLeaf(reflect.TypeFor[Config](), "")
	}
	keys := make(map[string]bool, len(root.Flags))
	for _, flag := range root.Flags {
		if flag.Name == "help" || flag.Name == configFlag {
			continue
		}
		keys[fileKey(flag.Name)] = true
	}
	return keys
}

// FileKey converts a flag name to the configuration file key
// (--log-level is log_level).
func FileKey(flagName string) string {
	return strings.ReplaceAll(flagName, "-", "_")
}

func fileKey(flagName string) string {
	return FileKey(flagName)
}
