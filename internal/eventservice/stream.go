// SPDX-License-Identifier: Apache-2.0

package eventservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/settings"
)

// ErrStopped is returned for what the service gets after Run ended.
var ErrStopped = errors.New("the event service has stopped")

// Stream implements connector.Receiver: it takes whether the stream on p
// is live (B11). Going live after the end of a session starts a new one
// with "channel.stream.start"; going offline starts the grace period, and
// only when it ends without the stream being back does the session end
// with "channel.stream.stop" (B8). A report that changes nothing, e.g.
// live again after a restart during the stream, fires nothing (B21).
func (s *Service) Stream(ctx context.Context, p platform.Name, live bool) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("stream: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream of %s: %w", p, ErrStopped)
	}
	ses := s.sessionLocked(p)
	now := time.Now()
	var err error
	switch {
	case live && ses.State == stream.StateLive:
		ses.confirmed, ses.SeenLive = true, now
		err = s.ports.Store.PutStreamSession(ctx, ses.Session)
	case live && ses.State == stream.StateGrace:
		s.stopGraceLocked(ses)
		ses.State, ses.Since, ses.SeenLive = stream.StateLive, now, now
		s.logger.InfoContext(ctx, "stream back within the grace period; the session goes on", "platform", p)
		err = s.ports.Store.PutStreamSession(ctx, ses.Session)
	case live:
		err = s.startLocked(ctx, ses, now)
	case ses.State == stream.StateLive:
		since, whileDown := now, !ses.confirmed
		if whileDown {
			// The stream went offline while the core did not run, at the
			// latest after the core saw it live last (B27).
			since = ses.SeenLive
		}
		ses.confirmed = true
		err = s.offlineLocked(ctx, ses, since, whileDown)
	}
	if err != nil {
		return fmt.Errorf("stream of %s: %w", p, err)
	}
	return nil
}

// sessionLocked returns the session of p, the initial one if p never had
// one. s.mu is held.
func (s *Service) sessionLocked(p platform.Name) *session {
	ses, ok := s.sessions[p]
	if !ok {
		ses = &session{Session: stream.Initial(p), confirmed: true}
		s.sessions[p] = ses
	}
	return ses
}

// startLocked starts a new session: the events that fire once per session
// may fire again (B3), and "channel.stream.start" fires. s.mu is held.
func (s *Service) startLocked(ctx context.Context, ses *session, now time.Time) error {
	next := stream.Session{Platform: ses.Platform, StartedAt: now, State: stream.StateLive, Since: now, SeenLive: now}
	if err := s.ports.Store.StartStreamSession(ctx, next); err != nil {
		return err
	}
	ses.Session, ses.confirmed = next, true
	s.logger.InfoContext(ctx, "stream session started", "platform", ses.Platform)
	s.fireOn(ctx, ses.Platform, eventtype.ChannelStreamStart, eventtype.Payload{Platform: ses.Platform})
	return nil
}

// offlineLocked handles a stream that went offline at since, while the
// core did not run if whileDown is set: it starts the grace period (B8),
// cancels the greetings if no other stream is live (command-engine.md,
// B41), and ends the session if no grace period is left. s.mu is held.
func (s *Service) offlineLocked(ctx context.Context, ses *session, since time.Time, whileDown bool) error {
	if _, err := s.settings(ctx); err != nil {
		return err
	}
	ses.State, ses.Since = stream.StateGrace, since
	if err := s.ports.Store.PutStreamSession(ctx, ses.Session); err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "stream offline", "platform", ses.Platform)
	if !s.anyLiveLocked() {
		s.ports.Engine.CancelEntrance(ctx)
	}
	return s.resumeGraceLocked(ctx, ses, since, whileDown)
}

// resumeGraceLocked lets the grace period of ses, which began at since,
// run for the rest of its time. If none is left, the session ends: with
// "channel.stream.stop" if the stream went offline while the core ran,
// without it if it went offline while the core did not run (whileDown) and
// the grace period ended meanwhile (B27). s.mu is held.
func (s *Service) resumeGraceLocked(ctx context.Context, ses *session, since time.Time, whileDown bool) error {
	cfg, err := s.settings(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	ends := since.Add(cfg.StreamGracePeriod.Std())
	if left := ends.Sub(now); left > 0 {
		s.stopGraceLocked(ses)
		p, timerCtx := ses.Platform, context.WithoutCancel(ctx)
		ses.grace = time.AfterFunc(left, func() { s.graceEnded(timerCtx, p, since) })
		return nil
	}
	if !whileDown {
		return s.endLocked(ctx, ses, now, true)
	}
	s.logger.InfoContext(ctx, "the grace period of a stream ended while the core did not run; the session ends without a stream stop",
		"platform", ses.Platform, "offline_since", since)
	return s.endLocked(ctx, ses, ends, false)
}

// graceEnded ends the session of p whose grace period began at since, if
// the stream is still offline (B8, B25).
func (s *Service) graceEnded(ctx context.Context, p platform.Name, since time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ses, ok := s.sessions[p]
	if s.closed || !ok || ses.State != stream.StateGrace || !ses.Since.Equal(since) {
		return
	}
	ses.grace = nil
	if err := s.endLocked(ctx, ses, time.Now(), true); err != nil {
		s.logger.ErrorContext(ctx, "ending the stream session failed", "platform", p, "error", err)
	}
}

// endLocked ends the session at the time end, with "channel.stream.stop"
// if fire is set. s.mu is held.
func (s *Service) endLocked(ctx context.Context, ses *session, end time.Time, fire bool) error {
	s.stopGraceLocked(ses)
	ses.State, ses.Since = stream.StateOffline, end
	if err := s.ports.Store.PutStreamSession(ctx, ses.Session); err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "stream session ended", "platform", ses.Platform)
	if fire {
		s.fireOn(ctx, ses.Platform, eventtype.ChannelStreamStop, eventtype.Payload{Platform: ses.Platform})
	}
	return nil
}

// stopGraceLocked stops the grace period of ses. s.mu is held.
func (s *Service) stopGraceLocked(ses *session) {
	if ses.grace != nil {
		ses.grace.Stop()
		ses.grace = nil
	}
}

// anyLiveLocked reports whether the stream is live on any platform. s.mu
// is held.
func (s *Service) anyLiveLocked() bool {
	for _, ses := range s.sessions {
		if ses.State == stream.StateLive {
			return true
		}
	}
	return false
}

// settings reads the settings section "events".
func (s *Service) settings(ctx context.Context) (cfg settings.Events, err error) {
	cfg, err = s.ports.Settings(ctx)
	if err != nil {
		return cfg, fmt.Errorf("event settings: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("event settings: %w", err)
	}
	return cfg, nil
}
