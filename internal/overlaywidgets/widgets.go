// SPDX-License-Identifier: MIT

// Package overlaywidgets runs the P1 widgets of the overlay (roadmap 7.2,
// spec overlays.md, B24-B28): label, goal, timer, event_list and chat.
//
// The service reads the widget definitions from the settings section
// "overlay" on demand, keeps the mutable state of each kind (the goal in
// the profile, B20), pushes the updates to the runtimes through the
// overlay service, and publishes the effects of the widgets as bus events
// (B23, A2). The kinds of 7.5 (persistent_timer, end_credits,
// stream_boss, …) are not part of this service.
package overlaywidgets

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/feed"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
)

// ErrUnknownWidget is wrapped by the operations for a widget the profile
// does not know, or of a kind the operation does not fit.
var ErrUnknownWidget = errors.New("unknown widget")

// labelTickDefault is the interval of the label re-render (B24, A5).
const labelTickDefault = 30 * time.Second

// The kinds of the widgets of 7.2 (B24-B28).
const (
	KindLabel     = "label"
	KindGoal      = "goal"
	KindTimer     = "timer"
	KindEventList = "event_list"
	KindChat      = "chat"
)

// FeedTypes are the event types that land in the feed (events.md, B30);
// the event_list widget shows all of them by default (B27).
//
//nolint:gochecknoglobals // static table of feed types
var FeedTypes = []event.Type{
	eventtype.ChannelFollow,
	eventtype.ChannelSubscribe,
	eventtype.ChannelResubscribe,
	eventtype.ChannelSubscriptionGift,
	eventtype.ChannelSubscriptionMassGift,
	eventtype.ChannelRaid,
	eventtype.TwitchBitsCheer,
}

// Feed is the source of the recent feed events.
type Feed interface {
	// LatestFeed returns up to n of the most recent feed events, oldest
	// first.
	LatestFeed(ctx context.Context, n int) ([]feed.Event, error)
}

// State keeps the mutable state of the widgets (B20).
type State interface {
	// OverlayWidgetState returns the state document of the widget; found
	// is false if the widget has no state yet.
	OverlayWidgetState(ctx context.Context, widgetID string) ([]byte, bool, error)
	// SetOverlayWidgetState stores the state document of the widget.
	SetOverlayWidgetState(ctx context.Context, widgetID string, state []byte) error
}

// SessionStats reports the statistics of the current stream session.
type SessionStats interface {
	// CurrentSessionStats returns the statistics of the current session of
	// p; ok is false if p has no current session.
	CurrentSessionStats(ctx context.Context, p platform.Name) (feed.SessionStats, bool, error)
}

// Ports are what the service needs. All are required.
type Ports struct {
	// Overlay takes the widget updates and the runtime visibility.
	Overlay *overlay.Service
	// Bus delivers the events the widgets react to, and receives the
	// effects of the goal widget (B23, A2).
	Bus *event.Bus
	// Section returns the overlay section on demand (A1); the service
	// re-reads it on the tick and after the events that change the
	// widgets.
	Section func(ctx context.Context) (settings.Overlay, error)
	// Feed is the source of the event_list and the latest_* labels.
	Feed Feed
	// State keeps the goal progress (B20).
	State State
	// Templates renders the text of the label widget (B24).
	Templates *template.Engine
	// Scope returns the template scope of the profile for the label
	// render: the platform, the time zone, the locale and the argument
	// delimiter.
	Scope func(ctx context.Context) (*template.Scope, error)
	// StreamStates reports the stream state for the count labels (B24).
	StreamStates template.StreamStates
	// SessionStats reports the session statistics for the total_* labels
	// (B24).
	SessionStats SessionStats
	// Locale returns the locale of the profile, as the runtime formats
	// the dates and times of the label kinds date and time (B24, A5).
	Locale func(ctx context.Context) (template.Locale, error)
	// Logger logs the service; nil discards the output.
	Logger *slog.Logger
}

// Option configures a Service.
type Option func(*Service) error

// WithLabelTick sets the interval of the label re-render and the other
// tick work; it must be positive. The default is 30 s (B24); a shorter
// interval is for tests.
func WithLabelTick(d time.Duration) Option {
	return func(s *Service) error {
		if d <= 0 {
			return fmt.Errorf("invalid option: label tick %s", d)
		}
		s.tickDur = d
		return nil
	}
}

// goalState is the mutable state of a goal widget, persisted in the
// profile (B20).
type goalState struct {
	// current is the amount reached so far.
	current int64
	// segment is the index of the segment the goal works on.
	segment int
	// resetAt is the time of the last automatic reset (B25); zero if it
	// never ran.
	resetAt time.Time
}

// goalDoc is the stored form of goalState.
type goalDoc struct {
	Current int64     `json:"current"`
	Segment int       `json:"segment"`
	ResetAt time.Time `json:"reset_at"`
}

// timerState is the running state of a timer widget (B26); it is not
// persisted.
type timerState struct {
	// deadline is when the timer reaches zero while it runs.
	deadline time.Time
	// paused reports whether the timer is paused.
	paused bool
	// pausedFor is the remaining time while it is paused.
	pausedFor time.Duration
}

// listEntry is one entry of an event_list widget (B27).
type listEntry struct {
	// User is the display name of the user; empty if there is none.
	User string `json:"user,omitempty"`
	// Type is the event type; empty for a dynamic entry.
	Type string `json:"type,omitempty"`
	// Text is the details text; empty if the entry has none.
	Text string `json:"text,omitempty"`
	// Time is the time of the entry in milliseconds.
	Time int64 `json:"time"`
}

// chatMsg is one shown message of a chat widget (B28).
type chatMsg struct {
	// ID is the platform's ID of the message, for the delete event;
	// empty if the platform names none. It stays out of the runtime
	// state.
	ID string `json:"-"`
	// UserID is the core ID of the user; it stays out of the runtime
	// state, which shows the name.
	UserID string `json:"-"`
	// User is the display name of the user, as a snapshot.
	User string `json:"user,omitempty"`
	// Text is the message as written.
	Text string `json:"text"`
	// Platform is the platform the message came from.
	Platform string `json:"platform,omitempty"`
}

