// SPDX-License-Identifier: MIT

// Package obs is the OBS Studio connection of the core (roadmap 7.4,
// spec overlays.md, B42 to B45): the obs-websocket v5 client (goobs),
// the events of the connection on the bus (B44) and the operations the
// streaming_software action runs (B42, B55). The connection starts with
// the core; without an OBS server at the start it is a clear, logged
// fact, not an error of the core, and it is given up (no retries). A
// dropped connection reconnects every five seconds (B43).
package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/events"
	"github.com/andreykaipov/goobs/api/requests/filters"
	"github.com/andreykaipov/goobs/api/requests/sceneitems"
	"github.com/andreykaipov/goobs/api/requests/scenes"
	"github.com/gorilla/websocket"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/netguard"
)

// Reconnect is the interval the service reconnects with after a dropped
// connection (B43).
const Reconnect = 5 * time.Second

// Source is the change of the kind set_source of the
// streaming_software action (B42): the source of the current scene,
// and the members to change; a nil member stays as it is.
type Source struct {
	// Source is the name of the source of the current program scene.
	Source string
	// Visible sets the visibility of the scene item; nil keeps it.
	Visible *bool
	// X and Y set the position of the scene item; nil keeps it.
	X, Y *float64
}

// Ports are the dependencies of the service.
type Ports struct {
	// Host is the host of the OBS obs-websocket (obs-host, B43).
	Host string
	// Port is the port of the OBS obs-websocket (obs-port, B43).
	Port int
	// Password is the password of the OBS obs-websocket (obs-password,
	// B43); empty for a connection without a password.
	Password string
	// Bus is where the service publishes its events (B44).
	Bus *event.Bus
	// Dialer guards the connection in the server mode (Code-ADR-0019,
	// point 4); without Protect it connects anywhere.
	Dialer netguard.Dialer
	// Logger records the connection and the operations.
	Logger *slog.Logger
}

// Service is the OBS connection of the core (B43, B44).
type Service struct {
	ports Ports

	mu   sync.Mutex
	conn *goobs.Client
	// The state of the connection for the "once per change" of B44:
	// the last seen stream and record state and the last program
	// scene, seeded at every (re)connection.
	streamActive bool
	recordActive bool
	lastScene    string

	reconnect time.Duration
}

// New returns the service with its ports; the app runs it with the
// supervisor (Code-ADR-0004).
func New(p Ports) *Service {
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{ports: p, reconnect: Reconnect}
}

// Run connects to OBS and runs the connection until ctx ends (B43). A
// failed first connection gives up with an info log; a dropped
// connection reconnects every five seconds until the context ends.
func (s *Service) Run(ctx context.Context) error {
	client, err := s.connect(ctx)
	if err != nil {
		s.giveUp(ctx, err)
		return nil
	}
	// The client is published only after the seed, so an action never sees
	// a connection whose state is unknown.
	if !s.ready(ctx, client) {
		s.giveUp(ctx, errors.New("the initial OBS state is unavailable"))
		return nil
	}
	s.setClient(client)
	s.ports.Logger.InfoContext(ctx, "OBS connection established", "host", s.ports.Host, "port", s.ports.Port)
	defer s.setClient(nil)
	// The loop body runs one session; a dropped connection reconnects
	// after the interval (B43), a failing reconnect tries again.
	// shortcut: a drop in the middle of an in-flight request stalls the
	// reconnection until the response timeout of ten seconds, because
	// goobs holds its client mutex until the timer fires — the next try
	// succeeds, so the stall is bounded, not lost.
	for s.session(ctx, client) {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(s.reconnect):
		}
		retry, err := s.connect(ctx)
		if err != nil {
			s.ports.Logger.WarnContext(ctx, "the OBS reconnect failed; trying again", "host", s.ports.Host, "error", err)
			continue
		}
		if !s.ready(ctx, retry) {
			s.ports.Logger.WarnContext(ctx, "the OBS reconnect failed; trying again", "host", s.ports.Host)
			continue
		}
		client = retry
		s.setClient(client)
		s.ports.Logger.InfoContext(ctx, "OBS connection re-established", "host", s.ports.Host, "port", s.ports.Port)
	}
	return nil
}

// giveUp logs the failed first connection (B43): the core runs on
// without OBS, and the streaming_software action reports the missing
// connection per action (B55).
func (s *Service) giveUp(ctx context.Context, err error) {
	s.ports.Logger.InfoContext(ctx, "no OBS connection; giving up until the next start (B43)",
		"host", s.ports.Host, "port", s.ports.Port, "error", err)
}

