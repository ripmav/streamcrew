// SPDX-License-Identifier: MIT

package logging

import (
	"log/slog"
	"strings"
)

// Redacted replaces secrets in the log output.
const Redacted = "[REDACTED]"

// Secret is a string that must never appear in logs. It formats as Redacted
// in slog, with fmt (%s, %v, %#v) and in text encodings such as JSON and
// YAML. Use Reveal where the value itself is needed, e.g. for an HTTP header.
type Secret string

// Reveal returns the secret value.
func (s Secret) Reveal() string {
	return string(s)
}

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value {
	return slog.StringValue(Redacted)
}

// String implements fmt.Stringer.
func (Secret) String() string {
	return Redacted
}

// GoString implements fmt.GoStringer.
func (Secret) GoString() string {
	return Redacted
}

// MarshalText implements encoding.TextMarshaler, so that a Secret in a
// struct does not leak through JSON or YAML output.
func (Secret) MarshalText() ([]byte, error) {
	return []byte(Redacted), nil
}

// redact is the slog ReplaceAttr function of all handlers. It masks the
// values of keys that look like they hold a secret.
func redact(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindGroup && isSecretKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	return a
}

// isSecretKey reports whether an attribute key names a secret. It is a
// heuristic on parts of the key, e.g. "access_token" or "Authorization".
func isSecretKey(key string) bool {
	key = strings.ToLower(key)
	for _, part := range [...]string{
		"token", "secret", "password", "passwd", "authorization",
		"cookie", "apikey", "api_key", "credential", "private_key",
	} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}