// Service runs the widgets. It is safe for concurrent use; the events
// arrive in the order the bus delivers them.
type Service struct {
	ports   Ports
	logger  *slog.Logger
	tickDur time.Duration

	mu      sync.Mutex
	started bool
	// goals are the running goal widgets by widget ID (B20).
	goals map[string]*goalState
	// timers are the running timer widgets by widget ID (B26).
	timers map[string]*timerState
	// lists are the dynamic entries of the event_list widgets by widget ID
	// (B27).
	lists map[string][]listEntry
	// chats are the shown messages of the chat widgets by widget ID (B28).
	chats map[string][]chatMsg
	// labelTexts is the last pushed label text by widget ID; an unchanged
	// render pushes nothing (B24).
	labelTexts map[string]string
}

// New returns a service with the ports p.
func New(p Ports, opts ...Option) (*Service, error) {
	switch {
	case p.Overlay == nil || p.Bus == nil || p.Section == nil || p.Feed == nil:
		return nil, errors.New("new widget service: missing port")
	case p.State == nil || p.Templates == nil || p.Scope == nil:
		return nil, errors.New("new widget service: missing port")
	case p.StreamStates == nil || p.SessionStats == nil || p.Locale == nil:
		return nil, errors.New("new widget service: missing port")
	}
	s := &Service{
		ports:      p,
		logger:     slog.New(slog.DiscardHandler),
		goals:      map[string]*goalState{},
		timers:     map[string]*timerState{},
		lists:      map[string][]listEntry{},
		chats:      map[string][]chatMsg{},
		labelTexts: map[string]string{},
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("new widget service: %w", err)
		}
	}
	if p.Logger != nil {
		s.logger = p.Logger
	}
	return s, nil
}

