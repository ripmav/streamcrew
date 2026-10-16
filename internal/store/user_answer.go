// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// SetUserAnswer stores the user's answer for key, replacing an existing
// one (roadmap 5.6, P1, user-specific chat commands).
func (s *Store) SetUserAnswer(ctx context.Context, userID id.ID, key, answer string) error {
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		return q.SetUserAnswer(ctx, sqlcgen.SetUserAnswerParams{
			UserID:    userID.String(),
			Key:       key,
			Answer:    answer,
			UpdatedAt: now().UnixMilli(),
		})
	})
	if err != nil {
		return fmt.Errorf("set the answer of %s for %q: %w", userID, key, err)
	}
	return nil
}

// UserAnswer returns the user's stored answer for key; ok is false if the
// user has none.
func (s *Store) UserAnswer(ctx context.Context, userID id.ID, key string) (string, bool, error) {
	var answer string
	var found bool
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		answer, err = q.GetUserAnswer(ctx, sqlcgen.GetUserAnswerParams{UserID: userID.String(), Key: key})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return "", false, fmt.Errorf("answer of %s for %q: %w", userID, key, err)
	}
	return answer, found, nil
}

// DeleteUserAnswer removes the user's stored answer for key.
func (s *Store) DeleteUserAnswer(ctx context.Context, userID id.ID, key string) error {
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		_, err := q.DeleteUserAnswer(ctx, sqlcgen.DeleteUserAnswerParams{UserID: userID.String(), Key: key})
		return err
	})
	if err != nil {
		return fmt.Errorf("delete the answer of %s for %q: %w", userID, key, err)
	}
	return nil
}
