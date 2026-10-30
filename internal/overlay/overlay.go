// SPDX-License-Identifier: MIT

// Package overlay runs the overlay server of the core (roadmap 7.1,
// ADR-0019): a dedicated HTTP server beside the API server that serves
// the embedded runtime to OBS browser sources, keeps the active items
// and the widget state, and delivers the JSON protocol over WebSocket.
package overlay

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ripmav/streamcrew/internal/settings"
)

// ItemKinds are the item kinds of the overlay (spec overlays.md, B13),
// in the order of the roadmap: the kinds of 7.2 first.
//
//nolint:gochecknoglobals // static table of kinds
var ItemKinds = []string{
	"text", "image", "video", "audio", "html", "youtube", "clip", "timer", "emote_effect",
}

// Animations are the show and hide animations of an item (B10).
//
//nolint:gochecknoglobals // static table of animations
var Animations = []string{
	"none", "fade", "slide-left", "slide-right", "slide-up", "slide-down", "pop",
}

// DefaultItemDuration is the show time of an item without one (B11).
const DefaultItemDuration = 10 * time.Second

// Item is an item to show on an endpoint (B9, B10, B13).
type Item struct {
	// Kind is the kind of the item, one of ItemKinds.
	Kind string
	// Position is where the item shows; the model random is rolled by
	// the service into a concrete point (B9).
	Position settings.Position
	// Layer is the integer layer; a higher layer shows above (B9).
	Layer int
	// Animation is the show and hide animation, one of Animations;
	// empty for none (B10).
	Animation string
	// AnimationDuration is the time of the animation; 0 for the
	// default of 300 ms (B10).
	AnimationDuration time.Duration
	// Duration is the show time; 0 keeps the item until the playback
	// ends (item.ended, B14), at most until the time limit of the
	// action (B46).
	Duration time.Duration
	// Content is the kind-specific content as a JSON document; empty
	// for none.
	Content any
}

// Ports are the dependencies of a Service.
type Ports struct {
	// Section returns the overlay section of the profile: the endpoints
	// and the widget definitions.
	Section func(ctx context.Context) (settings.Overlay, error)
	// Version is the version of the core, reported by hello (B4).
	Version string
	// Logger logs the service; nil discards the output.
	Logger *slog.Logger
	// Rand supplies the random point of the position model random as
	// two percent coordinates from 0 to 100 (B9); nil uses math/rand.
	Rand func() (x, y float64)
}

// ClientSlot is one connected runtime of an endpoint.
type ClientSlot struct {
	// ID identifies the slot, assigned by the service.
	ID string
	// Send delivers a message to the client; it may block while the
	// client is slow.
	Send func(msg Message)
}

// Service keeps the endpoints, the active items and the widget state of
// the profile and broadcasts the changes to the connected runtimes. It
// is the single writer of the overlay state (Code-ADR-0002: no globals,
// one owner).
type Service struct {
	section func(ctx context.Context) (settings.Overlay, error)
	version string
	logger  *slog.Logger
	rand    func() (float64, float64)

	mu        sync.Mutex
	endpoints map[string]settings.OverlayEndpoint
	widgets   map[string]settings.OverlayWidget
	// visible is the runtime visibility of a widget (B21); a widget
	// not in the map shows its flag of the section.
	visible map[string]bool
	// items are the active items per endpoint ID.
	items map[string]map[string]*activeItem
	// clients are the connected runtimes per endpoint ID.
	clients map[string]map[string]ClientSlot
	// stateProvider computes the runtime state of a widget for the state
	// sync of a (re)connecting runtime (B4, B20); set by the widget
	// service before the server runs, and it must not call back into the
	// service.
	stateProvider func(ctx context.Context, w settings.OverlayWidget) any
	// tts is the in-flight speech per endpoint ID (B41, provider
	// browser); nil if none speaks.
	tts map[string]*ttsState

	nextItem atomic.Uint64
	nextSlot atomic.Uint64
	nextTTS  atomic.Int64
}

// ttsState is a speech that the runtimes of an endpoint speak (B41).
type ttsState struct {
	// seq is the sequence of the speech, echoed by the tts.ended that
	// ends it, so that the tts.ended of a canceled speech cannot end the
	// next one (the runtime cancels the current speech before it speaks
	// a new one).
	seq int64
	// end is closed when the speech is done.
	end chan struct{}
}

// activeItem is an item in the active set of an endpoint.
type activeItem struct {
	data  ItemData
	timer *time.Timer
	// end is closed once the item leaves the active set, by its duration,
	// an explicit remove, the item.ended of a runtime or a reload that
	// drops its endpoint (B11, B14).
	end chan struct{}
}