// Run counts until ctx ends (Code-ADR-0004). It subscribes to the events
// that change the widgets and does the tick work on the label interval.
// A second call fails.
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("the widget service runs already")
	}
	s.started = true
	s.mu.Unlock()

	sub := s.ports.Bus.Subscribe(ctx,
		event.WithName("widgets"),
		event.WithTypes(
			eventtype.ChannelFollow,
			eventtype.ChannelSubscribe,
			eventtype.ChannelResubscribe,
			eventtype.ChannelSubscriptionGift,
			eventtype.ChannelSubscriptionMassGift,
			eventtype.ChannelRaid,
			eventtype.TwitchBitsCheer,
			eventtype.ChatMessage,
			eventtype.ChatMessageDelete,
			eventtype.ChatUserTimeout,
			eventtype.ChatUserBan,
		),
	)
	interval := s.tickDur
	if interval <= 0 {
		interval = labelTickDefault
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case env, ok := <-sub.C():
			if !ok { // the subscription ended with ctx
				return nil
			}
			s.handle(ctx, env)
		case <-tick.C:
			s.tick(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// handle applies one event to the widgets.
func (s *Service) handle(ctx context.Context, env event.Envelope) {
	switch env.Type {
	case event.TypeLag:
		lag, _ := event.Payload[event.Lag](env)
		s.logger.WarnContext(ctx, "the widgets lagged behind the events", "dropped", lag.Dropped)
	case eventtype.ChannelFollow, eventtype.ChannelSubscribe, eventtype.ChannelResubscribe,
		eventtype.ChannelSubscriptionGift, eventtype.ChannelSubscriptionMassGift,
		eventtype.ChannelRaid, eventtype.TwitchBitsCheer:
		s.goalProgress(ctx, env)
		s.refreshList(ctx)
		s.refreshLabels(ctx)
	case eventtype.ChatMessage:
		s.chatMessage(ctx, env)
	case eventtype.ChatMessageDelete:
		s.chatDeleted(ctx, env)
	case eventtype.ChatUserTimeout, eventtype.ChatUserBan:
		s.chatModerated(ctx, env)
	default:
		// The subscription filters; the default keeps a new type from
		// being silently counted wrong.
	}
}

// tick does the work of the label interval: it re-renders the labels
// (B24, A5), re-reads the section so that the overlay service and the
// widgets follow a change within one interval (B2, B19), starts the
// visible timer widgets (B26) and runs the goal auto-reset (B25).
func (s *Service) tick(ctx context.Context) {
	if err := s.ports.Overlay.Reload(ctx); err != nil {
		s.logger.ErrorContext(ctx, "re-reading the overlay section failed", "error", err)
	}
	s.refreshLabels(ctx)
	s.refreshList(ctx)
	s.startTimers(ctx)
	s.goalReset(ctx)
}

// section reads the overlay section on demand (A1); nil on an error, which
// is logged.
func (s *Service) section(ctx context.Context) *settings.Overlay {
	sec, err := s.ports.Section(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the overlay section failed", "error", err)
		return nil
	}
	return &sec
}

// visible reports whether the widget runs: it must be in the section and
// its effective visibility must be on (B21).
func (s *Service) visible(w settings.OverlayWidget) bool {
	v, ok := s.ports.Overlay.WidgetVisible(w.ID)
	return ok && v
}

// push sends the state of the widget to the runtimes (B22).
func (s *Service) push(ctx context.Context, w settings.OverlayWidget, state any) {
	if err := s.ports.Overlay.UpdateWidget(ctx, w.ID, state); err != nil {
		s.logger.ErrorContext(ctx, "pushing the widget state failed", "widget", w.ID, "error", err)
	}
}

// ---------- label (B24, A5) ----------

// labelConfig is the configuration of the label widget.
type labelConfig struct {
	// Type is the kind of the label; empty for text.
	Type string `json:"type"`
	// Text is the template of the kind text.
	Text string `json:"text"`
}

// The label kinds that the core renders (B24); date and time the runtime
// renders locally.
//
//nolint:gochecknoglobals // static table of label kinds
var labelValueKinds = []string{
	"viewer_count", "chatter_count", "latest_follower",
	"total_followers", "latest_subscriber", "total_subscribers", "latest_raid",
}

// refreshLabels re-renders the visible label widgets (B24).
func (s *Service) refreshLabels(ctx context.Context) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	var scope *template.Scope
	for _, w := range sec.Widgets {
		if w.Kind != KindLabel || !s.visible(w) {
			continue
		}
		if scope == nil {
			sc, err := s.ports.Scope(ctx)
			if err != nil {
				s.logger.ErrorContext(ctx, "building the label scope failed", "error", err)
				return
			}
			scope = sc
		}
		s.pushLabel(ctx, w, scope)
	}
}

// pushLabel computes the state of one label and pushes it on a change
// (B24). The kinds date and time go to the runtime with the locale of the
// profile, which renders them per second (A5).
func (s *Service) pushLabel(ctx context.Context, w settings.OverlayWidget, scope *template.Scope) {
	cfg := s.labelConfig(ctx, w)
	if cfg == nil {
		return
	}
	switch {
	case cfg.Type == "date" || cfg.Type == "time":
		s.pushLabelState(ctx, w, s.dateTimeState(ctx, cfg.Type))
	case slices.Contains(labelValueKinds, cfg.Type):
		s.pushLabelText(ctx, w, s.labelValue(ctx, cfg.Type, scope))
	default:
		text, err := s.ports.Templates.Render(ctx, template.Parse(cfg.Text), scope, template.Text)
		if err != nil {
			s.logger.ErrorContext(ctx, "rendering the label failed", "widget", w.ID, "error", err)
			return
		}
		s.pushLabelText(ctx, w, text)
	}
}

// dateTimeState is the state of the label kinds date and time (B24, A5):
// the runtime renders them per second in the locale and the time zone of
// the profile.
func (s *Service) dateTimeState(ctx context.Context, kind string) map[string]any {
	loc := template.LocaleUSEnglish
	if l, err := s.ports.Locale(ctx); err == nil {
		loc = l
	}
	zone := "UTC"
	if sc, err := s.ports.Scope(ctx); err == nil && sc.Location != nil {
		zone = sc.Location.String()
	}
	st := map[string]any{"kind": kind, "locale": string(loc)}
	// time.Local's name "Local" is not a zone the browser knows; without
	// the key the browser takes its own time zone.
	if zone != time.Local.String() {
		st["timeZone"] = zone
	}
	return st
}

// pushLabelState pushes the state of the label; the date and time states
// are keyed by the locale and the zone, so a change of either re-pushes.
func (s *Service) pushLabelState(ctx context.Context, w settings.OverlayWidget, state map[string]any) {
	key, _ := state["kind"].(string)
	loc, _ := state["locale"].(string)
	zone, _ := state["timeZone"].(string)
	s.pushIfChanged(ctx, w, key+" "+loc+" "+zone, state)
}

// pushLabelText pushes a rendered label text on a change (B24).
func (s *Service) pushLabelText(ctx context.Context, w settings.OverlayWidget, text string) {
	s.pushIfChanged(ctx, w, text, map[string]any{"text": text})
}

// pushIfChanged pushes the state of the label if key differs from the last
// pushed one (B24, "bei Textwechsel schickt").
func (s *Service) pushIfChanged(ctx context.Context, w settings.OverlayWidget, key string, state any) {
	s.mu.Lock()
	if s.labelTexts[w.ID] == key {
		s.mu.Unlock()
		return
	}
	s.labelTexts[w.ID] = key
	s.mu.Unlock()
	s.push(ctx, w, state)
}

// labelValue resolves the data kinds of the label (B24). A value the
// source does not report has no text (template.md, B4).
func (s *Service) labelValue(ctx context.Context, kind string, scope *template.Scope) string {
	switch kind {
	case "viewer_count", "chatter_count", "total_followers":
		st, err := s.ports.StreamStates.StreamState(ctx, scope.Platform)
		if err != nil {
			return ""
		}
		switch kind {
		case "viewer_count":
			if st.Viewers != nil {
				return strconv.FormatInt(*st.Viewers, 10)
			}
		case "chatter_count":
			if st.Chatters != nil {
				return strconv.FormatInt(*st.Chatters, 10)
			}
		default: // total_followers
			if st.Followers != nil {
				return strconv.FormatInt(*st.Followers, 10)
			}
			// shortcut: the platforms report no total followers; the
			// session count until a source exists (Phase 9).
			return s.sessionCount(ctx, scope.Platform, func(st feed.SessionStats) int64 { return st.Followers })
		}
		return "" // the value of the kind is unknown
	case "total_subscribers":
		// shortcut: the platforms report no total subscribers; the
		// session count until a source exists (Phase 9).
		return s.sessionCount(ctx, scope.Platform, func(st feed.SessionStats) int64 { return st.Subscribers })
	case "latest_follower":
		return s.latestUser(ctx, eventtype.ChannelFollow)
	case "latest_subscriber":
		return s.latestUser(ctx, eventtype.ChannelSubscribe)
	case "latest_raid":
		return s.latestUser(ctx, eventtype.ChannelRaid)
	default:
		return ""
	}
}

// sessionCount returns the part of the session statistics, or empty if the
// platform has no current session.
func (s *Service) sessionCount(ctx context.Context, p platform.Name, part func(feed.SessionStats) int64) string {
	st, ok, err := s.ports.SessionStats.CurrentSessionStats(ctx, p)
	if err != nil || !ok {
		return ""
	}
	return strconv.FormatInt(part(st), 10)
}

// latestUser returns the name of the most recent feed event of type, or
// empty if the feed has none.
func (s *Service) latestUser(ctx context.Context, typ event.Type) string {
	events, err := s.ports.Feed.LatestFeed(ctx, 50)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the feed for the label failed", "error", err)
		return ""
	}
	for _, event := range slices.Backward(events) {
		if event.Type == typ {
			return feedName(event)
		}
	}
	return ""
}

// feedName is the display name of a feed event, or its login.
func feedName(ev feed.Event) string {
	if ev.DisplayName != "" {
		return ev.DisplayName
	}
	return ev.Login
}

// labelConfig decodes and checks the configuration of the label widget;
// the defaults are the kind text and an empty template.
func (s *Service) labelConfig(ctx context.Context, w settings.OverlayWidget) *labelConfig {
	var c labelConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the label configuration is unreadable", "widget", w.ID, "error", err)
		return nil
	}
	if c.Type == "" {
		c.Type = "text"
	}
	if c.Type != "text" && c.Type != "date" && c.Type != "time" && !slices.Contains(labelValueKinds, c.Type) {
		s.logger.ErrorContext(ctx, "the label kind is unknown", "widget", w.ID, "kind", c.Type)
		return nil
	}
	return &c
}

