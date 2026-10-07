// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"slices"
	"strings"
)

// ProgramEnv returns the environment of the process without the variables
// of streamcrew (STREAMCREW_*), for programs the core starts (actions.md
// B117; Code-ADR-0019, point 6). Only this package reads the environment
// (Code-ADR-0005); the composition root calls it once at start. The result
// may hold secrets and is never shown or logged.
func ProgramEnv() []string {
	return withoutOwn(os.Environ())
}

// withoutOwn returns env without the variables whose name starts with the
// prefix of streamcrew, regardless of case, because Windows does not
// distinguish case in names.
func withoutOwn(env []string) []string {
	prefix := EnvPrefix + "_"
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return len(name) >= len(prefix) && strings.EqualFold(name[:len(prefix)], prefix)
	})
}

// SystemLocale returns the locale of the environment the process runs in
// (B41): the first non-empty of LC_ALL, LC_TIME and LANG, with the encoding
// stripped and underscores as hyphens, e.g. "de-DE" for "de_DE.UTF-8";
// empty when none is set.
func SystemLocale() string {
	for _, name := range []string{"LC_ALL", "LC_TIME", "LANG"} {
		v := os.Getenv(name)
		if v == "" {
			continue
		}
		v = strings.SplitN(v, ".", 2)[0]
		v = strings.ReplaceAll(v, "_", "-")
		if v != "" {
			return v
		}
	}
	return ""
}