// connect dials and identifies against the OBS server (B43); the
// response timeout of the client keeps a dead connection from holding a
// request for longer than ten seconds.
func (s *Service) connect(_ context.Context) (*goobs.Client, error) {
	endpoint := net.JoinHostPort(s.ports.Host, strconv.Itoa(s.ports.Port))
	client, err := goobs.New(endpoint,
		goobs.WithPassword(s.ports.Password),
		goobs.WithResponseTimeoutDuration(10*time.Second),
		goobs.WithDialer(&websocket.Dialer{NetDialContext: s.ports.Dialer.DialContext}),
		goobs.WithLogger(slogLogger{logger: s.ports.Logger}),
	)
	if err != nil {
		return nil, fmt.Errorf("obs %s: %w", endpoint, err)
	}
	return client, nil
}

// ready seeds the connection state with the current OBS state (B44:
// "once per change" needs the starting point, and a reconnect must not
// repeat the state it inherits); it reports whether the seed worked and
// disconnects the client if it did not.
func (s *Service) ready(ctx context.Context, client *goobs.Client) bool {
	err := s.seed(ctx, client)
	if err != nil {
		s.ports.Logger.WarnContext(ctx, "the OBS state is unavailable", "error", err)
		_ = client.Disconnect()
		return false
	}
	return true
}

// seed reads the current stream, record and scene state of OBS (B44).
func (s *Service) seed(_ context.Context, client *goobs.Client) error {
	stream, err := client.Stream.GetStreamStatus()
	if err != nil {
		return fmt.Errorf("the stream status: %w", err)
	}
	record, err := client.Record.GetRecordStatus()
	if err != nil {
		return fmt.Errorf("the record status: %w", err)
	}
	scene, err := client.Scenes.GetCurrentProgramScene()
	if err != nil {
		return fmt.Errorf("the current scene: %w", err)
	}
	s.mu.Lock()
	s.streamActive = stream.OutputActive
	s.recordActive = record.OutputActive
	s.lastScene = scene.SceneName
	s.mu.Unlock()
	return nil
}

// setClient sets or clears the active connection; the operations of
// the action use it (B55).
func (s *Service) setClient(client *goobs.Client) {
	s.mu.Lock()
	s.conn = client
	s.mu.Unlock()
}