// New returns a service with the overlay section of the profile loaded.
func New(ctx context.Context, p Ports) (*Service, error) {
	if p.Section == nil {
		return nil, errors.New("overlay: no section loader")
	}
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	if p.Rand == nil {
		p.Rand = func() (x, y float64) {
			//nolint:gosec // not a cryptographic source, but a random position of an item
			return float64(rand.IntN(101)), float64(rand.IntN(101))
		}
	}
	s := &Service{
		section:   p.Section,
		version:   p.Version,
		logger:    p.Logger,
		rand:      p.Rand,
		endpoints: map[string]settings.OverlayEndpoint{},
		widgets:   map[string]settings.OverlayWidget{},
		visible:   map[string]bool{},
		items:     map[string]map[string]*activeItem{},
		clients:   map[string]map[string]ClientSlot{},
		tts:       map[string]*ttsState{},
	}
	if err := s.reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// reload reads the section and rebuilds the endpoint and widget maps.
// It drops the items of endpoints the section no longer knows; the
// visibility overrides of removed widgets are dropped with them.
func (s *Service) reload(ctx context.Context) error {
	sec, err := s.section(ctx)
	if err != nil {
		return fmt.Errorf("overlay: load the section: %w", err)
	}
	endpoints := map[string]settings.OverlayEndpoint{settings.DefaultEndpointID: {ID: settings.DefaultEndpointID, Name: "Default"}}
	for _, e := range sec.Endpoints {
		endpoints[e.ID] = e
	}
	widgets := make(map[string]settings.OverlayWidget, len(sec.Widgets))
	for _, w := range sec.Widgets {
		widgets[w.ID] = w
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.endpoints = endpoints
	s.widgets = widgets
	for id := range s.visible {
		if _, ok := widgets[id]; !ok {
			delete(s.visible, id)
		}
	}
	for id := range s.items {
		if _, ok := endpoints[id]; !ok {
			for _, item := range s.items[id] {
				if item.timer != nil {
					item.timer.Stop()
				}
				close(item.end)
			}
			delete(s.items, id)
		}
	}
	// The in-flight speech of a removed endpoint cannot end (B41).
	for id := range s.tts {
		if _, ok := endpoints[id]; !ok {
			close(s.tts[id].end)
			delete(s.tts, id)
		}
	}
	return nil
}

// Reload re-reads the overlay section and applies the change (B2, B19).
func (s *Service) Reload(ctx context.Context) error {
	return s.reload(ctx)
}

// SetStateProvider names the provider of the widget state for the state
// sync (B4, B20); nil removes it. The provider must not call back into
// the service; set it before the server runs.
func (s *Service) SetStateProvider(fn func(ctx context.Context, w settings.OverlayWidget) any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateProvider = fn
}

// Endpoint returns the named endpoint; ok is false if the profile does
// not know it (B2, B45).
func (s *Service) Endpoint(id string) (settings.OverlayEndpoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.endpoints[id]
	return e, ok
}

// Endpoints returns the endpoints in a stable order: default first, then
// the endpoints of the section in their order.
func (s *Service) Endpoints() []settings.OverlayEndpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]settings.OverlayEndpoint, 0, len(s.endpoints))
	if e, ok := s.endpoints[settings.DefaultEndpointID]; ok {
		out = append(out, e)
	}
	ids := make([]string, 0, len(s.endpoints))
	for id := range s.endpoints {
		if id != settings.DefaultEndpointID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		out = append(out, s.endpoints[id])
	}
	return out
}

// Widget returns the widget definition of the profile (B19).
func (s *Service) Widget(id string) (settings.OverlayWidget, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.widgets[id]
	return w, ok
}