// StateForLabel returns the label state for the state sync (B4): the last
// pushed text, or the date and time state.
func (s *Service) StateForLabel(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.labelConfig(ctx, w)
	if cfg == nil {
		return nil
	}
	if cfg.Type == "date" || cfg.Type == "time" {
		return s.dateTimeState(ctx, cfg.Type)
	}
	s.mu.Lock()
	text := s.labelTexts[w.ID]
	s.mu.Unlock()
	if text == "" {
		return map[string]any{"text": ""}
	}
	return map[string]any{"text": text}
}

// ---------- goal (B25, B23, A2, B50) ----------

// goalConfig is the configuration of the goal widget.
type goalConfig struct {
	// Segments are the segments of the goal, in order; at least one.
	Segments []goalSegment `json:"segments"`
	// Mode is cumulative (default) or individual.
	Mode string `json:"mode"`
	// Reset is the automatic reset: daily or monthly; empty for none.
	Reset string `json:"reset"`
}

// goalSegment is one segment of the goal.
type goalSegment struct {
	// Name is the name of the segment.
	Name string `json:"name"`
	// Amount is the amount of the segment; not negative.
	Amount int64 `json:"amount"`
}

// goalProgress grows the visible goal widgets by the amount of the event
// (B25), publishes the effects (B23, A2) and pushes the new state (B22).
func (s *Service) goalProgress(ctx context.Context, env event.Envelope) {
	payload, ok := event.Payload[eventtype.Payload](env)
	if !ok {
		return
	}
	amount, ok := goalAmount(env.Type, payload)
	if !ok {
		return
	}
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindGoal || !s.visible(w) {
			continue
		}
		s.goalStep(ctx, w, amount, payload)
	}
}

// goalAmount is the part the event adds to the goals (B25): follow,
// subscribe and raid count one each, a cheer its bits.
func goalAmount(t event.Type, p eventtype.Payload) (int64, bool) {
	switch t {
	case eventtype.ChannelFollow, eventtype.ChannelSubscribe, eventtype.ChannelRaid:
		return 1, true
	case eventtype.TwitchBitsCheer:
		if b := p.Details.Bits; b != nil {
			return b.Amount, true
		}
	default:
		// not a goal event
	}
	return 0, false
}

// goalStep adds amount to the goal of w, publishes the effects and pushes
// the new state. The step is one of the event effects (B25, B50).
func (s *Service) goalStep(ctx context.Context, w settings.OverlayWidget, amount int64, p eventtype.Payload) {
	cfg := s.goalConfig(ctx, w)
	if cfg == nil {
		return
	}
	st, err := s.goalLoad(ctx, w.ID)
	if err != nil {
		return
	}
	if s.goalResetDue(ctx, w, cfg, st) {
		st.current, st.segment, st.resetAt = 0, 0, time.Now()
	}
	st.current += amount
	if st.current < 0 {
		st.current = 0 // B50: clamped to zero
	}
	completed := s.goalSegmentsCrossed(cfg, st)
	s.goalSave(ctx, w.ID, st)
	if len(completed) > 0 {
		for _, c := range completed {
			s.publish(ctx, eventtype.OverlayGoalSegmentCompleted, eventtype.Payload{
				Platform: p.Platform,
				User:     p.User,
				Details:  eventtype.Details{GoalSegment: c.detail},
			})
		}
	} else {
		s.publish(ctx, eventtype.OverlayGoalProgress, eventtype.Payload{
			Platform: p.Platform,
			User:     p.User,
			Details: eventtype.Details{GoalProgress: &eventtype.GoalProgress{
				Total:         goalTotal(cfg, st),
				Current:       goalReached(cfg, st),
				Amount:        amount,
				SegmentName:   goalSegmentName(cfg, st),
				SegmentAmount: goalSegmentAmount(cfg, st),
			}},
		})
	}
	s.push(ctx, w, s.goalStateFor(cfg, st))
}

// goalSegmentsCrossed advances st over the segments its amount reached, in
// the ascending direction only (B25, B50), and returns them with their
// event details.
func (s *Service) goalSegmentsCrossed(cfg *goalConfig, st *goalState) []goalSegmentCompleted {
	var out []goalSegmentCompleted
	for st.segment < len(cfg.Segments) {
		boundary := goalBoundary(cfg, st)
		if st.current < boundary {
			break
		}
		seg := cfg.Segments[st.segment]
		nextName, nextAmount := "", int64(0)
		if st.segment+1 < len(cfg.Segments) {
			nextName = cfg.Segments[st.segment+1].Name
			nextAmount = cfg.Segments[st.segment+1].Amount
		}
		out = append(out, goalSegmentCompleted{
			segment: seg,
			detail: &eventtype.GoalSegment{
				SegmentName:       seg.Name,
				Current:           goalReached(cfg, st),
				NextSegmentName:   nextName,
				NextSegmentAmount: nextAmount,
				Remaining:         goalRemaining(cfg, st),
			},
		})
		st.current -= boundary
		st.segment++
	}
	return out
}

// goalSegmentCompleted is a completed segment with its event detail.
type goalSegmentCompleted struct {
	segment goalSegment
	detail  *eventtype.GoalSegment
}

// goalBoundary is the amount at which the segment of st completes (B25):
// the sum of the segments so far for the kind cumulative, the amount of
// the segment for the kind individual.
func goalBoundary(cfg *goalConfig, st *goalState) int64 {
	if cfg.Mode == "individual" {
		return cfg.Segments[st.segment].Amount
	}
	var sum int64
	for _, sg := range cfg.Segments[:st.segment+1] {
		sum += sg.Amount
	}
	return sum
}

// goalTotal is the overall amount the goal displays (B25): the sum of the
// segments for the kind cumulative, the amount of the current segment for
// the kind individual.
func goalTotal(cfg *goalConfig, st *goalState) int64 {
	if cfg.Mode == "individual" {
		if st.segment < len(cfg.Segments) {
			return cfg.Segments[st.segment].Amount
		}
		return 0
	}
	var sum int64
	for _, sg := range cfg.Segments {
		sum += sg.Amount
	}
	return sum
}

