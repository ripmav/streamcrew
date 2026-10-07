// SPDX-License-Identifier: Apache-2.0

// Package eventservice is the event service (spec events.md; Code-ADR-0011,
// point 4): it takes what the platform adapters receive, applies the rules
// of events.md, publishes the events on the bus and triggers the commands.
//
//   - Chat messages fire the events of the chat user and trigger the chat
//     command their trigger names and the greetings (B13; commands.md, B16;
//     command-engine.md, B41).
//   - Events fire with their platform-neutral type after their own (B2),
//     at most once per stream session and user where the catalog says so
//     (B3, B11), and mass gifts by the threshold of the profile (B5).
//   - The stream session of each platform outlasts short breaks and a
//     restart of the core (B8, B21, B27).
//
// It hands everything over in the order it came, and does not wait for the
// commands it triggers (command-engine.md, B16). Service implements
// connector.Receiver and is a runnable of the supervisor (Code-ADR-0004).
package eventservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/settings"
)

var (
	// ErrInvalidOption is returned by New for a missing port or an option
	// without a valid value.
	ErrInvalidOption = errors.New("invalid option")
	// ErrAlreadyRunning is returned by a second call of Run.
	ErrAlreadyRunning = errors.New("the event service runs already")
	// ErrNotReceived is wrapped by the error of an event the service does
	// not take that way, e.g. a chat message handed over as an event.
	ErrNotReceived = errors.New("not received this way")
)

// Store keeps the users, the stream sessions and the events that fire once
// (B3, B11, B21); *store.Store implements it.
type Store interface {
	// UpsertIdentity returns the user of the account, created if new, with
	// the names of ident.
	UpsertIdentity(ctx context.Context, ident user.Identity) (u user.User, created bool, err error)
	// SetRoles sets the roles of the account in the channel.
	SetRoles(ctx context.Context, p platform.Name, platformUserID string, roles role.Set) error
	// StreamSessions returns the stored sessions.
	StreamSessions(ctx context.Context) ([]stream.Session, error)
	// PutStreamSession stores the session of its platform.
	PutStreamSession(ctx context.Context, s stream.Session) error
	// StartStreamSession stores a new session of its platform and forgets
	// the events of the session before.
	StartStreamSession(ctx context.Context, s stream.Session) error
	// FirstInSession records an event of type t for the user in the session
	// of p and reports whether it is the first one.
	FirstInSession(ctx context.Context, p platform.Name, t event.Type, userID id.ID) (bool, error)
	// FirstForUser records an event of type t for the user and reports
	// whether it is the first one ever.
	FirstForUser(ctx context.Context, userID id.ID, t event.Type) (bool, error)
}

// Commands finds the commands to trigger; *command.Service implements it.
type Commands interface {
	// Recognize finds the chat command a chat message triggers
	// (commands.md, B16).
	Recognize(ctx context.Context, message string) (command.Recognition, error)
	// EventCommand returns the event command of type t; ok is false if
	// there is none (commands.md, B20).
	EventCommand(ctx context.Context, t event.Type) (cmd command.Command, ok bool, err error)
	// Command returns a command, e.g. the entrance command of a user.
	Command(ctx context.Context, commandID id.ID) (command.Command, error)
}

// Engine runs the commands; *engine.Engine implements it.
type Engine interface {
	// Submit triggers a command without waiting for the decision
	// (command-engine.md, B16).
	Submit(ctx context.Context, req engine.Request, done func(engine.Result, error)) error
	// CancelEntrance cancels the greetings that have not ended
	// (command-engine.md, B41).
	CancelEntrance(ctx context.Context)
}

// Publisher publishes events; *event.Bus implements it.
type Publisher interface {
	Publish(ctx context.Context, e event.Envelope) error
}

// Ports are what the service needs. All are required.
type Ports struct {
	Store     Store
	Commands  Commands
	Engine    Engine
	Publisher Publisher
	// Settings reads the settings section "events" (B10) whenever a rule
	// needs it, so changes apply at once.
	Settings func(ctx context.Context) (settings.Events, error)
}

// Option configures a Service. An option without a valid value makes New
// fail with ErrInvalidOption; to keep a default, leave the option out.
type Option func(*Service) error

// WithLogger sets the logger; without it, the service logs nothing.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) error {
		if l == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		s.logger = l
		return nil
	}
}

// Service is the event service. It is safe for concurrent use; it hands
// over one thing at a time, in the order of the calls.
type Service struct {
	ports  Ports
	logger *slog.Logger

	// mu orders everything the service takes and guards the fields below.
	mu      sync.Mutex
	started bool
	closed  bool
	// sessions are the stream sessions by platform.
	sessions map[platform.Name]*session
}

