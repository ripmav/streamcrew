// SPDX-License-Identifier: MIT

// Package id provides the identifiers of streamcrew entities and events:
// UUIDv7 from the standard library, which sort chronologically
// (Code-ADR-0009).
//
// In the database, in JSON and in YAML an ID is its canonical lowercase text
// form, e.g. "0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d".
package id

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
	"uuid"
)

// ID identifies an entity or event. The zero value is the nil UUID and means
// "no ID".
type ID uuid.UUID

// New returns a new UUIDv7. IDs from one process are increasing unless the
// system clock moves backwards.
func New() ID {
	return ID(uuid.NewV7())
}

// Parse reads the text form of an ID.
func Parse(s string) (ID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return ID{}, fmt.Errorf("parse id %q: %w", s, err)
	}
	return ID(u), nil
}

// MustParse is Parse for constant IDs in tests and fixtures. It panics on an
// invalid ID.
func MustParse(s string) ID {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// IsZero reports whether the ID is the zero value.
func (v ID) IsZero() bool {
	return v == ID{}
}

// String returns the canonical text form.
func (v ID) String() string {
	return uuid.UUID(v).String()
}

// Compare returns -1, 0 or +1 as v sorts before, equal to or after w.
func (v ID) Compare(w ID) int {
	return uuid.UUID(v).Compare(uuid.UUID(w))
}

// Time returns the creation time encoded in a UUIDv7, with millisecond
// precision. For other versions and the zero ID it returns the zero time.
func (v ID) Time() time.Time {
	if v[6]>>4 != 7 {
		return time.Time{}
	}
	ms := int64(v[0])<<40 | int64(v[1])<<32 | int64(v[2])<<24 | int64(v[3])<<16 | int64(v[4])<<8 | int64(v[5])
	return time.UnixMilli(ms).UTC()
}

// MarshalText implements encoding.TextMarshaler.
func (v ID) MarshalText() ([]byte, error) {
	return uuid.UUID(v).MarshalText()
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (v *ID) UnmarshalText(b []byte) error {
	u, err := uuid.Parse(string(b))
	if err != nil {
		return fmt.Errorf("parse id %q: %w", b, err)
	}
	*v = ID(u)
	return nil
}

// Value implements driver.Valuer: the text form.
func (v ID) Value() (driver.Value, error) {
	return v.String(), nil
}

// errScanType is returned when a database value is neither text nor bytes.
var errScanType = errors.New("id: unsupported database type")

// Scan implements sql.Scanner for text columns.
func (v *ID) Scan(src any) error {
	switch s := src.(type) {
	case string:
		return v.UnmarshalText([]byte(s))
	case []byte:
		return v.UnmarshalText(s)
	default:
		return fmt.Errorf("%w %T", errScanType, src)
	}
}
