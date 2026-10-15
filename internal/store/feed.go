// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/feed"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// feedLimit is how many recent feed events the store keeps (roadmap 5.3).
const feedLimit = int64(200)

// RecordFeedEvent takes ev into the event feed, keeps only the latest
// feedLimit entries, and, if the platform has a session with a start, adds
// the event to that session's statistics (roadmap 5.3). One transaction.
func (s *Store) RecordFeedEvent(ctx context.Context, ev feed.Event, sessionStarted time.Time) error {
	delta := ev.StatsDelta()
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		if err := q.InsertFeedEvent(ctx, sqlcgen.InsertFeedEventParams{
			Platform:    string(ev.Platform),
			Type:        string(ev.Type),
			UserID:      nullID(ev.UserID),
			Login:       ev.Login,
			DisplayName: ev.DisplayName,
			OccurredAt:  ev.Time.UnixMilli(),
			Amount:      nullInt64(ev.Amount),
			Cents:       nullInt64(ev.Cents),
			Tier:        nullInt64(int64(ev.Tier)),
			Text:        sql.NullString{String: ev.Text, Valid: ev.Text != ""},
		}); err != nil {
			return err
		}
		if err := q.PruneFeed(ctx, feedLimit); err != nil {
			return err
		}
		if sessionStarted.IsZero() {
			return nil
		}
		return q.AddSessionStats(ctx, sqlcgen.AddSessionStatsParams{
			Platform:           string(ev.Platform),
			StartedAt:          sessionStarted.UnixMilli(),
			Followers:          delta.Followers,
			Subscribers:        delta.Subscribers,
			Resubscribers:      delta.Resubscribers,
			GiftSubscriptions:  delta.Gifts,
			Raids:              delta.Raids,
			Cheers:             delta.Cheers,
			Donations:          delta.Donations,
			BitsTotal:          delta.BitsTotal,
			DonationCentsTotal: delta.DonationCents,
		})
	})
	if err != nil {
		return fmt.Errorf("record feed event %s: %w", ev.Type, err)
	}
	return nil
}

// LatestFeed returns up to n of the most recent feed events, oldest first
// (roadmap 5.3).
func (s *Store) LatestFeed(ctx context.Context, n int) ([]feed.Event, error) {
	var out []feed.Event
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.LatestFeed(ctx, int64(n))
		if err != nil {
			return err
		}
		out = make([]feed.Event, 0, len(rows))
		for _, r := range rows {
			ev, err := feedEvent(r)
			if err != nil {
				return err
			}
			out = append(out, ev)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("latest feed: %w", err)
	}
	return out, nil
}

// SessionStats returns the statistics of the session of p that started at
// started; ok is false if the session has no statistics yet.
func (s *Store) SessionStats(ctx context.Context, p platform.Name, started time.Time) (feed.SessionStats, bool, error) {
	var st feed.SessionStats
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetSessionStats(ctx, sqlcgen.GetSessionStatsParams{Platform: string(p), StartedAt: started.UnixMilli()})
		if err != nil {
			return err
		}
		st = sessionStats(row)
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return feed.SessionStats{}, false, nil
		}
		return feed.SessionStats{}, false, fmt.Errorf("session statistics of %s: %w", p, err)
	}
	return st, true, nil
}

// CurrentSessionStats returns the statistics of p's current stream session;
// ok is false if p has no current session with a start. A session without
// feed events yet returns zero statistics (roadmap 5.3).
func (s *Store) CurrentSessionStats(ctx context.Context, p platform.Name) (feed.SessionStats, bool, error) {
	sessions, err := s.StreamSessions(ctx)
	if err != nil {
		return feed.SessionStats{}, false, err
	}
	var started time.Time
	found := false
	for _, ss := range sessions {
		if ss.Platform == p {
			found = true
			started = ss.StartedAt
			break
		}
	}
	if !found || started.IsZero() {
		return feed.SessionStats{}, false, nil
	}
	st, ok, err := s.SessionStats(ctx, p, started)
	if err != nil {
		return feed.SessionStats{}, false, err
	}
	if !ok {
		return feed.SessionStats{Platform: p, StartedAt: started}, true, nil
	}
	return st, true, nil
}

// RecentSessions returns up to n of the most recent sessions of p with
// statistics, newest first (roadmap 5.3).
func (s *Store) RecentSessions(ctx context.Context, p platform.Name, n int) ([]feed.SessionStats, error) {
	var out []feed.SessionStats
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListSessionStats(ctx, sqlcgen.ListSessionStatsParams{Platform: string(p), Limit: int64(n)})
		if err != nil {
			return err
		}
		out = make([]feed.SessionStats, 0, len(rows))
		for _, r := range rows {
			out = append(out, sessionStats(r))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("recent sessions of %s: %w", p, err)
	}
	return out, nil
}

// feedEvent converts a stored feed row.
func feedEvent(r sqlcgen.EventFeed) (feed.Event, error) {
	ev := feed.Event{
		Platform:    platform.Name(r.Platform),
		Type:        event.Type(r.Type),
		Login:       r.Login,
		DisplayName: r.DisplayName,
		Time:        fromMillis(r.OccurredAt),
	}
	var err error
	if ev.UserID, err = parseNullID(r.UserID); err != nil {
		return feed.Event{}, err
	}
	ev.Amount = r.Amount.Int64
	ev.Cents = r.Cents.Int64
	ev.Tier = int(r.Tier.Int64)
	ev.Text = r.Text.String
	return ev, nil
}

// sessionStats converts a stored session statistics row.
func sessionStats(r sqlcgen.SessionStat) feed.SessionStats {
	return feed.SessionStats{
		Platform:      platform.Name(r.Platform),
		StartedAt:     fromMillis(r.StartedAt),
		Followers:     r.Followers,
		Subscribers:   r.Subscribers,
		Resubscribers: r.Resubscribers,
		Gifts:         r.GiftSubscriptions,
		Raids:         r.Raids,
		Cheers:        r.Cheers,
		Donations:     r.Donations,
		BitsTotal:     r.BitsTotal,
		DonationCents: r.DonationCentsTotal,
	}
}

// nullInt64 stores a zero value as NULL.
func nullInt64(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}