// session is the stream session of a platform with what only the running
// core knows about it.
type session struct {
	stream.Session
	// confirmed is false for a session that was live when the core
	// stopped, until the adapter says whether it still is (B21, B27).
	confirmed bool
	// grace ends the grace period; nil while none runs.
	grace *time.Timer
}

// New returns a service with the ports p. It loads the stored stream
// sessions and resumes their grace periods; one that ended while the core
// did not run ends the session without "channel.stream.stop" (B27).
func New(ctx context.Context, p Ports, opts ...Option) (*Service, error) {
	switch {
	case p.Store == nil, p.Commands == nil, p.Engine == nil, p.Publisher == nil, p.Settings == nil:
		return nil, fmt.Errorf("new event service: %w: missing port", ErrInvalidOption)
	}
	s := &Service{ports: p, logger: slog.New(slog.DiscardHandler), sessions: make(map[platform.Name]*session)}
	var errs []error
	for _, opt := range opts {
		errs = append(errs, opt(s))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("new event service: %w", err)
	}
	stored, err := p.Store.StreamSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("new event service: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range stored {
		ses := &session{Session: ss, confirmed: ss.State != stream.StateLive}
		s.sessions[ss.Platform] = ses
		if ss.State == stream.StateGrace {
			if err := s.resumeGraceLocked(ctx, ses, ss.Since, true); err != nil {
				return nil, fmt.Errorf("new event service: %w", err)
			}
		}
	}
	return s, nil
}

// Run keeps the service until ctx ends (Code-ADR-0004). Then it stops the
// grace periods and remembers that the live streams were live until now,
// so that a restart can tell how long they were offline (B27). A second
// call returns ErrAlreadyRunning.
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("run event service: %w", ErrAlreadyRunning)
	}
	s.started = true
	s.mu.Unlock()

	<-ctx.Done()
	stopCtx := context.WithoutCancel(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	now := time.Now()
	var errs []error
	for _, ses := range s.sessions {
		if ses.grace != nil {
			ses.grace.Stop()
			ses.grace = nil
		}
		if ses.State == stream.StateLive && ses.confirmed {
			ses.SeenLive = now
			errs = append(errs, s.ports.Store.PutStreamSession(stopCtx, ses.Session))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("event service: remember the live streams: %w", err)
	}
	return nil
}

// Application publishes an event of the core, "app.started" or
// "app.stopping", with its payload and triggers its event command (B12).
func (s *Service) Application(ctx context.Context, t event.Type, payload any) error {
	switch t {
	case eventtype.AppStarted, eventtype.AppStopping:
	default:
		return fmt.Errorf("application event %s: %w", t, ErrNotReceived)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src := event.Source{Kind: event.SourceSystem, Name: "app"}
	s.publish(ctx, event.New(src, t, payload))
	s.triggerEvent(ctx, t, engine.Params{})
	return nil
}

// publish publishes e; a failure is logged.
func (s *Service) publish(ctx context.Context, e event.Envelope) {
	if err := s.ports.Publisher.Publish(ctx, e); err != nil {
		s.logger.ErrorContext(ctx, "publishing an event failed", "type", e.Type, "error", err)
	}
}

// triggerEvent triggers the event command of type t with the parameters p,
// if there is one (B12). s.mu is held, so the commands are submitted in the
// order of the events.
func (s *Service) triggerEvent(ctx context.Context, t event.Type, p engine.Params) {
	cmd, ok, err := s.ports.Commands.EventCommand(ctx, t)
	if err != nil {
		s.logger.ErrorContext(ctx, "finding the event command failed", "type", t, "error", err)
		return
	}
	if !ok {
		return
	}
	s.submit(ctx, engine.Request{Command: cmd, Source: engine.SourceEvent, Params: p, Event: t})
}

// submit submits req to the engine without waiting for the decision; the
// outcome is logged. s.mu is held.
func (s *Service) submit(ctx context.Context, req engine.Request) {
	name := req.Command.Name
	err := s.ports.Engine.Submit(ctx, req, func(r engine.Result, err error) {
		if err != nil {
			s.logger.WarnContext(ctx, "triggering a command failed", "command", name, "error", err)
			return
		}
		s.logger.DebugContext(ctx, "command triggered", "command", name, "outcome", r.Outcome)
	})
	if err != nil {
		s.logger.WarnContext(ctx, "triggering a command failed", "command", name, "error", err)
	}
}

// Compile-time check that the service is the receiver of the adapters.
var _ connector.Receiver = (*Service)(nil)

// RegisterEvents adds the platform events the service publishes to c: the
// platform-neutral types and the platform-specific ones with a neutral
// type, each with the payload eventtype.Payload (B9).
func RegisterEvents(c *event.Catalog) error {
	var errs []error
	for _, d := range eventtype.All() {
		if _, ok := eventtype.ShapeOf(d.Type); ok {
			errs = append(errs, event.Register[eventtype.Payload](c, d.Type))
		}
	}
	return errors.Join(errs...)
}