// goalReached is the overall amount reached (B25): the sum of the
// completed segments plus the amount of the current one for the kind
// cumulative, the amount of the current segment for the kind individual.
func goalReached(cfg *goalConfig, st *goalState) int64 {
	if cfg.Mode == "individual" {
		return st.current
	}
	var sum int64
	for _, sg := range cfg.Segments[:st.segment] {
		sum += sg.Amount
	}
	return sum + st.current
}

// goalRemaining is the amount still missing to the end of the goal; 0 if
// the goal is reached or overshot.
func goalRemaining(cfg *goalConfig, st *goalState) int64 {
	var rest int64
	if cfg.Mode == "individual" {
		var sum int64
		for _, sg := range cfg.Segments[st.segment:] {
			sum += sg.Amount
		}
		rest = sum - st.current
	} else {
		rest = goalTotal(cfg, st) - goalReached(cfg, st)
	}
	if rest < 0 {
		rest = 0
	}
	return rest
}

// goalSegmentName is the name of the segment the goal works on; empty if
// there is no more segment.
func goalSegmentName(cfg *goalConfig, st *goalState) string {
	if st.segment < len(cfg.Segments) {
		return cfg.Segments[st.segment].Name
	}
	return ""
}

// goalSegmentAmount is the amount of the segment the goal works on; 0 if
// there is no more segment.
func goalSegmentAmount(cfg *goalConfig, st *goalState) int64 {
	if st.segment < len(cfg.Segments) {
		return cfg.Segments[st.segment].Amount
	}
	return 0
}

// goalResetDue reports whether the automatic reset of the goal has run its
// course (B25): the last reset must lie before the start of the current
// day or month of the profile.
func (s *Service) goalResetDue(ctx context.Context, w settings.OverlayWidget, cfg *goalConfig, st *goalState) bool {
	switch cfg.Reset {
	case "":
		return false
	case "daily", "monthly":
	default:
		return false
	}
	scope, err := s.ports.Scope(ctx)
	if err != nil || scope.Location == nil {
		s.logger.ErrorContext(ctx, "the goal reset has no time zone", "widget", w.ID, "error", err)
		return false
	}
	now := time.Now().In(scope.Location)
	var start time.Time
	if cfg.Reset == "daily" {
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, scope.Location)
	} else {
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, scope.Location)
	}
	return st.resetAt.Before(start)
}

// goalReset runs the automatic resets of the visible goal widgets on the
// tick (B25).
func (s *Service) goalReset(ctx context.Context) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindGoal || !s.visible(w) {
			continue
		}
		cfg := s.goalConfig(ctx, w)
		if cfg == nil || cfg.Reset == "" {
			continue
		}
		st, err := s.goalLoad(ctx, w.ID)
		if err != nil {
			continue
		}
		if !s.goalResetDue(ctx, w, cfg, st) {
			continue
		}
		st.current, st.segment, st.resetAt = 0, 0, time.Now()
		s.goalSave(ctx, w.ID, st)
		s.push(ctx, w, s.goalStateFor(cfg, st))
	}
}

// goalLoad returns the running state of the goal, loaded from the profile
// on first use (B20).
func (s *Service) goalLoad(ctx context.Context, widgetID string) (*goalState, error) {
	s.mu.Lock()
	if st, ok := s.goals[widgetID]; ok {
		s.mu.Unlock()
		return st, nil
	}
	s.mu.Unlock()
	doc, found, err := s.ports.State.OverlayWidgetState(ctx, widgetID)
	if err != nil {
		return nil, err
	}
	st := &goalState{}
	if found {
		var docGoal goalDoc
		if err := json.Unmarshal(doc, &docGoal); err != nil {
			s.logger.ErrorContext(ctx, "the goal state is unreadable, starting fresh", "widget", widgetID, "error", err)
		} else {
			st.current, st.segment, st.resetAt = docGoal.Current, docGoal.Segment, docGoal.ResetAt
		}
	}
	s.mu.Lock()
	s.goals[widgetID] = st
	s.mu.Unlock()
	return st, nil
}

// goalSave stores the state of the goal in the profile (B20).
func (s *Service) goalSave(ctx context.Context, widgetID string, st *goalState) {
	doc, err := json.Marshal(goalDoc{Current: st.current, Segment: st.segment, ResetAt: st.resetAt})
	if err != nil {
		s.logger.ErrorContext(ctx, "storing the goal state failed", "widget", widgetID, "error", err)
		return
	}
	if err := s.ports.State.SetOverlayWidgetState(ctx, widgetID, doc); err != nil {
		s.logger.ErrorContext(ctx, "storing the goal state failed", "widget", widgetID, "error", err)
	}
}

// goalStateFor is the runtime state of the goal widget (B25): the overall
// amount, the amount reached and the segments.
func (s *Service) goalStateFor(cfg *goalConfig, st *goalState) map[string]any {
	total := goalTotal(cfg, st)
	current := goalReached(cfg, st)
	if cfg.Mode != "individual" && current > total {
		current = total
	}
	segments := make([]map[string]any, len(cfg.Segments))
	for i, sg := range cfg.Segments {
		segments[i] = map[string]any{"name": sg.Name, "amount": sg.Amount}
	}
	return map[string]any{
		"total":    total,
		"current":  current,
		"segments": segments,
	}
}

// goalConfig decodes and checks the configuration of the goal widget:
// at least one segment of a positive amount, a known mode and a known
// reset (B25).
func (s *Service) goalConfig(ctx context.Context, w settings.OverlayWidget) *goalConfig {
	var c goalConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the goal configuration is unreadable", "widget", w.ID, "error", err)
		return nil
	}
	if len(c.Segments) < 1 {
		s.logger.ErrorContext(ctx, "the goal has no segments", "widget", w.ID)
		return nil
	}
	for i, sg := range c.Segments {
		if sg.Name == "" || sg.Amount < 1 {
			s.logger.ErrorContext(ctx, "a goal segment needs a name and a positive amount", "widget", w.ID, "segment", i)
			return nil
		}
	}
	if c.Mode != "" && c.Mode != "cumulative" && c.Mode != "individual" {
		s.logger.ErrorContext(ctx, "the goal mode is unknown", "widget", w.ID, "mode", c.Mode)
		return nil
	}
	if c.Reset != "" && c.Reset != "daily" && c.Reset != "monthly" {
		s.logger.ErrorContext(ctx, "the goal reset is unknown", "widget", w.ID, "reset", c.Reset)
		return nil
	}
	return &c
}

