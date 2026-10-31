// SPDX-License-Identifier: MIT

package obs

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

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
	conn *client
	// The state of the connection for the "once per change" of B44:
	// the last seen stream and record state and the last program
	// scene, seeded at every (re)connection.
	streamActive bool
	recordActive bool
	lastScene    string

	reconnect   time.Duration
	dialTimeout time.Duration
}

// New returns the service with its ports; the app runs it with the
// supervisor (Code-ADR-0004).
func New(p Ports) *Service {
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{ports: p, reconnect: Reconnect, dialTimeout: dialTimeout}
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

// connect dials and identifies against the OBS server (B43).
func (s *Service) connect(ctx context.Context) (*client, error) {
	return dial(ctx, s.ports.Host, s.ports.Port, s.ports.Password, s.ports.Dialer, s.ports.Logger, s.dialTimeout)
}

// ready seeds the connection state with the current OBS state (B44:
// "once per change" needs the starting point, and a reconnect must not
// repeat the state it inherits); it reports whether the seed worked and
// closes the connection if it did not.
func (s *Service) ready(ctx context.Context, c *client) bool {
	err := s.seed(ctx, c)
	if err != nil {
		s.ports.Logger.WarnContext(ctx, "the OBS state is unavailable", "error", err)
		c.close()
		return false
	}
	return true
}

// seed reads the current stream, record and scene state of OBS (B44).
func (s *Service) seed(ctx context.Context, c *client) error {
	var stream, record struct {
		OutputActive bool `json:"outputActive"`
	}
	if err := c.request(ctx, "GetStreamStatus", nil, &stream); err != nil {
		return fmt.Errorf("the stream status: %w", err)
	}
	if err := c.request(ctx, "GetRecordStatus", nil, &record); err != nil {
		return fmt.Errorf("the record status: %w", err)
	}
	var scene sceneChangedEvent
	if err := c.request(ctx, "GetCurrentProgramScene", nil, &scene); err != nil {
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
func (s *Service) setClient(c *client) {
	s.mu.Lock()
	s.conn = c
	s.mu.Unlock()
}

// session runs the connection of client until it ends; it reports
// whether the connection dropped, so that Run reconnects (B43). The
// consumer of the events runs until the reader of the connection ends.
func (s *Service) session(ctx context.Context, c *client) bool {
	go func() {
		for ev := range c.events {
			s.handleEvent(ctx, c, ev)
		}
	}()
	// The reader ends when the connection drops or the context ends
	// (its read fails on both).
	<-c.done
	// The connection is gone: clear it, so that an operation in the
	// window until the (re)connection gets ErrNotConnected instead of
	// the dead client (B55, Review 346-3).
	s.setClient(nil)
	select {
	case <-ctx.Done():
		// The core is shutting down; the process exit ends the
		// connection.
		return false
	default:
		c.close()
		return true
	}
}

// handleEvent runs one OBS event of the connection as a bus event
// (B44), regardless of who caused the change.
func (s *Service) handleEvent(ctx context.Context, c *client, ev *eventMsg) {
	switch ev.Type {
	case "StreamStateChanged":
		var d streamStateEvent
		_ = json.Unmarshal(ev.Data, &d)
		s.stateChanged(ctx, d.OutputState, &s.streamActive, func(active bool) {
			typ := eventtype.ObsStreamStopped
			if active {
				typ = eventtype.ObsStreamStarted
			}
			s.publish(ctx, typ, eventtype.Payload{})
		})
	case "RecordStateChanged":
		var d recordStateEvent
		_ = json.Unmarshal(ev.Data, &d)
		s.stateChanged(ctx, d.OutputState, &s.recordActive, func(active bool) {
			typ := eventtype.ObsRecordingStopped
			if active {
				typ = eventtype.ObsRecordingStarted
			}
			p := eventtype.Payload{}
			// The path is the value of obs.recording.stopped (B44); the
			// start carries none.
			if !active {
				p.Details.ObsFile = &eventtype.ObsFile{Path: d.OutputPath}
			}
			s.publish(ctx, typ, p)
		})
	case "ReplayBufferSaved":
		var d replaySavedEvent
		_ = json.Unmarshal(ev.Data, &d)
		s.publish(ctx, eventtype.ObsReplaySaved, eventtype.Payload{
			Details: eventtype.Details{ObsFile: &eventtype.ObsFile{Path: d.SavedReplayPath}},
		})
	case "CurrentProgramSceneChanged":
		var d sceneChangedEvent
		_ = json.Unmarshal(ev.Data, &d)
		s.mu.Lock()
		previous, current := s.lastScene, d.SceneName
		s.lastScene = current
		s.mu.Unlock()
		s.publish(ctx, eventtype.ObsSceneChanged, eventtype.Payload{
			Details: eventtype.Details{ObsScene: &eventtype.ObsScene{Scene: current, Previous: previous}},
		})
	case "SceneTransitionStarted", "SceneTransitionEnded":
		var d transitionEvent
		_ = json.Unmarshal(ev.Data, &d)
		typ := eventtype.ObsTransitionStarted
		if ev.Type == "SceneTransitionEnded" {
			typ = eventtype.ObsTransitionEnded
		}
		s.publish(ctx, typ, eventtype.Payload{
			Details: eventtype.Details{ObsTransition: &eventtype.ObsTransition{Name: d.TransitionName}},
		})
	case "SceneItemEnableStateChanged":
		var d itemEnabledEvent
		_ = json.Unmarshal(ev.Data, &d)
		s.sourceVisibility(ctx, c, d)
	case "SourceFilterEnableStateChanged":
		var d filterEnabledEvent
		_ = json.Unmarshal(ev.Data, &d)
		s.publish(ctx, eventtype.ObsFilterVisibilityChanged, eventtype.Payload{
			Details: eventtype.Details{ObsFilter: &eventtype.ObsFilter{
				Source:  d.SourceName,
				Filter:  d.FilterName,
				Visible: d.FilterEnabled,
			}},
		})
	}
}

// stateChanged runs the state change of stream and record (B44): the
// output states other than started and stopped are ignored (starting,
// stopping, paused, resumed, reconnecting, reconnected), and each
// change publishes once, so a repeated state publishes nothing.
func (s *Service) stateChanged(_ context.Context, state string, active *bool, publish func(active bool)) {
	var on bool
	switch state {
	case OutputStarted:
		on = true
	case OutputStopped:
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
func (s *Service) sourceVisibility(ctx context.Context, c *client, ev itemEnabledEvent) {
	var d struct {
		SourceName string `json:"sourceName"`
	}
	if err := c.request(ctx, "GetSceneItemSource", map[string]any{
		"sceneName":   ev.SceneName,
		"sceneItemId": ev.SceneItemID,
	}, &d); err != nil {
		s.ports.Logger.DebugContext(ctx, "the source of the changed scene item is unavailable",
			"scene", ev.SceneName, "scene_item", ev.SceneItemID, "error", err)
		return
	}
	s.publish(ctx, eventtype.ObsSourceVisibilityChanged, eventtype.Payload{
		Details: eventtype.Details{ObsSource: &eventtype.ObsSource{
			Scene:   ev.SceneName,
			Source:  d.SourceName,
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
func (s *Service) client() (*client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil, ErrNotConnected
	}
	return s.conn, nil
}

// SetScene sets the current program scene to name (B42).
func (s *Service) SetScene(ctx context.Context, name string) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	if err := c.request(ctx, "SetCurrentProgramScene", map[string]any{"sceneName": name}, nil); err != nil {
		return fmt.Errorf("obs: the scene %q: %w", name, err)
	}
	return nil
}

// StartStream starts the stream output of OBS (B42).
func (s *Service) StartStream(ctx context.Context) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	if err := c.request(ctx, "StartStream", nil, nil); err != nil {
		return fmt.Errorf("obs: start the stream: %w", err)
	}
	return nil
}

// StopStream stops the stream output of OBS (B42).
func (s *Service) StopStream(ctx context.Context) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	if err := c.request(ctx, "StopStream", nil, nil); err != nil {
		return fmt.Errorf("obs: stop the stream: %w", err)
	}
	return nil
}

// StartRecording starts the record output of OBS (B42).
func (s *Service) StartRecording(ctx context.Context) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	if err := c.request(ctx, "StartRecord", nil, nil); err != nil {
		return fmt.Errorf("obs: start the recording: %w", err)
	}
	return nil
}

// StopRecording stops the record output of OBS (B42).
func (s *Service) StopRecording(ctx context.Context) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	if err := c.request(ctx, "StopRecord", nil, nil); err != nil {
		return fmt.Errorf("obs: stop the recording: %w", err)
	}
	return nil
}

// SaveReplay saves the replay buffer of OBS (B42); the path of the
// saved file arrives with obs.replay.saved (B44).
func (s *Service) SaveReplay(ctx context.Context) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	if err := c.request(ctx, "SaveReplayBuffer", nil, nil); err != nil {
		return fmt.Errorf("obs: save the replay buffer: %w", err)
	}
	return nil
}

// SetSource changes the scene item of source in the current program
// scene (B42): the visibility, the position, or both; a source that is
// not in the scene is a clear error.
func (s *Service) SetSource(ctx context.Context, ch Source) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	var scene sceneChangedEvent
	if err := c.request(ctx, "GetCurrentProgramScene", nil, &scene); err != nil {
		return fmt.Errorf("obs: the current scene: %w", err)
	}
	var items struct {
		SceneItems []struct {
			SceneItemID int    `json:"sceneItemId"`
			SourceName  string `json:"sourceName"`
		} `json:"sceneItems"`
	}
	if err := c.request(ctx, "GetSceneItemList", map[string]any{"sceneName": scene.SceneName}, &items); err != nil {
		return fmt.Errorf("obs: the items of the scene %q: %w", scene.SceneName, err)
	}
	var id int
	found := false
	for _, item := range items.SceneItems {
		if item.SourceName == ch.Source {
			id, found = item.SceneItemID, true
			break
		}
	}
	if !found {
		return fmt.Errorf("obs: the source %q is not in the scene %q", ch.Source, scene.SceneName)
	}
	if ch.Visible != nil {
		if err := c.request(ctx, "SetSceneItemEnabled", map[string]any{
			"sceneName":        scene.SceneName,
			"sceneItemId":      id,
			"sceneItemEnabled": *ch.Visible,
		}, nil); err != nil {
			return fmt.Errorf("obs: the visibility of the source %q: %w", ch.Source, err)
		}
	}
	if ch.X != nil || ch.Y != nil {
		// The position changes only the position; the rest of the
		// transform (rotation, scale, bounds) stays as it is, so the
		// request carries the position fields only. A full transform
		// written back would be rejected by OBS for the default bounds
		// (boundsWidth 0) (Review 346-2).
		tr := map[string]any{}
		if ch.X != nil {
			tr["positionX"] = *ch.X
		}
		if ch.Y != nil {
			tr["positionY"] = *ch.Y
		}
		if err := c.request(ctx, "SetSceneItemTransform", map[string]any{
			"sceneName":          scene.SceneName,
			"sceneItemId":        id,
			"sceneItemTransform": tr,
		}, nil); err != nil {
			return fmt.Errorf("obs: the position of the source %q: %w", ch.Source, err)
		}
	}
	return nil
}

// SetFilter sets the visibility of the filter of the source (B42).
func (s *Service) SetFilter(ctx context.Context, source, filter string, visible bool) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	if err := c.request(ctx, "SetSourceFilterEnabled", map[string]any{
		"sourceName":    source,
		"filterName":    filter,
		"filterEnabled": visible,
	}, nil); err != nil {
		return fmt.Errorf("obs: the filter %q of the source %q: %w", filter, source, err)
	}
	return nil
}
