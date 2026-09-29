// SPDX-License-Identifier: Apache-2.0

package polydoc

import (
	"fmt"
	"time"
)

// Duration is a time.Duration that documents hold as a Go duration string
// such as "30s" or "5m0s", in JSON as in YAML (Code-ADR-0009: durations in
// YAML as Go durations; Code-ADR-0010: the same keys and values in both).
type Duration time.Duration

// Std returns d as a time.Duration.
func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("duration: %w", err)
	}
	*d = Duration(v)
	return nil
}