// session runs the connection of client until it drops or ctx ends;
// it reports whether the connection dropped, so that Run reconnects
// (B43).
func (s *Service) session(ctx context.Context, client *goobs.Client) bool {
	done := make(chan struct{})
	go func() {
		// Listen returns when the client closes its events, that is when
		// the connection dropped.
		client.Listen(func(e any) { s.handleEvent(ctx, client, e) })
		close(done)
	}()
	select {
	case <-ctx.Done():
		// The core is shutting down; the process exit closes the
		// connection. shortcut: the client's Disconnect is not safe to
		// call while the connection delivers events (goobs closes its
		// opcode channel under the reader's send), and the remaining
		// library goroutines are cleaned up by the exit.
		return false
	case <-done:
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
}

// handleEvent runs one OBS event of the connection as a bus event
// (B44), regardless of who caused the change.
func (s *Service) handleEvent(ctx context.Context, client *goobs.Client, e any) {
	switch ev := e.(type) {
	case *events.StreamStateChanged:
		s.stateChanged(ctx, ev.OutputState, "streaming", &s.streamActive, func(active bool) {
			typ := eventtype.ObsStreamStopped
			if active {
				typ = eventtype.ObsStreamStarted
			}
			s.publish(ctx, typ, eventtype.Payload{})
		})
	case *events.RecordStateChanged:
		s.stateChanged(ctx, ev.OutputState, "recording", &s.recordActive, func(active bool) {
			typ := eventtype.ObsRecordingStopped
			if active {
				typ = eventtype.ObsRecordingStarted
			}
			p := eventtype.Payload{}
			// The path is the value of obs.recording.stopped (B44); the
			// start carries none.
			if !active {
				p.Details.ObsFile = &eventtype.ObsFile{Path: ev.OutputPath}
			}
			s.publish(ctx, typ, p)
		})
	case *events.ReplayBufferSaved:
		s.publish(ctx, eventtype.ObsReplaySaved, eventtype.Payload{
			Details: eventtype.Details{ObsFile: &eventtype.ObsFile{Path: ev.SavedReplayPath}},
		})
	case *events.CurrentProgramSceneChanged:
		s.mu.Lock()
		previous, current := s.lastScene, ev.SceneName
		s.lastScene = current
		s.mu.Unlock()
		s.publish(ctx, eventtype.ObsSceneChanged, eventtype.Payload{
			Details: eventtype.Details{ObsScene: &eventtype.ObsScene{Scene: current, Previous: previous}},
		})
	case *events.SceneTransitionStarted:
		s.publish(ctx, eventtype.ObsTransitionStarted, eventtype.Payload{
			Details: eventtype.Details{ObsTransition: &eventtype.ObsTransition{Name: ev.TransitionName}},
		})
	case *events.SceneTransitionEnded:
		s.publish(ctx, eventtype.ObsTransitionEnded, eventtype.Payload{
			Details: eventtype.Details{ObsTransition: &eventtype.ObsTransition{Name: ev.TransitionName}},
		})
	case *events.SceneItemEnableStateChanged:
		s.sourceVisibility(ctx, client, ev)
	case *events.SourceFilterEnableStateChanged:
		s.publish(ctx, eventtype.ObsFilterVisibilityChanged, eventtype.Payload{
			Details: eventtype.Details{ObsFilter: &eventtype.ObsFilter{
				Source:  ev.SourceName,
				Filter:  ev.FilterName,
				Visible: ev.FilterEnabled,
			}},
		})
	}
}

// stateChanged runs the state change of stream and record (B44): the
// OBS states starting, stopping and paused are ignored, and each
// change publishes once, so a repeated state publishes nothing.
func (s *Service) stateChanged(_ context.Context, state, activeState string, active *bool, publish func(active bool)) {
	var on bool
	switch state {
	case activeState:
		on = true
	case "stopped":
	default:
		return
	}
	s.mu.Lock()
	changed := on != *active
	*active = on
	s.mu.Unlock()
	if changed {
		publish(on)
	}
}

// sourceVisibility runs obs.source.visibility_changed (B44): the OBS
// event names the scene item, not the source, so the source name comes
// from the item; without the item (it left in between) the event is
// dropped with a debug log.
func (s *Service) sourceVisibility(ctx context.Context, client *goobs.Client, ev *events.SceneItemEnableStateChanged) {
	res, err := client.SceneItems.GetSceneItemSource(sceneitems.NewGetSceneItemSourceParams().
		WithSceneName(ev.SceneName).WithSceneItemId(ev.SceneItemId))
	if err != nil {
		s.ports.Logger.DebugContext(ctx, "the source of the changed scene item is unavailable",
			"scene", ev.SceneName, "scene_item", ev.SceneItemId, "error", err)
		return
	}
	s.publish(ctx, eventtype.ObsSourceVisibilityChanged, eventtype.Payload{
		Details: eventtype.Details{ObsSource: &eventtype.ObsSource{
			Scene:   ev.SceneName,
			Source:  res.SourceName,
			Visible: ev.SceneItemEnabled,
		}},
	})
}

// publish runs typ as a bus event with its details (B44).
func (s *Service) publish(ctx context.Context, typ event.Type, p eventtype.Payload) {
	env := event.New(event.Source{Kind: event.SourceIntegration, Name: "obs"}, typ, p)
	if err := s.ports.Bus.Publish(ctx, env); err != nil {
		s.ports.Logger.ErrorContext(ctx, "publishing the OBS event failed", "type", typ, "error", err)
	}
}

// ErrNotConnected is the error of an operation without an active
// connection (B55): a clear message, not a wait.
var ErrNotConnected = errors.New("obs: no active connection to OBS")

// Connected reports whether the connection is established (B55: the
// action fails without it and does not wait for it).
func (s *Service) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn != nil
}

// client returns the active connection or ErrNotConnected (B55).
func (s *Service) client() (*goobs.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil, ErrNotConnected
	}
	return s.conn, nil
}

// SetScene sets the current program scene to name (B42).
func (s *Service) SetScene(_ context.Context, name string) error {
	client, err := s.client()
	if err != nil {
		return err
	}
	if _, err := client.Scenes.SetCurrentProgramScene(scenes.NewSetCurrentProgramSceneParams().WithSceneName(name)); err != nil {
		return fmt.Errorf("obs: the scene %q: %w", name, err)
	}
	return nil
}

// StartStream starts the stream output of OBS (B42).
func (s *Service) StartStream(_ context.Context) error {
	client, err := s.client()
	if err != nil {
		return err
	}
	if _, err := client.Stream.StartStream(); err != nil {
		return fmt.Errorf("obs: start the stream: %w", err)
	}
	return nil
}

// StopStream stops the stream output of OBS (B42).
func (s *Service) StopStream(_ context.Context) error {
	client, err := s.client()
	if err != nil {
		return err
	}
	if _, err := client.Stream.StopStream(); err != nil {
		return fmt.Errorf("obs: stop the stream: %w", err)
	}
	return nil
}