// ShowItem registers an item on the endpoint, broadcasts item.show and
// returns its ID and the channel that closes once the item leaves the
// active set (B9–B13, B18). An unknown endpoint is an error with the ID
// in the message (B45). A duration of 0 keeps the item in the active set
// until ItemEnded removes it (B11, B14); a duration above 0 removes the
// item after the time has passed (B11).
func (s *Service) ShowItem(ctx context.Context, endpoint string, item Item) (string, <-chan struct{}, error) {
	if !contains(ItemKinds, item.Kind) {
		return "", nil, fmt.Errorf("overlay: the item kind %q is unknown", item.Kind)
	}
	if item.Animation != "" && !contains(Animations, item.Animation) {
		return "", nil, fmt.Errorf("overlay: the animation %q is unknown", item.Animation)
	}
	if err := item.Position.Validate(); err != nil {
		return "", nil, fmt.Errorf("overlay: the position: %w", err)
	}
	if item.Duration < 0 {
		return "", nil, fmt.Errorf("overlay: the duration %s must be 0 or more", item.Duration)
	}
	if item.Position.Model == settings.PositionRandom {
		x, y := s.rand()
		item.Position = settings.Position{Model: settings.PositionPercent, X: x, Y: y}
	}

	s.mu.Lock()
	if _, ok := s.endpoints[endpoint]; !ok {
		s.mu.Unlock()
		return "", nil, fmt.Errorf("overlay: the endpoint %q is unknown", endpoint)
	}
	// Zero-padded, so the lexicographic order of the IDs is the order
	// of the show, which the state sync uses (B4).
	id := fmt.Sprintf("item-%06d", s.nextItem.Add(1))
	data := ItemData{
		ID:                id,
		Kind:              item.Kind,
		Position:          item.Position,
		Layer:             item.Layer,
		Animation:         item.Animation,
		AnimationDuration: int(item.AnimationDuration / time.Millisecond),
		Duration:          int(item.Duration / time.Millisecond),
		Content:           item.Content,
	}
	active := &activeItem{data: data, end: make(chan struct{})}
	if item.Duration > 0 {
		active.timer = time.AfterFunc(item.Duration, func() {
			s.RemoveItem(ctx, endpoint, id)
		})
	}
	if s.items[endpoint] == nil {
		s.items[endpoint] = map[string]*activeItem{}
	}
	s.items[endpoint][id] = active
	s.mu.Unlock()

	s.logger.InfoContext(ctx, "overlay item shown", "endpoint", endpoint, "item", id, "kind", item.Kind, "duration", item.Duration)
	s.broadcast(endpoint, Message{Type: TypeItemShow, Data: data})
	return id, active.end, nil
}

// RemoveItem removes an active item and broadcasts item.remove (B11);
// ok is false if the item is not active.
func (s *Service) RemoveItem(ctx context.Context, endpoint, id string) (ok bool) {
	s.mu.Lock()
	item, ok := s.takeItem(endpoint, id)
	s.mu.Unlock()
	if !ok {
		return false
	}
	if item.timer != nil {
		item.timer.Stop()
	}
	s.logger.DebugContext(ctx, "overlay item removed", "endpoint", endpoint, "item", id)
	s.broadcast(endpoint, Message{Type: TypeItemRemove, Data: RemoveData{ID: id}})
	return true
}

// takeItem removes the item from the active set and closes its end
// channel; the caller holds mu.
func (s *Service) takeItem(endpoint, id string) (*activeItem, bool) {
	items := s.items[endpoint]
	item, ok := items[id]
	if !ok {
		return nil, false
	}
	delete(items, id)
	if len(items) == 0 {
		delete(s.items, endpoint)
	}
	close(item.end)
	return item, true
}

// ItemEnded is the item.ended message of a runtime (B14): the playback
// of a tone or video item has ended, so the item leaves the active set
// and is removed from the other runtimes of the endpoint (B49). ok is
// false if the item is not active.
func (s *Service) ItemEnded(ctx context.Context, endpoint, id string) (ok bool) {
	return s.RemoveItem(ctx, endpoint, id)
}

// TTSSpeak makes the runtimes of the endpoint speak the text (B41, the
// provider browser) and returns the channel that the tts.ended closes.
// The runtime cancels the current speech before it speaks the new one,
// so the speech is replaced and the old channel is closed; the seq
// keeps the tts.ended of the canceled speech from ending the new one
// (B49). An unknown endpoint is an error with the ID in the message
// (B45).
func (s *Service) TTSSpeak(ctx context.Context, endpoint string, d TTSSpeakData) (<-chan struct{}, error) {
	if d.Text == "" {
		return nil, errors.New("overlay: tts.speak needs a text")
	}
	s.mu.Lock()
	if _, ok := s.endpoints[endpoint]; !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("overlay: the endpoint %q is unknown", endpoint)
	}
	if cur := s.tts[endpoint]; cur != nil {
		close(cur.end)
	}
	d.Seq = s.nextTTS.Add(1)
	st := &ttsState{seq: d.Seq, end: make(chan struct{})}
	s.tts[endpoint] = st
	s.mu.Unlock()
	s.logger.InfoContext(ctx, "overlay tts speak", "endpoint", endpoint, "seq", d.Seq)
	s.broadcast(endpoint, Message{Type: TypeTTSSpeak, Data: d})
	return st.end, nil
}

