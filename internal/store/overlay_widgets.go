// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// OverlayWidgetState returns the JSON state document of the overlay widget
// (spec overlays.md, B20); found is false if the widget has no state yet.
func (s *Store) OverlayWidgetState(ctx context.Context, widgetID string) ([]byte, bool, error) {
	row, err := s.reader().GetOverlayWidgetState(ctx, widgetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("overlay widget state %s: %w", widgetID, translate(err))
	}
	return []byte(row), true, nil
}

// SetOverlayWidgetState stores the JSON state document of the overlay
// widget (spec overlays.md, B20); an existing document is replaced.
func (s *Store) SetOverlayWidgetState(ctx context.Context, widgetID string, state []byte) error {
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		if err := q.SetOverlayWidgetState(ctx, sqlcgen.SetOverlayWidgetStateParams{
			WidgetID: widgetID,
			State:    string(state),
		}); err != nil {
			return translate(err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("overlay widget state %s: %w", widgetID, err)
	}
	return nil
}
