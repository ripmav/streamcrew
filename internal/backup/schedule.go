// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// recheckInterval bounds how long the scheduler sleeps, so that changed
// settings take effect within this time.
const recheckInterval = time.Hour

// Policy says which automatic backups to keep: the newest one of each of the
// last Daily days, Weekly ISO weeks and Monthly months that have backups.
// Backups of other kinds are never removed.
type Policy struct {
	Daily, Weekly, Monthly int
}

// Expired returns the automatic backups the policy does not keep. Day, week
// and month boundaries are those of loc.
func Expired(backups []Info, p Policy, loc *time.Location) []Info {
	var scheduled []Info
	for _, b := range backups {
		if b.Manifest.Kind == KindScheduled {
			scheduled = append(scheduled, b)
		}
	}
	// List returns newest first; keep that order for the buckets.
	keep := make(map[string]bool)
	bucket := func(n int, key func(time.Time) string) {
		seen := make(map[string]bool)
		for _, b := range scheduled {
			if len(seen) >= n {
				return
			}
			k := key(b.Manifest.CreatedAt.In(loc))
			if !seen[k] {
				seen[k] = true
				keep[b.Path] = true
			}
		}
	}
	bucket(p.Daily, func(t time.Time) string { return t.Format("2006-01-02") })
	bucket(p.Weekly, func(t time.Time) string {
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	})
	bucket(p.Monthly, func(t time.Time) string { return t.Format("2006-01") })

	var expired []Info
	for _, b := range scheduled {
		if !keep[b.Path] {
			expired = append(expired, b)
		}
	}
	return expired
}

// Schedule is the configuration of the automatic backups.
type Schedule struct {
	Enabled      bool
	Hour, Minute int
	Policy       Policy
	Location     *time.Location
}

// Scheduler makes the daily automatic backup of a profile and removes
// expired ones. It is a supervisor.Runnable (Code-ADR-0004).
type Scheduler struct {
	src      Source
	dir      string
	req      Request
	schedule func(ctx context.Context) (Schedule, error)
	logger   *slog.Logger
}

// NewScheduler returns a scheduler for the profile in req. schedule is
// called before every decision, so that changed settings apply.
func NewScheduler(src Source, dir string, req Request, schedule func(ctx context.Context) (Schedule, error), logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	req.Kind = KindScheduled
	return &Scheduler{src: src, dir: dir, req: req, schedule: schedule, logger: logger}
}

// Run makes a backup once a day at the configured time. If the core was not
// running at that time, the backup is made as soon as it runs.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		wait, err := s.tick(ctx)
		if err != nil {
			return err
		}
		timer := time.NewTimer(min(wait, recheckInterval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// tick makes a backup if one is due and returns the time until the next.
func (s *Scheduler) tick(ctx context.Context) (time.Duration, error) {
	sch, err := s.schedule(ctx)
	if err != nil {
		return 0, fmt.Errorf("backup schedule: %w", err)
	}
	if !sch.Enabled {
		return recheckInterval, nil
	}
	now := time.Now().In(sch.Location)
	due := time.Date(now.Year(), now.Month(), now.Day(), sch.Hour, sch.Minute, 0, 0, sch.Location)
	if now.Before(due) {
		return due.Sub(now), nil
	}

	backups, err := List(s.dir, s.req.ProfileID)
	if err != nil {
		return 0, err
	}
	if !madeSince(backups, due) {
		info, err := Create(ctx, s.src, s.dir, s.req)
		if err != nil {
			return 0, err
		}
		s.logger.InfoContext(ctx, "automatic backup created", "path", info.Path, "size", info.Size)
		backups = append([]Info{info}, backups...)
		if err := s.prune(ctx, backups, sch); err != nil {
			return 0, err
		}
	}
	next := time.Date(now.Year(), now.Month(), now.Day()+1, sch.Hour, sch.Minute, 0, 0, sch.Location)
	return next.Sub(now), nil
}

func (s *Scheduler) prune(ctx context.Context, backups []Info, sch Schedule) error {
	var errs []error
	for _, b := range Expired(backups, sch.Policy, sch.Location) {
		if err := os.Remove(b.Path); err != nil {
			errs = append(errs, fmt.Errorf("remove expired backup: %w", err))
			continue
		}
		s.logger.InfoContext(ctx, "expired backup removed", "path", b.Path)
	}
	return errors.Join(errs...)
}

// madeSince reports whether an automatic backup exists from t or later.
func madeSince(backups []Info, t time.Time) bool {
	for _, b := range backups {
		if b.Manifest.Kind == KindScheduled && !b.Manifest.CreatedAt.Before(t) {
			return true
		}
	}
	return false
}