// TTSEnded is the tts.ended message of a runtime (B41): the speech with
// the seq has ended, and the first report closes the channel (B49). ok
// is false if the endpoint has no speech with the seq, e.g. the tts.ended
// of a canceled speech after the next one started.
func (s *Service) TTSEnded(ctx context.Context, endpoint string, seq int64) (ok bool) {
	s.mu.Lock()
	st := s.tts[endpoint]
	if st == nil || st.seq != seq {
		s.mu.Unlock()
		return false
	}
	delete(s.tts, endpoint)
	s.mu.Unlock()
	close(st.end)
	s.logger.DebugContext(ctx, "overlay tts ended", "endpoint", endpoint, "seq", seq)
	return true
}

// ActiveItems returns the active items of the endpoint in a stable
// order, for the state sync of a (re)connecting runtime (B4).
func (s *Service) ActiveItems(endpoint string) []ItemData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeItems(endpoint)
}

// SetVisible switches a widget on or off (B21) and broadcasts
// widget.show or widget.hide. A hidden widget does not run and is not
// sent to the runtimes.
func (s *Service) SetVisible(ctx context.Context, widgetID string, visible bool) error {
	s.mu.Lock()
	w, ok := s.widgets[widgetID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("overlay: the widget %q is unknown", widgetID)
	}
	cur := w.Visible
	if v, set := s.visible[widgetID]; set {
		cur = v
	}
	if cur == visible {
		s.mu.Unlock()
		return nil
	}
	s.visible[widgetID] = visible
	endpoint := w.Endpoint
	if endpoint == "" {
		endpoint = settings.DefaultEndpointID
	}
	typ := TypeWidgetShow
	if !visible {
		typ = TypeWidgetHide
	}
	s.mu.Unlock()
	s.logger.InfoContext(ctx, "overlay widget", "widget", widgetID, "visible", visible)
	s.broadcast(endpoint, Message{Type: typ, Data: WidgetIDData{ID: widgetID}})
	return nil
}

// WidgetVisible reports the effective visibility of the widget (B21): the
// flag of the section or a runtime override of SetVisible; ok is false if
// the profile does not know the widget. A hidden widget does not run.
func (s *Service) WidgetVisible(widgetID string) (visible, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.widgets[widgetID]
	if !ok {
		return false, false
	}
	visible = w.Visible
	if v, set := s.visible[widgetID]; set {
		visible = v
	}
	return visible, true
}

// UpdateWidget changes the state of a widget and broadcasts
// widget.update to the runtimes of its endpoint (B22); an unknown widget
// is an error with the ID in the message.
func (s *Service) UpdateWidget(ctx context.Context, widgetID string, state any) error {
	s.mu.Lock()
	w, ok := s.widgets[widgetID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("overlay: the widget %q is unknown", widgetID)
	}
	endpoint := w.Endpoint
	if endpoint == "" {
		endpoint = settings.DefaultEndpointID
	}
	s.mu.Unlock()
	s.logger.DebugContext(ctx, "overlay widget updated", "widget", widgetID)
	s.broadcast(endpoint, Message{Type: TypeWidgetUpdate, Data: WidgetUpdateData{ID: widgetID, State: state}})
	return nil
}

// WidgetStates returns the state of the visible widgets of the endpoint
// for the state sync (B4, B20). The state is filled by the widget
// services from 7.2.
func (s *Service) WidgetStates(ctx context.Context, endpoint string) []WidgetState {
	s.mu.Lock()
	widgets := s.visibleWidgets(endpoint)
	provider := s.stateProvider
	s.mu.Unlock()
	return s.syncStates(ctx, provider, widgets)
}

// Attach registers a connected runtime of the endpoint and sends it the
// handshake: hello, state.widgets and the active items (B4, B7). send
// delivers the messages to the runtime, called by the service for every
// broadcast and the handshake. It returns the slot; the server detaches
// it again with Detach when the connection ends.
func (s *Service) Attach(ctx context.Context, endpoint string, send func(msg Message)) (ClientSlot, error) {
	if err := s.SyncState(ctx, endpoint, send); err != nil {
		return ClientSlot{}, err
	}
	slot := ClientSlot{ID: fmt.Sprintf("slot-%d", s.nextSlot.Add(1)), Send: send}
	s.mu.Lock()
	if s.clients[endpoint] == nil {
		s.clients[endpoint] = map[string]ClientSlot{}
	}
	s.clients[endpoint][slot.ID] = slot
	s.mu.Unlock()
	s.logger.InfoContext(ctx, "overlay client attached", "endpoint", endpoint, "slot", slot.ID)
	return slot, nil
}