// AddToGoal adds amount, which may be negative (B25, B50), to the goal
// widget and pushes the progress.
func (s *Service) AddToGoal(ctx context.Context, widgetID string, amount int64, p platform.Name, u *user.User) error {
	w, err := s.widget(ctx, widgetID, KindGoal)
	if err != nil {
		return err
	}
	s.goalStep(ctx, w, amount, eventtype.Payload{Platform: p, User: u})
	return nil
}

// StateForGoal returns the goal state for the state sync (B4, B20).
func (s *Service) StateForGoal(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.goalConfig(ctx, w)
	if cfg == nil {
		return nil
	}
	st, err := s.goalLoad(ctx, w.ID)
	if err != nil {
		return nil
	}
	return s.goalStateFor(cfg, st)
}

// ---------- timer (B26) ----------

// timerConfig is the configuration of the timer widget.
type timerConfig struct {
	// Duration is the initial duration as text; empty for none.
	Duration string `json:"duration"`
	// Format is the display format (B17); empty for MM:SS.
	Format string `json:"format"`
	// duration is the parsed initial duration.
	duration time.Duration
}

// startTimers starts the visible timer widgets that have no running state
// yet (B26): paused, with their configured duration.
func (s *Service) startTimers(ctx context.Context) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindTimer || !s.visible(w) {
			continue
		}
		s.mu.Lock()
		_, started := s.timers[w.ID]
		s.mu.Unlock()
		if started {
			continue
		}
		cfg := s.timerConfig(ctx, w)
		st := &timerState{paused: true, pausedFor: cfg.duration}
		s.mu.Lock()
		if _, taken := s.timers[w.ID]; !taken {
			s.timers[w.ID] = st
		} else {
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()
		s.pushTimer(ctx, w, cfg, st)
	}
}

// UpdateTimer sets a new duration, or resets the timer widget to its
// configured duration (B26).
func (s *Service) UpdateTimer(ctx context.Context, widgetID string, d time.Duration, reset bool) error {
	w, err := s.widget(ctx, widgetID, KindTimer)
	if err != nil {
		return err
	}
	cfg := s.timerConfig(ctx, w)
	if reset {
		if cfg.duration <= 0 {
			return fmt.Errorf("%w: the timer widget has no configured duration", ErrUnknownWidget)
		}
		d = cfg.duration
	}
	if d <= 0 {
		return fmt.Errorf("widgets: the timer duration %s must be positive", d)
	}
	st := &timerState{deadline: time.Now().Add(d)}
	s.mu.Lock()
	s.timers[widgetID] = st
	s.mu.Unlock()
	s.pushTimer(ctx, w, cfg, st)
	return nil
}

// AddToTimer adds d, which may be negative (B26, B38), to the remaining
// time of the timer widget and pushes the new state.
func (s *Service) AddToTimer(ctx context.Context, widgetID string, d time.Duration) error {
	w, err := s.widget(ctx, widgetID, KindTimer)
	if err != nil {
		return err
	}
	cfg := s.timerConfig(ctx, w)
	s.mu.Lock()
	st := s.timers[widgetID]
	if st == nil {
		st = &timerState{paused: true, pausedFor: cfg.duration}
		s.timers[widgetID] = st
	}
	if st.paused {
		st.pausedFor += d
		if st.pausedFor < 0 {
			st.pausedFor = 0
		}
	} else {
		st.deadline = st.deadline.Add(d)
	}
	s.mu.Unlock()
	s.pushTimer(ctx, w, cfg, st)
	return nil
}

// PauseTimer pauses the timer widget, or resumes it while it is paused
// (B26).
func (s *Service) PauseTimer(ctx context.Context, widgetID string) error {
	w, err := s.widget(ctx, widgetID, KindTimer)
	if err != nil {
		return err
	}
	cfg := s.timerConfig(ctx, w)
	s.mu.Lock()
	st := s.timers[widgetID]
	switch {
	case st == nil:
		st = &timerState{paused: true, pausedFor: cfg.duration}
		s.timers[widgetID] = st
	case st.paused:
		st.deadline = time.Now().Add(st.pausedFor)
		st.paused = false
	default:
		st.pausedFor = max(time.Until(st.deadline), 0)
		st.paused = true
	}
	s.mu.Unlock()
	s.pushTimer(ctx, w, cfg, st)
	return nil
}

// pushTimer pushes the state of the timer widget (B26); the runtime
// renders it per second from the server time of hello.
func (s *Service) pushTimer(ctx context.Context, w settings.OverlayWidget, cfg *timerConfig, st *timerState) {
	s.push(ctx, w, s.timerStateFor(cfg, st))
}

// timerStateFor is the runtime state of the timer widget.
func (s *Service) timerStateFor(cfg *timerConfig, st *timerState) map[string]any {
	return map[string]any{
		"deadline": st.deadline.UnixMilli(),
		"paused":   st.paused,
		"pausedMs": st.pausedFor.Milliseconds(),
		"format":   cfg.Format,
	}
}

// StateForTimer returns the timer state for the state sync (B4, B26).
func (s *Service) StateForTimer(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.timerConfig(ctx, w)
	s.mu.Lock()
	st := s.timers[w.ID]
	s.mu.Unlock()
	if st == nil {
		st = &timerState{paused: true, pausedFor: cfg.duration}
	}
	return s.timerStateFor(cfg, st)
}

// timerConfig decodes the configuration of the timer widget; the format
// defaults to MM:SS (B17, B26).
func (s *Service) timerConfig(ctx context.Context, w settings.OverlayWidget) *timerConfig {
	var c timerConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the timer configuration is unreadable", "widget", w.ID, "error", err)
		return &timerConfig{Format: "MM:SS"}
	}
	if c.Duration != "" {
		d, err := time.ParseDuration(c.Duration)
		if err != nil {
			s.logger.ErrorContext(ctx, "the timer duration is unknown", "widget", w.ID, "duration", c.Duration)
			return &timerConfig{Format: "MM:SS"}
		}
		c.duration = d
	}
	if c.Format == "" {
		c.Format = "MM:SS"
	}
	return &c
}