// StartRecording starts the record output of OBS (B42).
func (s *Service) StartRecording(_ context.Context) error {
	client, err := s.client()
	if err != nil {
		return err
	}
	if _, err := client.Record.StartRecord(); err != nil {
		return fmt.Errorf("obs: start the recording: %w", err)
	}
	return nil
}

// StopRecording stops the record output of OBS (B42).
func (s *Service) StopRecording(_ context.Context) error {
	client, err := s.client()
	if err != nil {
		return err
	}
	if _, err := client.Record.StopRecord(); err != nil {
		return fmt.Errorf("obs: stop the recording: %w", err)
	}
	return nil
}

// SaveReplay saves the replay buffer of OBS (B42); the path of the
// saved file arrives with obs.replay.saved (B44).
func (s *Service) SaveReplay(_ context.Context) error {
	client, err := s.client()
	if err != nil {
		return err
	}
	if _, err := client.Outputs.SaveReplayBuffer(); err != nil {
		return fmt.Errorf("obs: save the replay buffer: %w", err)
	}
	return nil
}

// SetSource changes the scene item of source in the current program
// scene (B42): the visibility, the position, or both; a source that is
// not in the scene is a clear error.
func (s *Service) SetSource(_ context.Context, ch Source) error {
	client, err := s.client()
	if err != nil {
		return err
	}
	scene, err := client.Scenes.GetCurrentProgramScene()
	if err != nil {
		return fmt.Errorf("obs: the current scene: %w", err)
	}
	items, err := client.SceneItems.GetSceneItemList(sceneitems.NewGetSceneItemListParams().WithSceneName(scene.SceneName))
	if err != nil {
		return fmt.Errorf("obs: the items of the scene %q: %w", scene.SceneName, err)
	}
	var id *int
	for _, item := range items.SceneItems {
		if item.SourceName == ch.Source {
			id = &item.SceneItemID
			break
		}
	}
	if id == nil {
		return fmt.Errorf("obs: the source %q is not in the scene %q", ch.Source, scene.SceneName)
	}
	if ch.Visible != nil {
		if _, err := client.SceneItems.SetSceneItemEnabled(sceneitems.NewSetSceneItemEnabledParams().
			WithSceneName(scene.SceneName).WithSceneItemId(*id).WithSceneItemEnabled(*ch.Visible)); err != nil {
			return fmt.Errorf("obs: the visibility of the source %q: %w", ch.Source, err)
		}
	}
	if ch.X != nil || ch.Y != nil {
		// The position changes only the position; the rest of the
		// transform (rotation, scale, bounds) stays as it is.
		transform, err := client.SceneItems.GetSceneItemTransform(sceneitems.NewGetSceneItemTransformParams().
			WithSceneName(scene.SceneName).WithSceneItemId(*id))
		if err != nil {
			return fmt.Errorf("obs: the transform of the source %q: %w", ch.Source, err)
		}
		if transform.SceneItemTransform == nil {
			return fmt.Errorf("obs: the source %q has no transform", ch.Source)
		}
		if ch.X != nil {
			transform.SceneItemTransform.PositionX = *ch.X
		}
		if ch.Y != nil {
			transform.SceneItemTransform.PositionY = *ch.Y
		}
		if _, err := client.SceneItems.SetSceneItemTransform(sceneitems.NewSetSceneItemTransformParams().
			WithSceneName(scene.SceneName).WithSceneItemId(*id).
			WithSceneItemTransform(transform.SceneItemTransform)); err != nil {
			return fmt.Errorf("obs: the position of the source %q: %w", ch.Source, err)
		}
	}
	return nil
}

// SetFilter sets the visibility of the filter of the source (B42).
func (s *Service) SetFilter(_ context.Context, source, filter string, visible bool) error {
	client, err := s.client()
	if err != nil {
		return err
	}
	if _, err := client.Filters.SetSourceFilterEnabled(filters.NewSetSourceFilterEnabledParams().
		WithSourceName(source).WithFilterName(filter).WithFilterEnabled(visible)); err != nil {
		return fmt.Errorf("obs: the filter %q of the source %q: %w", filter, source, err)
	}
	return nil
}

// slogLogger adapts the slog logger to the logger interface of goobs;
// the connection diagnostics of the library stay at the debug level,
// the facts the service publishes itself.
type slogLogger struct{ logger *slog.Logger }

// Printf implements the logger interface of goobs. The message of the
// library is dynamic, so it goes in an attribute, not the msg (sloglint
// static-msg).
func (s slogLogger) Printf(format string, args ...any) {
	s.logger.Debug("goobs", "message", fmt.Sprintf(format, args...))
}
