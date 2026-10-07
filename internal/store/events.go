// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// StreamSessions returns the stored stream sessions, one per platform that
// had one (events.md, B11, B21).
func (s *Store) StreamSessions(ctx context.Context) ([]stream.Session, error) {
	rows, err := s.reader().ListStreamSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list stream sessions: %w", translate(err))
	}
	sessions := make([]stream.Session, 0, len(rows))
	for _, r := range rows {
		ss := stream.Session{
			Platform:  platform.Name(r.Platform),
			StartedAt: fromNullMillis(r.StartedAt),
			State:     stream.State(r.State),
			Since:     fromNullMillis(r.Since),
			SeenLive:  fromNullMillis(r.SeenLive),
		}
		if err := ss.Validate(); err != nil {
			return nil, fmt.Errorf("list stream sessions: %w", err)
		}
		sessions = append(sessions, ss)
	}
	return sessions, nil
}

// PutStreamSession stores the session of its platform, with millisecond
// precision.
func (s *Store) PutStreamSession(ctx context.Context, ss stream.Session) error {
	if err := ss.Validate(); err != nil {
		return err
	}
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		return q.PutStreamSession(ctx, sessionParams(ss))
	})
	if err != nil {
		return fmt.Errorf("put stream session of %s: %w", ss.Platform, err)
	}
	return nil
}

// StartStreamSession stores the new session of its platform and forgets the
// events that fired in the session before (B3), in one transaction.
func (s *Store) StartStreamSession(ctx context.Context, ss stream.Session) error {
	if err := ss.Validate(); err != nil {
		return err
	}
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		if err := q.DeleteSessionEvents(ctx, string(ss.Platform)); err != nil {
			return err
		}
		return q.PutStreamSession(ctx, sessionParams(ss))
	})
	if err != nil {
		return fmt.Errorf("start stream session of %s: %w", ss.Platform, err)
	}
	return nil
}

func sessionParams(ss stream.Session) sqlcgen.PutStreamSessionParams {
	return sqlcgen.PutStreamSessionParams{
		Platform:  string(ss.Platform),
		StartedAt: nullMillis(ss.StartedAt),
		State:     string(ss.State),
		Since:     nullMillis(ss.Since),
		SeenLive:  nullMillis(ss.SeenLive),
	}
}

// FirstInSession records that an event of type t fired for the user in the
// stream session of platform p and reports whether it is the first one
// (events.md, B3, B11). The user must exist; the record goes with it.
func (s *Store) FirstInSession(ctx context.Context, p platform.Name, t event.Type, userID id.ID) (bool, error) {
	var n int64
	err := s.Write(ctx, func(q *sqlcgen.Queries) (err error) {
		n, err = q.InsertSessionEvent(ctx, sqlcgen.InsertSessionEventParams{
			Platform: string(p), Type: string(t), UserID: userID.String(),
		})
		return err
	})
	if err != nil {
		return false, fmt.Errorf("record %s of user %s on %s: %w", t, userID, p, err)
	}
	return n == 1, nil
}

// FirstForUser records that an event of type t fired for the user and
// reports whether it is the first one ever (events.md, B11). The user must
// exist; the record goes with it.
func (s *Store) FirstForUser(ctx context.Context, userID id.ID, t event.Type) (bool, error) {
	var n int64
	err := s.Write(ctx, func(q *sqlcgen.Queries) (err error) {
		n, err = q.InsertUserEvent(ctx, sqlcgen.InsertUserEventParams{UserID: userID.String(), Type: string(t)})
		return err
	})
	if err != nil {
		return false, fmt.Errorf("record %s of user %s: %w", t, userID, err)
	}
	return n == 1, nil
}
