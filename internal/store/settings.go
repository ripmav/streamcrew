// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// Settings returns the document of a settings section; found is false if
// the section has never been saved.
func (s *Store) Settings(ctx context.Context, section string) (doc []byte, found bool, err error) {
	row, err := s.reader().GetSettings(ctx, section)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("settings %q: %w", section, translate(err))
	}
	return []byte(row.Document), true, nil
}

// PutSettings saves the document of a settings section.
func (s *Store) PutSettings(ctx context.Context, section string, doc []byte) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		return q.PutSettings(ctx, sqlcgen.PutSettingsParams{
			Section:   section,
			Document:  string(doc),
			UpdatedAt: time.Now().UnixMilli(),
		})
	})
}
