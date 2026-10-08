// SPDX-License-Identifier: MIT

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