// ---------- event_list (B27) ----------

// eventListConfig is the configuration of the event_list widget.
type eventListConfig struct {
	// Types are the feed types the list shows; empty for all of them.
	Types []string `json:"types"`
	// Max is the maximum number of entries; 0 for the default of 10.
	Max int `json:"max"`
}

// refreshList pushes the lists of the visible event_list widgets (B27).
func (s *Service) refreshList(ctx context.Context) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindEventList || !s.visible(w) {
			continue
		}
		s.pushList(ctx, w)
	}
}

// pushList pushes the state of one event_list widget: the dynamic entries
// and the recent feed events of its types, newest first, capped at its
// maximum (B27).
func (s *Service) pushList(ctx context.Context, w settings.OverlayWidget) {
	cfg := s.listConfig(ctx, w)
	entries, ok := s.feedEntries(ctx, cfg)
	if !ok {
		return
	}
	s.mu.Lock()
	dyn := slices.Clone(s.lists[w.ID])
	s.mu.Unlock()
	dyn = append(dyn, entries...)
	sortEntries(dyn)
	if len(dyn) > cfg.Max {
		dyn = dyn[:cfg.Max]
	}
	list := make([]listEntry, len(dyn))
	copy(list, dyn)
	s.push(ctx, w, map[string]any{"entries": list})
}

// feedEntries are the recent feed events of the types of cfg, newest
// first; ok is false if the feed could not be read.
func (s *Service) feedEntries(ctx context.Context, cfg *eventListConfig) ([]listEntry, bool) {
	events, err := s.ports.Feed.LatestFeed(ctx, cfg.Max)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the feed for the event list failed", "error", err)
		return nil, false
	}
	var out []listEntry
	for _, ev := range slices.Backward(events) {

		if len(cfg.Types) > 0 && !slices.Contains(cfg.Types, string(ev.Type)) {
			continue
		}
		out = append(out, listEntry{User: feedName(ev), Type: string(ev.Type), Text: ev.Text, Time: ev.Time.UnixMilli()})
	}
	return out, true
}

// AddToEventList adds a dynamic entry, the user and the details text of
// the call (B27).
func (s *Service) AddToEventList(ctx context.Context, widgetID, user, text string) error {
	w, err := s.widget(ctx, widgetID, KindEventList)
	if err != nil {
		return err
	}
	cfg := s.listConfig(ctx, w)
	entry := listEntry{User: user, Text: text, Time: time.Now().UnixMilli()}
	s.mu.Lock()
	list := append([]listEntry{entry}, s.lists[widgetID]...)
	if len(list) > cfg.Max {
		list = list[:cfg.Max]
	}
	s.lists[widgetID] = list
	s.mu.Unlock()
	if s.visible(w) {
		s.pushList(ctx, w)
	}
	return nil
}

// StateForList returns the list state for the state sync (B4, B27).
func (s *Service) StateForList(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.listConfig(ctx, w)
	entries, ok := s.feedEntries(ctx, cfg)
	if !ok {
		return nil
	}
	s.mu.Lock()
	dyn := slices.Clone(s.lists[w.ID])
	s.mu.Unlock()
	dyn = append(dyn, entries...)
	sortEntries(dyn)
	if len(dyn) > cfg.Max {
		dyn = dyn[:cfg.Max]
	}
	list := make([]listEntry, len(dyn))
	copy(list, dyn)
	return map[string]any{"entries": list}
}

// listConfig decodes the configuration of the event_list widget; the
// defaults are all feed types and 10 entries (B27).
func (s *Service) listConfig(ctx context.Context, w settings.OverlayWidget) *eventListConfig {
	var c eventListConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the event list configuration is unreadable", "widget", w.ID, "error", err)
		return &eventListConfig{Max: 10}
	}
	if c.Max < 1 {
		c.Max = 10
	}
	if len(c.Types) > 0 {
		for _, typ := range c.Types {
			if !slices.Contains(FeedTypes, event.Type(typ)) {
				s.logger.ErrorContext(ctx, "the event list type is not a feed type", "widget", w.ID, "type", typ)
				return &eventListConfig{Max: 10}
			}
		}
	}
	return &c
}

// sortEntries sorts the entries by time, newest first.
func sortEntries(entries []listEntry) {
	slices.SortFunc(entries, func(a, b listEntry) int {
		if a.Time != b.Time {
			return int(b.Time - a.Time)
		}
		return 0
	})
}

// ---------- chat (B28) ----------

// chatConfig is the configuration of the chat widget.
type chatConfig struct {
	// Platforms are the platforms the widget shows; empty for all.
	Platforms []string `json:"platforms"`
	// DelayMs is the delay before a message shows.
	DelayMs int `json:"delayMs"`
	// RemoveAfterMs is the time after which a message leaves the widget;
	// 0 for always visible.
	RemoveAfterMs int `json:"removeAfterMs"`
	// Max is the maximum number of messages; 0 for the default of 20.
	Max int `json:"max"`
}

// chatMessage adds the message of a visible chat widget, after its delay
// (B28).
func (s *Service) chatMessage(ctx context.Context, env event.Envelope) {
	payload, ok := event.Payload[eventtype.Payload](env)
	if !ok || payload.Details.Message == nil || payload.User == nil {
		return
	}
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindChat || !s.visible(w) {
			continue
		}
		cfg := s.chatConfig(ctx, w)
		if len(cfg.Platforms) > 0 && !slices.Contains(cfg.Platforms, string(payload.Platform)) {
			continue
		}
		userName := ""
		if ident, ok := payload.User.Identity(payload.Platform); ok {
			userName = ident.DisplayName
			if userName == "" {
				userName = ident.Login
			}
		}
		msg := chatMsg{ID: payload.Details.Message.ID, UserID: payload.User.ID.String(), User: userName, Text: payload.Details.Message.Text, Platform: string(payload.Platform)}
		add := func() {
			s.mu.Lock()
			chats := append(slices.Clone(s.chats[w.ID]), msg)
			if len(chats) > cfg.Max {
				chats = chats[len(chats)-cfg.Max:]
			}
			s.chats[w.ID] = chats
			s.mu.Unlock()
			s.pushChat(ctx, w)
			if cfg.RemoveAfterMs > 0 {
				time.AfterFunc(time.Duration(cfg.RemoveAfterMs)*time.Millisecond, func() {
					s.chatRemove(w.ID, msg)
					s.pushChat(ctx, w)
				})
			}
		}
		if cfg.DelayMs > 0 {
			time.AfterFunc(time.Duration(cfg.DelayMs)*time.Millisecond, add)
		} else {
			add()
		}
	}
}