// SyncState sends the full state of the endpoint to send in the order
// of B4: hello, state.widgets and the active items. The server calls it
// on attach and on ready, so a message lost during the connection is
// caught (B4, B7). The widget states are computed outside the service
// lock, because the state provider does DB I/O (Review 344-12).
func (s *Service) SyncState(ctx context.Context, endpoint string, send func(msg Message)) error {
	e, ok := s.Endpoint(endpoint)
	if !ok {
		return fmt.Errorf("overlay: the endpoint %q is unknown", endpoint)
	}
	s.mu.Lock()
	hello := HelloData{Version: s.version, Endpoint: e.ID, Name: e.Name, Time: time.Now()}
	widgets := s.visibleWidgets(endpoint)
	items := s.activeItems(endpoint)
	provider := s.stateProvider
	s.mu.Unlock()
	states := s.syncStates(ctx, provider, widgets)
	for _, msg := range s.handshake(hello, states, items) {
		send(msg)
	}
	return nil
}

// handshake builds the messages of a (re)connect in the order of B4.
func (s *Service) handshake(hello HelloData, widgets []WidgetState, items []ItemData) []Message {
	msgs := []Message{
		{Type: TypeHello, Data: hello},
		{Type: TypeStateWidgets, Data: StateWidgetsData{Widgets: widgets}},
	}
	for _, item := range items {
		msgs = append(msgs, Message{Type: TypeItemShow, Data: item})
	}
	return msgs
}

// visibleWidgets are the definitions of the visible widgets of the
// endpoint in the stable ID order, for the state sync (B4, B21); the
// caller holds mu.
func (s *Service) visibleWidgets(endpoint string) []settings.OverlayWidget {
	var out []settings.OverlayWidget
	for _, w := range s.widgets {
		ep := w.Endpoint
		if ep == "" {
			ep = settings.DefaultEndpointID
		}
		if ep != endpoint {
			continue
		}
		visible := w.Visible
		if v, ok := s.visible[w.ID]; ok {
			visible = v
		}
		if !visible {
			continue
		}
		out = append(out, w)
	}
	slices.SortFunc(out, func(a, b settings.OverlayWidget) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// syncStates fills the states of the widget definitions with the
// provider (B20); a nil provider leaves the state empty. It runs
// outside the service lock, because the provider does DB I/O
// (Review 344-12).
func (s *Service) syncStates(ctx context.Context, provider func(ctx context.Context, w settings.OverlayWidget) any, widgets []settings.OverlayWidget) []WidgetState {
	out := make([]WidgetState, 0, len(widgets))
	for _, w := range widgets {
		var state any
		if provider != nil {
			state = provider(ctx, w)
		}
		out = append(out, WidgetState{ID: w.ID, Kind: w.Kind, Name: w.Name, Position: w.Position, State: state})
	}
	return out
}

// activeItems is ActiveItems without the lock; the caller holds mu.
func (s *Service) activeItems(endpoint string) []ItemData {
	items := make([]ItemData, 0, len(s.items[endpoint]))
	for _, item := range s.items[endpoint] {
		items = append(items, item.data)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

// Detach removes a connected runtime of the endpoint (B49): the other
// runtimes keep the same state, the active items stay in the set.
func (s *Service) Detach(ctx context.Context, endpoint, slotID string) {
	s.mu.Lock()
	delete(s.clients[endpoint], slotID)
	if len(s.clients[endpoint]) == 0 {
		delete(s.clients, endpoint)
	}
	s.mu.Unlock()
	s.logger.DebugContext(ctx, "overlay client detached", "endpoint", endpoint, "slot", slotID)
}

// Clients returns the number of the connected runtimes of the endpoint,
// for the status and the tests.
func (s *Service) Clients(endpoint string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients[endpoint])
}

// broadcast sends a message to every runtime of the endpoint. A slot
// whose send has failed was detached by the server; the call is a no-op
// for an endpoint without runtimes.
func (s *Service) broadcast(endpoint string, msg Message) {
	s.mu.Lock()
	slots := make([]ClientSlot, 0, len(s.clients[endpoint]))
	for _, slot := range s.clients[endpoint] {
		slots = append(slots, slot)
	}
	s.mu.Unlock()
	for _, slot := range slots {
		slot.Send(msg)
	}
}

// contains reports whether list contains s.
func contains(list []string, s string) bool {
	return slices.Contains(list, s)
}