// chatDeleted removes the deleted message from the chat widgets (B28):
// the delete event names the message by the platform's ID.
func (s *Service) chatDeleted(ctx context.Context, env event.Envelope) {
	payload, ok := event.Payload[eventtype.Payload](env)
	if !ok || payload.Details.Message == nil || payload.Details.Message.ID == "" {
		return
	}
	msgID := payload.Details.Message.ID
	plat := string(payload.Platform)
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	changed := s.filterChats(func(m chatMsg) bool { return m.ID == msgID && m.Platform == plat })
	for _, w := range sec.Widgets {
		if w.Kind == KindChat && changed[w.ID] {
			s.pushChat(ctx, w)
		}
	}
}

// chatModerated removes the messages of the affected user from the chat
// widgets (B28): a timed out or banned user leaves no trace.
func (s *Service) chatModerated(ctx context.Context, env event.Envelope) {
	payload, ok := event.Payload[eventtype.Payload](env)
	if !ok || payload.User == nil {
		return
	}
	userID := payload.User.ID.String()
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	changed := s.filterChats(func(m chatMsg) bool { return m.UserID == userID })
	for _, w := range sec.Widgets {
		if w.Kind == KindChat && changed[w.ID] {
			s.pushChat(ctx, w)
		}
	}
}

// filterChats removes the messages matching keepOut of all the chat
// widgets and returns the IDs of the widgets that changed.
func (s *Service) filterChats(keepOut func(chatMsg) bool) map[string]bool {
	s.mu.Lock()
	changed := map[string]bool{}
	for id, chats := range s.chats {
		kept := chats[:0]
		for _, m := range chats {
			if keepOut(m) {
				continue
			}
			kept = append(kept, m)
		}
		if len(kept) != len(chats) {
			s.chats[id] = kept
			changed[id] = true
		}
	}
	s.mu.Unlock()
	return changed
}

// chatRemove removes one message of the widget by its user and text.
func (s *Service) chatRemove(widgetID string, msg chatMsg) {
	s.mu.Lock()
	chats := s.chats[widgetID]
	kept := chats[:0]
	for _, m := range chats {
		if m.UserID == msg.UserID && m.Text == msg.Text {
			continue
		}
		kept = append(kept, m)
	}
	s.chats[widgetID] = kept
	s.mu.Unlock()
}

// pushChat pushes the state of the chat widget (B28).
func (s *Service) pushChat(ctx context.Context, w settings.OverlayWidget) {
	s.mu.Lock()
	msgs := make([]chatMsg, len(s.chats[w.ID]))
	copy(msgs, s.chats[w.ID])
	s.mu.Unlock()
	if !s.visible(w) {
		return
	}
	s.push(ctx, w, map[string]any{"messages": msgs})
}

// StateForChat returns the chat state for the state sync (B4, B28).
func (s *Service) StateForChat(_ context.Context, w settings.OverlayWidget) any {
	s.mu.Lock()
	msgs := make([]chatMsg, len(s.chats[w.ID]))
	copy(msgs, s.chats[w.ID])
	s.mu.Unlock()
	return map[string]any{"messages": msgs}
}

// chatConfig decodes the configuration of the chat widget; the defaults
// are all platforms and 20 messages (B28).
func (s *Service) chatConfig(ctx context.Context, w settings.OverlayWidget) *chatConfig {
	var c chatConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the chat configuration is unreadable", "widget", w.ID, "error", err)
		return &chatConfig{Max: 20}
	}
	if c.Max < 1 {
		c.Max = 20
	}
	if c.DelayMs < 0 || c.RemoveAfterMs < 0 {
		s.logger.ErrorContext(ctx, "the chat delays must be 0 or more", "widget", w.ID)
		return &chatConfig{Max: 20}
	}
	return &c
}

// ---------- shared ----------

// widget finds the widget in the section; it must be of the kind want.
func (s *Service) widget(ctx context.Context, widgetID, want string) (settings.OverlayWidget, error) {
	sec := s.section(ctx)
	if sec == nil {
		return settings.OverlayWidget{}, errors.New("widgets: the overlay section is unknown")
	}
	for _, w := range sec.Widgets {
		if w.ID != widgetID {
			continue
		}
		if w.Kind != want {
			return w, fmt.Errorf("%w: the widget %q is a %s, not a %s", ErrUnknownWidget, widgetID, w.Kind, want)
		}
		return w, nil
	}
	return settings.OverlayWidget{}, fmt.Errorf("%w: the widget %q is unknown", ErrUnknownWidget, widgetID)
}

// decodeConfig decodes the configuration document of a widget into out;
// a nil document keeps the defaults of out.
func decodeConfig(cfg any, out any) error {
	if cfg == nil {
		return nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// publish publishes an effect of the widgets on the bus (B23, A2).
func (s *Service) publish(ctx context.Context, typ event.Type, p eventtype.Payload) {
	env := event.New(event.Source{Kind: event.SourceSystem, Name: "overlay"}, typ, p)
	if err := s.ports.Bus.Publish(ctx, env); err != nil {
		s.logger.ErrorContext(ctx, "publishing the widget event failed", "type", typ, "error", err)
	}
}

// StateForSync returns the runtime state of the widget for the state sync
// of a (re)connecting runtime (B4, B20); nil for a kind without a state.
func (s *Service) StateForSync(ctx context.Context, w settings.OverlayWidget) any {
	switch w.Kind {
	case KindLabel:
		return s.StateForLabel(ctx, w)
	case KindGoal:
		return s.StateForGoal(ctx, w)
	case KindTimer:
		return s.StateForTimer(ctx, w)
	case KindEventList:
		return s.StateForList(ctx, w)
	case KindChat:
		return s.StateForChat(ctx, w)
	default:
		return nil
	}
}
