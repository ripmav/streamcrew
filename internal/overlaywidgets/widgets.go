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
	"math"
	"math/rand"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/discord"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/feed"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/emote"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/poll"
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

// The kinds of the widgets of 7.5 (B29-B37).
const (
	KindPersistentTimer = "persistent_timer"
	KindStreamBoss      = "stream_boss"
	KindEndCredits      = "end_credits"
	KindLeaderboard     = "leaderboard"
	KindWheel           = "wheel"
	KindPoll            = "poll"
	KindEmoteEffect     = "persistent_emote_effect"
	KindGameQueue       = "game_queue"
	KindDiscordVoice    = "discord_voice"
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
	// TopUsers ranks the users of the profile by a statistic for the
	// leaderboard widget (B32).
	TopUsers func(ctx context.Context, stat user.Stat, n int) ([]user.Ranked, error)
	// Emotes lists the emotes of the home channel for the emote effect
	// (B35).
	Emotes func(ctx context.Context) []emote.Emote
	// DiscordOccupants lists the current occupants of the configured
	// Discord voice channel for the discord_voice widget (B37).
	DiscordOccupants func(ctx context.Context) []discord.Occupant
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
	// pollState is the state of the active vote; all poll widgets show
	// it (B34).
	pollState poll.State
	hasPoll   bool
	// persistentTimers are the running persistent timers by widget ID
	// (B29); they are persisted in the profile (B20).
	persistentTimers map[string]*persistentTimerState
	// bosses are the running stream bosses by widget ID (B30); in memory.
	bosses map[string]*streamBossState
	// wheels are the running probabilities of the wheel widgets by widget
	// ID (B33).
	wheels map[string]*wheelState
	// gameQueue is the queue of the profile; all game_queue widgets show
	// it (B36).
	gameQueue []gameEntry
	// gameQueueLoaded reports whether the queue was read from the state.
	gameQueueLoaded bool
	// credits are the collected sections of the end_credits widgets by
	// widget ID (B31).
	credits map[string]*endCreditsState
	// emoteCombos are the running combo counts of the persistent emote
	// effect widgets by widget ID (B35).
	emoteCombos map[string]*emoteEffectState
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
	case p.TopUsers == nil || p.Emotes == nil || p.DiscordOccupants == nil:
		return nil, errors.New("new widget service: missing port")
	}
	s := &Service{
		ports:            p,
		logger:           slog.New(slog.DiscardHandler),
		goals:            map[string]*goalState{},
		timers:           map[string]*timerState{},
		lists:            map[string][]listEntry{},
		chats:            map[string][]chatMsg{},
		labelTexts:       map[string]string{},
		persistentTimers: map[string]*persistentTimerState{},
		bosses:           map[string]*streamBossState{},
		wheels:           map[string]*wheelState{},
		credits:          map[string]*endCreditsState{},
		emoteCombos:      map[string]*emoteEffectState{},
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
			eventtype.ChannelStreamStart,
			eventtype.DiscordVoiceJoined,
			eventtype.DiscordVoiceLeft,
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
		s.persistentTimerProgress(ctx, env)
		s.streamBossStep(ctx, env)
		s.endCreditsCollect(ctx, env)
		s.refreshLeaderboard(ctx)
	case eventtype.ChatMessage:
		s.chatMessage(ctx, env)
		s.endCreditsCollect(ctx, env)
		s.emoteEffectMessage(ctx, env)
	case eventtype.ChatMessageDelete:
		s.chatDeleted(ctx, env)
	case eventtype.ChatUserTimeout, eventtype.ChatUserBan:
		s.chatModerated(ctx, env)
		s.endCreditsCollect(ctx, env)
	case eventtype.ChannelStreamStart:
		s.endCreditsClear(ctx)
	case eventtype.DiscordVoiceJoined, eventtype.DiscordVoiceLeft:
		s.refreshDiscordVoice(ctx)
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
	s.persistentTimerTick(ctx)
	s.refreshLeaderboard(ctx)
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

// LabelKind is the kind of the label widget (B24).
type LabelKind string

// The kinds of the label widget (B24); date and time the runtime renders
// locally, text the core as a template.
const (
	// LabelKindText is the default kind: a template text.
	LabelKindText LabelKind = "text"
	// LabelKindDate renders the date in the runtime (B24, A5).
	LabelKindDate LabelKind = "date"
	// LabelKindTime renders the time in the runtime (B24, A5).
	LabelKindTime LabelKind = "time"
	// LabelKindViewerCount shows the viewer count of the platform.
	LabelKindViewerCount LabelKind = "viewer_count"
	// LabelKindChatterCount shows the chatter count of the platform.
	LabelKindChatterCount LabelKind = "chatter_count"
	// LabelKindLatestFollower shows the name of the latest follower.
	LabelKindLatestFollower LabelKind = "latest_follower"
	// LabelKindTotalFollowers shows the count of the followers.
	LabelKindTotalFollowers LabelKind = "total_followers"
	// LabelKindLatestSubscriber shows the name of the latest subscriber.
	LabelKindLatestSubscriber LabelKind = "latest_subscriber"
	// LabelKindTotalSubscribers shows the count of the subscribers.
	LabelKindTotalSubscribers LabelKind = "total_subscribers"
	// LabelKindLatestRaid shows the name of the latest raid.
	LabelKindLatestRaid LabelKind = "latest_raid"
)

// labelValueKinds are the kinds the core renders from its sources (B24).
//
//nolint:gochecknoglobals // static table of value kinds
var labelValueKinds = []LabelKind{
	LabelKindViewerCount, LabelKindChatterCount, LabelKindLatestFollower,
	LabelKindTotalFollowers, LabelKindLatestSubscriber, LabelKindTotalSubscribers, LabelKindLatestRaid,
}

// Valid reports whether k is a known label kind.
func (k LabelKind) Valid() bool {
	return k == LabelKindText || k == LabelKindDate || k == LabelKindTime || slices.Contains(labelValueKinds, k)
}

// labelConfig is the configuration of the label widget.
type labelConfig struct {
	// Type is the kind of the label; empty for text.
	Type LabelKind `json:"type"`
	// Text is the template of the kind text.
	Text string `json:"text"`
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
	case cfg.Type == LabelKindDate || cfg.Type == LabelKindTime:
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
func (s *Service) dateTimeState(ctx context.Context, kind LabelKind) map[string]any {
	loc := template.LocaleUSEnglish
	if l, err := s.ports.Locale(ctx); err == nil {
		loc = l
	}
	zone := "UTC"
	if sc, err := s.ports.Scope(ctx); err == nil && sc.Location != nil {
		zone = sc.Location.String()
	}
	st := map[string]any{"kind": string(kind), "locale": string(loc)}
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
func (s *Service) labelValue(ctx context.Context, kind LabelKind, scope *template.Scope) string {
	switch kind {
	case LabelKindViewerCount, LabelKindChatterCount, LabelKindTotalFollowers:
		st, err := s.ports.StreamStates.StreamState(ctx, scope.Platform)
		if err != nil {
			return ""
		}
		switch kind {
		case LabelKindViewerCount:
			if st.Viewers != nil {
				return strconv.FormatInt(*st.Viewers, 10)
			}
		case LabelKindChatterCount:
			if st.Chatters != nil {
				return strconv.FormatInt(*st.Chatters, 10)
			}
		default: // LabelKindTotalFollowers
			if st.Followers != nil {
				return strconv.FormatInt(*st.Followers, 10)
			}
			// shortcut: the platforms report no total followers; the
			// session count until a source exists (Phase 9).
			return s.sessionCount(ctx, scope.Platform, func(st feed.SessionStats) int64 { return st.Followers })
		}
		return "" // the value of the kind is unknown
	case LabelKindTotalSubscribers:
		// shortcut: the platforms report no total subscribers; the
		// session count until a source exists (Phase 9).
		return s.sessionCount(ctx, scope.Platform, func(st feed.SessionStats) int64 { return st.Subscribers })
	case LabelKindLatestFollower:
		return s.latestUser(ctx, eventtype.ChannelFollow)
	case LabelKindLatestSubscriber:
		return s.latestUser(ctx, eventtype.ChannelSubscribe)
	case LabelKindLatestRaid:
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
	events, err := s.ports.Feed.LatestFeed(ctx, labelFeedScan)
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

// labelFeedScan is how many recent feed events the latest_* labels scan
// for the user (B24).
const labelFeedScan = 50

// labelConfig decodes and checks the configuration of the label widget;
// the defaults are the kind text and an empty template.
func (s *Service) labelConfig(ctx context.Context, w settings.OverlayWidget) *labelConfig {
	var c labelConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the label configuration is unreadable", "widget", w.ID, "error", err)
		return nil
	}
	if c.Type == "" {
		c.Type = LabelKindText
	}
	if !c.Type.Valid() {
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
	if cfg.Type == LabelKindDate || cfg.Type == LabelKindTime {
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

// GoalMode is the mode of the goal widget (B25).
type GoalMode string

// The modes of the goal widget (B25); cumulative is the default.
const (
	// GoalModeCumulative is the default mode: the segments build on each
	// other, the display carries the overall amount.
	GoalModeCumulative GoalMode = "cumulative"
	// GoalModeIndividual works on each segment on its own.
	GoalModeIndividual GoalMode = "individual"
)

//
//nolint:gochecknoglobals // static table of goal modes
var goalModes = []GoalMode{GoalModeCumulative, GoalModeIndividual}

// Valid reports whether m is a known goal mode.
func (m GoalMode) Valid() bool {
	return slices.Contains(goalModes, m)
}

// GoalReset is the automatic reset of the goal widget (B25).
type GoalReset string

// The automatic resets of the goal widget (B25).
const (
	// GoalResetDaily runs the reset at the start of each day in the
	// time zone of the profile.
	GoalResetDaily GoalReset = "daily"
	// GoalResetMonthly runs the reset at the start of each month in the
	// time zone of the profile.
	GoalResetMonthly GoalReset = "monthly"
)

//
//nolint:gochecknoglobals // static table of goal resets
var goalResets = []GoalReset{GoalResetDaily, GoalResetMonthly}

// Valid reports whether r is a known goal reset.
func (r GoalReset) Valid() bool {
	return slices.Contains(goalResets, r)
}

// goalConfig is the configuration of the goal widget.
type goalConfig struct {
	// Segments are the segments of the goal, in order; at least one.
	Segments []goalSegment `json:"segments"`
	// Mode is cumulative (default) or individual.
	Mode GoalMode `json:"mode"`
	// Reset is the automatic reset: daily or monthly; empty for none.
	Reset GoalReset `json:"reset"`
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
		if err := s.goalStep(ctx, w, amount, payload); err != nil {
			s.logger.ErrorContext(ctx, "the goal step failed", "widget", w.ID, "error", err)
		}
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
//
// Review 344-8: the load, the change and the save run under the lock, and
// the push takes its snapshot under the lock, as the bus goroutine and
// the actions share the state.
func (s *Service) goalStep(ctx context.Context, w settings.OverlayWidget, amount int64, p eventtype.Payload) error {
	cfg := s.goalConfig(ctx, w)
	if cfg == nil {
		return nil
	}
	s.mu.Lock()
	st, err := s.goalLoadLocked(ctx, w.ID)
	var completed []goalSegmentCompleted
	var state map[string]any
	prog := &eventtype.GoalProgress{Amount: amount}
	if err == nil {
		if s.goalResetDue(ctx, w, cfg, st) {
			st.current, st.segment, st.resetAt = 0, 0, time.Now()
		}
		st.current += amount
		if st.current < 0 {
			st.current = 0 // B50: clamped to zero
		}
		completed = goalSegmentsCrossed(cfg, st)
		s.goalSave(ctx, w.ID, st)
		state = s.goalStateFor(cfg, st)
		if len(completed) == 0 {
			prog.Total = goalTotal(cfg, st)
			prog.Current = goalReached(cfg, st)
			prog.SegmentName = goalSegmentName(cfg, st)
			prog.SegmentAmount = goalSegmentAmount(cfg, st)
		}
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
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
			Details:  eventtype.Details{GoalProgress: prog},
		})
	}
	s.push(ctx, w, state)
	return nil
}

// goalSegmentsCrossed advances st over the segments its amount reached, in
// the ascending direction only (B25, B50), and returns them with their
// event details; it is called under the lock.
func goalSegmentsCrossed(cfg *goalConfig, st *goalState) []goalSegmentCompleted {
	var out []goalSegmentCompleted
	for st.segment < len(cfg.Segments) {
		boundary := goalBoundary(st, cfg.Segments)
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
// the amount of the segment in both modes, as the progress is relative to
// the current segment after every crossing (Review 344-4).
func goalBoundary(st *goalState, segs []goalSegment) int64 {
	return segs[st.segment].Amount
}

// goalTotal is the overall amount the goal displays (B25): the sum of the
// segments for the kind cumulative, the amount of the current segment for
// the kind individual.
func goalTotal(cfg *goalConfig, st *goalState) int64 {
	if cfg.Mode == GoalModeIndividual {
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
	if cfg.Mode == GoalModeIndividual {
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
	if cfg.Mode == GoalModeIndividual {
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
	if cfg.Reset == "" || !cfg.Reset.Valid() {
		return false
	}
	scope, err := s.ports.Scope(ctx)
	if err != nil || scope.Location == nil {
		s.logger.ErrorContext(ctx, "the goal reset has no time zone", "widget", w.ID, "error", err)
		return false
	}
	now := time.Now().In(scope.Location)
	var start time.Time
	if cfg.Reset == GoalResetDaily {
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
		s.mu.Lock()
		st, err := s.goalLoadLocked(ctx, w.ID)
		var state map[string]any
		ran := false
		if err == nil && s.goalResetDue(ctx, w, cfg, st) {
			st.current, st.segment, st.resetAt = 0, 0, time.Now()
			s.goalSave(ctx, w.ID, st)
			state = s.goalStateFor(cfg, st)
			ran = true
		}
		s.mu.Unlock()
		if err != nil {
			s.logger.ErrorContext(ctx, "the goal reset failed", "widget", w.ID, "error", err)
			continue
		}
		if !ran {
			continue
		}
		s.push(ctx, w, state)
	}
}

// goalLoadLocked returns the running state of the goal, loaded from the
// profile on first use (B20); s.mu is held (Review 344-8).
func (s *Service) goalLoadLocked(ctx context.Context, widgetID string) (*goalState, error) {
	if st, ok := s.goals[widgetID]; ok {
		return st, nil
	}
	doc, found, err := s.ports.State.OverlayWidgetState(ctx, widgetID)
	if err != nil {
		return nil, fmt.Errorf("the goal state of %s: %w", widgetID, err)
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
	s.goals[widgetID] = st
	return st, nil
}

// goalSave stores the state of the goal in the profile (B20); it is called
// under the lock (Review 344-8).
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
	if cfg.Mode != GoalModeIndividual && current > total {
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
	if c.Mode != "" && !c.Mode.Valid() {
		s.logger.ErrorContext(ctx, "the goal mode is unknown", "widget", w.ID, "mode", c.Mode)
		return nil
	}
	if c.Reset != "" && !c.Reset.Valid() {
		s.logger.ErrorContext(ctx, "the goal reset is unknown", "widget", w.ID, "reset", c.Reset)
		return nil
	}
	return &c
}

// AddToGoal adds amount, which may be negative (B25, B50), to the goal
// widget and pushes the progress; a state error is returned (Review
// 344-11).
func (s *Service) AddToGoal(ctx context.Context, widgetID string, amount int64, p platform.Name, u *user.User) error {
	w, err := s.widget(ctx, widgetID, KindGoal)
	if err != nil {
		return err
	}
	return s.goalStep(ctx, w, amount, eventtype.Payload{Platform: p, User: u})
}

// StateForGoal returns the goal state for the state sync (B4, B20); the
// snapshot is taken under the lock (Review 344-8).
func (s *Service) StateForGoal(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.goalConfig(ctx, w)
	if cfg == nil {
		return nil
	}
	s.mu.Lock()
	st, err := s.goalLoadLocked(ctx, w.ID)
	var state any
	if err == nil {
		state = s.goalStateFor(cfg, st)
	}
	s.mu.Unlock()
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the goal state failed", "widget", w.ID, "error", err)
		return nil
	}
	return state
}

// ---------- timer (B26) ----------

// timerFormatDefault is the display format of the timer (B17, B26).
const timerFormatDefault = "MM:SS"

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
		s.pushTimer(ctx, w, cfg)
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
	s.pushTimer(ctx, w, cfg)
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
	s.pushTimer(ctx, w, cfg)
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
	s.pushTimer(ctx, w, cfg)
	return nil
}

// pushTimer pushes the state of the timer widget (B26); the runtime
// renders it per second from the server time of hello. The snapshot is
// taken under the lock (Review 344-8); the state of w must exist.
func (s *Service) pushTimer(ctx context.Context, w settings.OverlayWidget, cfg *timerConfig) {
	s.mu.Lock()
	state := timerStateFor(cfg, s.timers[w.ID])
	s.mu.Unlock()
	s.push(ctx, w, state)
}

// timerStateFor is the runtime state of the timer widget; st must not be
// nil.
func timerStateFor(cfg *timerConfig, st *timerState) map[string]any {
	return map[string]any{
		"deadline": st.deadline.UnixMilli(),
		"paused":   st.paused,
		"pausedMs": st.pausedFor.Milliseconds(),
		"format":   cfg.Format,
	}
}

// StateForTimer returns the timer state for the state sync (B4, B26); the
// snapshot is taken under the lock (Review 344-8).
func (s *Service) StateForTimer(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.timerConfig(ctx, w)
	s.mu.Lock()
	st := s.timers[w.ID]
	if st == nil {
		st = &timerState{paused: true, pausedFor: cfg.duration}
	}
	state := timerStateFor(cfg, st)
	s.mu.Unlock()
	return state
}

// timerConfig decodes the configuration of the timer widget; the format
// defaults to MM:SS (B17, B26).
func (s *Service) timerConfig(ctx context.Context, w settings.OverlayWidget) *timerConfig {
	var c timerConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the timer configuration is unreadable", "widget", w.ID, "error", err)
		return &timerConfig{Format: timerFormatDefault}
	}
	if c.Duration != "" {
		d, err := time.ParseDuration(c.Duration)
		if err != nil {
			s.logger.ErrorContext(ctx, "the timer duration is unknown", "widget", w.ID, "duration", c.Duration)
			return &timerConfig{Format: timerFormatDefault}
		}
		c.duration = d
	}
	if c.Format == "" {
		c.Format = timerFormatDefault
	}
	return &c
}

// ---------- event_list (B27) ----------

// listMaxDefault is the default maximum of the event list (B27).
const listMaxDefault = 10

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
		return &eventListConfig{Max: listMaxDefault}
	}
	if c.Max < 1 {
		c.Max = listMaxDefault
	}
	if len(c.Types) > 0 {
		for _, typ := range c.Types {
			if !slices.Contains(FeedTypes, event.Type(typ)) {
				s.logger.ErrorContext(ctx, "the event list type is not a feed type", "widget", w.ID, "type", typ)
				return &eventListConfig{Max: listMaxDefault}
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

// chatMaxDefault is the default maximum of the chat widget (B28).
const chatMaxDefault = 20

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
		return &chatConfig{Max: chatMaxDefault}
	}
	if c.Max < 1 {
		c.Max = chatMaxDefault
	}
	if c.DelayMs < 0 || c.RemoveAfterMs < 0 {
		s.logger.ErrorContext(ctx, "the chat delays must be 0 or more", "widget", w.ID)
		return &chatConfig{Max: chatMaxDefault}
	}
	return &c
}

// ---------- shared ----------

// widget finds the widget in the section; it must be of the kind want.
// ---------- poll (B34) ----------

// SetPollState sets the state of the active vote (B34) and pushes it to
// the visible poll widgets; the twitch action feeds it from the Helix
// status checks. Without a section (start) it is kept for the sync.
func (s *Service) SetPollState(ctx context.Context, st poll.State) error {
	s.mu.Lock()
	s.pollState = st
	s.hasPoll = true
	s.mu.Unlock()
	sec := s.section(ctx)
	if sec == nil {
		return nil
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindPoll || !s.visible(w) {
			continue
		}
		s.push(ctx, w, st)
	}
	return nil
}

// StateForPoll returns the active vote for the state sync (B4); nil
// without one.
func (s *Service) StateForPoll(_ context.Context, _ settings.OverlayWidget) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasPoll {
		return nil
	}
	return s.pollState
}

// ---------- discord_voice (B37) ----------

// refreshDiscordVoice pushes the current occupants of the configured
// voice channel to the visible discord_voice widgets (B37).
func (s *Service) refreshDiscordVoice(ctx context.Context) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	occupants := s.ports.DiscordOccupants(ctx)
	state := map[string]any{"occupants": occupants}
	for _, w := range sec.Widgets {
		if w.Kind != KindDiscordVoice || !s.visible(w) {
			continue
		}
		s.push(ctx, w, state)
	}
}

// StateForDiscordVoice returns the current occupants for the state sync
// (B4, B37).
func (s *Service) StateForDiscordVoice(ctx context.Context, _ settings.OverlayWidget) any {
	return map[string]any{"occupants": s.ports.DiscordOccupants(ctx)}
}

// ---------- leaderboard (B32) ----------

// leaderboardConfig is the configuration of the leaderboard widget.
type leaderboardConfig struct {
	// Stat is the statistic: watch_time, messages or commands.
	Stat string `json:"stat"`
	// Count is the number of entries; zero for the default of ten.
	Count int `json:"count"`
}

// leaderboardStat maps the statistic name of the configuration to the
// statistic of the user rankings (B32).
func leaderboardStat(name string) (user.Stat, bool) {
	switch name {
	case "watch_time":
		return user.StatWatchMinutes, true
	case "messages":
		return user.StatMessages, true
	case "commands":
		return user.StatCommandsRun, true
	default:
		return "", false
	}
}

// refreshLeaderboard re-reads the visible leaderboard widgets on the tick
// and on the events that change the statistics (B32).
func (s *Service) refreshLeaderboard(ctx context.Context) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindLeaderboard || !s.visible(w) {
			continue
		}
		cfg := s.leaderboardConfig(ctx, w)
		if cfg == nil {
			continue
		}
		if st, ok := s.leaderboardState(ctx, cfg); ok {
			s.push(ctx, w, st)
		}
	}
}

func (s *Service) leaderboardConfig(ctx context.Context, w settings.OverlayWidget) *leaderboardConfig {
	var c leaderboardConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the leaderboard configuration is unreadable", "widget", w.ID, "error", err)
		return nil
	}
	if _, ok := leaderboardStat(c.Stat); !ok {
		s.logger.ErrorContext(ctx, "the leaderboard statistic is unknown", "widget", w.ID, "stat", c.Stat)
		return nil
	}
	if c.Count < 0 {
		s.logger.ErrorContext(ctx, "the leaderboard count is negative", "widget", w.ID, "count", c.Count)
		return nil
	}
	return &c
}

// leaderboardState is the runtime state of the leaderboard (B32): the
// ranked entries with the user and the value.
func (s *Service) leaderboardState(ctx context.Context, cfg *leaderboardConfig) (map[string]any, bool) {
	stat, ok := leaderboardStat(cfg.Stat)
	if !ok {
		return nil, false
	}
	n := cfg.Count
	if n <= 0 {
		n = 10
	}
	ranked, err := s.ports.TopUsers(ctx, stat, n)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the leaderboard failed", "error", err)
		return nil, false
	}
	entries := make([]map[string]any, 0, len(ranked))
	for _, r := range ranked {
		entries = append(entries, map[string]any{"name": userName(r.User), "value": r.Value})
	}
	return map[string]any{"stat": cfg.Stat, "entries": entries}, true
}

// StateForLeaderboard returns the leaderboard for the state sync (B4); it
// re-reads the ranking.
func (s *Service) StateForLeaderboard(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.leaderboardConfig(ctx, w)
	if cfg == nil {
		return nil
	}
	st, _ := s.leaderboardState(ctx, cfg)
	return st
}

// userName is the display name of the user; the login if there is none.
func userName(u user.User) string {
	for _, i := range u.Identities {
		if i.DisplayName != "" {
			return i.DisplayName
		}
		if i.Login != "" {
			return i.Login
		}
	}
	return ""
}

// ---------- wheel (B33) ----------

// wheelConfig is the configuration of the wheel widget.
type wheelConfig struct {
	// EqualProbability is true for equal chances for every outcome
	// (default); otherwise the probabilities of the outcomes are used.
	EqualProbability bool `json:"equal_probability"`
	// Outcomes are the outcomes of the wheel, in order; at least one.
	Outcomes []wheelOutcome `json:"outcomes"`
}

// wheelOutcome is one outcome of the wheel.
type wheelOutcome struct {
	// Name is the name of the outcome.
	Name string `json:"name"`
	// Probability is the decimal percent of the outcome, when the chances
	// are not equal; the probabilities must sum to 100.
	Probability float64 `json:"probability"`
	// Modifier is the whole percent points of the outcome; the sum of the
	// modifiers is zero.
	Modifier int `json:"modifier"`
}

// wheelState is the running state of the wheel widget (B33): the current
// probabilities, which the modifiers shift from spin to spin.
type wheelState struct {
	probs []float64
}

func (s *Service) wheelConfig(ctx context.Context, w settings.OverlayWidget) *wheelConfig {
	var c wheelConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the wheel configuration is unreadable", "widget", w.ID, "error", err)
		return nil
	}
	if len(c.Outcomes) < 1 {
		s.logger.ErrorContext(ctx, "the wheel has no outcomes", "widget", w.ID)
		return nil
	}
	for i, o := range c.Outcomes {
		if o.Name == "" {
			s.logger.ErrorContext(ctx, "a wheel outcome needs a name", "widget", w.ID, "outcome", i)
			return nil
		}
	}
	if !c.EqualProbability {
		sum := 0.0
		mod := 0
		for _, o := range c.Outcomes {
			if o.Probability < 0 {
				s.logger.ErrorContext(ctx, "a wheel probability is negative", "widget", w.ID)
				return nil
			}
			sum += o.Probability
			mod += o.Modifier
		}
		if math.Abs(sum-100) > 0.0001 {
			s.logger.ErrorContext(ctx, "the wheel probabilities must sum to 100", "widget", w.ID, "sum", sum)
			return nil
		}
		if mod != 0 {
			s.logger.ErrorContext(ctx, "the wheel modifiers must sum to zero", "widget", w.ID, "sum", mod)
			return nil
		}
	}
	return &c
}

// wheelProbs returns the current probabilities of the wheel, loaded from
// the running state or the configuration.
func (s *Service) wheelProbs(w settings.OverlayWidget, cfg *wheelConfig) []float64 {
	s.mu.Lock()
	st := s.wheels[w.ID]
	s.mu.Unlock()
	if st != nil && len(st.probs) == len(cfg.Outcomes) {
		return st.probs
	}
	return s.wheelInitialProbs(cfg)
}

// wheelInitialProbs are the probabilities before the first spin (B33).
func (s *Service) wheelInitialProbs(cfg *wheelConfig) []float64 {
	n := len(cfg.Outcomes)
	if cfg.EqualProbability {
		p := make([]float64, n)
		for i := range p {
			p[i] = 100.0 / float64(n)
		}
		return p
	}
	p := make([]float64, n)
	for i, o := range cfg.Outcomes {
		p[i] = o.Probability
	}
	return p
}

// SpinWheel runs the spin of the wheel widget (B33): it draws the outcome
// server-side, sends the spin to the runtime, applies the modifier rule,
// and publishes overlay.wheel.spun.
func (s *Service) SpinWheel(ctx context.Context, widgetID string, u *user.User) error {
	w, err := s.widget(ctx, widgetID, KindWheel)
	if err != nil {
		return err
	}
	cfg := s.wheelConfig(ctx, w)
	if cfg == nil {
		return fmt.Errorf("%w: the wheel widget has no valid configuration", ErrUnknownWidget)
	}
	probs := s.wheelProbs(w, cfg)
	idx := weightedPick(probs)
	outcome := cfg.Outcomes[idx]
	// The modifier rule of the spin (B33): a negative modifier of the
	// landed outcome shifts the probabilities by the modifiers, a positive
	// one resets them, zero changes nothing.
	next := probs
	if outcome.Modifier < 0 {
		next = append([]float64(nil), probs...)
		for i := range next {
			next[i] += float64(cfg.Outcomes[i].Modifier)
		}
	} else if outcome.Modifier > 0 {
		next = s.wheelInitialProbs(cfg)
	}
	s.mu.Lock()
	s.wheels[w.ID] = &wheelState{probs: next}
	s.mu.Unlock()
	s.push(ctx, w, map[string]any{
		"spin": map[string]any{
			"index":    idx,
			"name":     outcome.Name,
			"duration": 4.0,
		},
	})
	s.publish(ctx, eventtype.OverlayWheelSpun, eventtype.Payload{
		Platform: platform.Twitch,
		User:     u,
		Details:  eventtype.Details{OverlayWheel: &eventtype.OverlayWheel{Outcome: outcome.Name}},
	})
	return nil
}

// StateForWheel returns the wheel for the state sync (B4): the outcomes
// and the current probabilities.
func (s *Service) StateForWheel(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.wheelConfig(ctx, w)
	if cfg == nil {
		return nil
	}
	probs := s.wheelProbs(w, cfg)
	outcomes := make([]map[string]any, len(cfg.Outcomes))
	for i, o := range cfg.Outcomes {
		outcomes[i] = map[string]any{"name": o.Name, "probability": probs[i]}
	}
	return map[string]any{"outcomes": outcomes}
}

// ---------- persistent_timer (B29) ----------

// persistentTimerConfig is the configuration of the persistent timer.
type persistentTimerConfig struct {
	// Initial is the starting duration as text, e.g. "1h30m".
	Initial string `json:"initial"`
	// Format is the display format (B17); empty for MM:SS.
	Format string `json:"format"`
	// DisableOnCompletion hides the widget when it reaches zero.
	DisableOnCompletion bool `json:"disable_on_completion"`
	// ResetOnEnable resets to the initial duration when the widget is
	// shown again after being hidden.
	ResetOnEnable bool `json:"reset_on_enable"`
	// AllowWhilePaused allows the event amounts while the widget is
	// hidden (paused).
	AllowWhilePaused bool `json:"allow_adjustment_while_paused"`
	// EventAmounts are the seconds an event of the type adds, keyed by the
	// event type, e.g. "channel.follow": 60.
	EventAmounts map[string]int64 `json:"event_amounts"`
	// initial is the parsed initial duration.
	initial time.Duration
	// eventAmounts are the parsed event durations.
	eventAmounts map[string]time.Duration
}

// persistentTimerState is the running state of the persistent timer (B29);
// it is persisted in the profile (B20).
type persistentTimerState struct {
	// deadline is when the timer reaches zero while it runs.
	deadline time.Time
	// remaining is the frozen time while it is paused (hidden).
	remaining time.Duration
	// paused reports whether the timer is frozen.
	paused bool
	// completed reports whether the timer reached zero.
	completed bool
	// wasVisible is the visibility of the last tick, for the change.
	wasVisible bool
}

// persistentTimerDoc is the stored form of persistentTimerState.
type persistentTimerDoc struct {
	DeadlineMs  int64 `json:"deadline_ms"`
	RemainingMs int64 `json:"remaining_ms"`
	Paused      bool  `json:"paused"`
	Completed   bool  `json:"completed"`
	WasVisible  bool  `json:"was_visible"`
}

// persistentTimerLoad returns the running state of the persistent timer,
// loaded from the profile on first use (B20).
func (s *Service) persistentTimerLoad(ctx context.Context, widgetID string) (*persistentTimerState, error) {
	s.mu.Lock()
	if st, ok := s.persistentTimers[widgetID]; ok {
		s.mu.Unlock()
		return st, nil
	}
	s.mu.Unlock()
	doc, found, err := s.ports.State.OverlayWidgetState(ctx, widgetID)
	if err != nil {
		return nil, err
	}
	st := &persistentTimerState{}
	if found {
		var d persistentTimerDoc
		if err := json.Unmarshal(doc, &d); err != nil {
			s.logger.ErrorContext(ctx, "the persistent timer state is unreadable, starting fresh", "widget", widgetID, "error", err)
		} else {
			st.deadline = time.UnixMilli(d.DeadlineMs)
			st.remaining = time.Duration(d.RemainingMs) * time.Millisecond
			st.paused = d.Paused
			st.completed = d.Completed
			st.wasVisible = d.WasVisible
		}
	}
	s.mu.Lock()
	s.persistentTimers[widgetID] = st
	s.mu.Unlock()
	return st, nil
}

// persistentTimerSave stores the state of the persistent timer (B20).
func (s *Service) persistentTimerSave(ctx context.Context, widgetID string, st *persistentTimerState) {
	d := persistentTimerDoc{
		DeadlineMs:  st.deadline.UnixMilli(),
		RemainingMs: st.remaining.Milliseconds(),
		Paused:      st.paused,
		Completed:   st.completed,
		WasVisible:  st.wasVisible,
	}
	doc, err := json.Marshal(d)
	if err != nil {
		s.logger.ErrorContext(ctx, "storing the persistent timer state failed", "widget", widgetID, "error", err)
		return
	}
	if err := s.ports.State.SetOverlayWidgetState(ctx, widgetID, doc); err != nil {
		s.logger.ErrorContext(ctx, "storing the persistent timer state failed", "widget", widgetID, "error", err)
	}
}

// persistentTimerConfig decodes and checks the configuration of the
// persistent timer (B29).
func (s *Service) persistentTimerConfig(ctx context.Context, w settings.OverlayWidget) *persistentTimerConfig {
	var c persistentTimerConfig
	c.eventAmounts = map[string]time.Duration{}
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the persistent timer configuration is unreadable", "widget", w.ID, "error", err)
		return nil
	}
	if c.Initial != "" {
		d, err := time.ParseDuration(c.Initial)
		if err != nil {
			s.logger.ErrorContext(ctx, "the persistent timer initial duration is unknown", "widget", w.ID, "initial", c.Initial)
			return nil
		}
		c.initial = d
	}
	if c.Format == "" {
		c.Format = "MM:SS"
	}
	for typ, sec := range c.EventAmounts {
		c.eventAmounts[typ] = time.Duration(sec) * time.Second
	}
	return &c
}

// persistentTimerProgress applies the event amount of the type to the
// visible persistent timers, or to the hidden ones while
// allow_adjustment_while_paused (B29).
func (s *Service) persistentTimerProgress(ctx context.Context, env event.Envelope) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	key := string(env.Type)
	for _, w := range sec.Widgets {
		if w.Kind != KindPersistentTimer {
			continue
		}
		cfg := s.persistentTimerConfig(ctx, w)
		if cfg == nil {
			continue
		}
		// A hidden timer is frozen; the event amounts reach it while
		// allow_adjustment_while_paused (B29).
		if !s.visible(w) && !cfg.AllowWhilePaused {
			continue
		}
		amount, ok := cfg.eventAmounts[key]
		if !ok || amount == 0 {
			continue
		}
		s.persistentTimerAdjust(ctx, w, cfg, amount, env)
	}
}

// AddToPersistentTimer adds d, which may be negative (B29, B38), to the
// persistent timer and publishes overlay.timer.adjusted.
func (s *Service) AddToPersistentTimer(ctx context.Context, widgetID string, d time.Duration) error {
	w, err := s.widget(ctx, widgetID, KindPersistentTimer)
	if err != nil {
		return err
	}
	cfg := s.persistentTimerConfig(ctx, w)
	if cfg == nil {
		return fmt.Errorf("%w: the persistent timer has no valid configuration", ErrUnknownWidget)
	}
	s.persistentTimerAdjust(ctx, w, cfg, d, event.Envelope{})
	return nil
}

// persistentTimerAdjust changes the remaining time of the persistent timer
// by d and publishes overlay.timer.adjusted (B29).
func (s *Service) persistentTimerAdjust(ctx context.Context, w settings.OverlayWidget, cfg *persistentTimerConfig, d time.Duration, env event.Envelope) {
	st, err := s.persistentTimerLoad(ctx, w.ID)
	if err != nil {
		return
	}
	s.mu.Lock()
	if st.completed {
		s.mu.Unlock()
		return
	}
	if st.paused {
		st.remaining += d
		if st.remaining < 0 {
			st.remaining = 0
		}
	} else {
		st.deadline = st.deadline.Add(d)
	}
	s.mu.Unlock()
	s.persistentTimerSave(ctx, w.ID, st)
	s.pushPersistentTimer(ctx, w, cfg, st)
	user := (*user.User)(nil)
	if p, ok := event.Payload[eventtype.Payload](env); ok {
		user = p.User
	}
	s.publish(ctx, eventtype.OverlayTimerAdjusted, eventtype.Payload{
		Platform: platform.Twitch,
		User:     user,
		Details:  eventtype.Details{OverlayTimer: &eventtype.OverlayTimer{AmountChanged: int64(d.Seconds())}},
	})
}

// persistentTimerTick runs the tick work of the visible persistent timers
// (B29): it freezes the hidden ones, resumes the shown ones, and completes
// the ones that reached zero.
func (s *Service) persistentTimerTick(ctx context.Context) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindPersistentTimer {
			continue
		}
		cfg := s.persistentTimerConfig(ctx, w)
		if cfg == nil {
			continue
		}
		st, err := s.persistentTimerLoad(ctx, w.ID)
		if err != nil {
			continue
		}
		visible := s.visible(w)
		now := time.Now()
		changed := false
		s.mu.Lock()
		switch {
		case !visible && st.wasVisible:
			// The widget was hidden: freeze the remaining time, or reset
			// it to the initial while reset_on_enable (B29).
			if cfg.ResetOnEnable {
				st.remaining = cfg.initial
			} else if !st.paused {
				st.remaining = max(time.Until(st.deadline), 0)
			}
			st.paused = true
			changed = true
		case visible && !st.wasVisible:
			// The widget was shown: resume from the frozen time, or start
			// from the initial while it is new or reset_on_enable (B29).
			if cfg.ResetOnEnable || st.deadline.IsZero() {
				st.deadline = now.Add(cfg.initial)
			} else {
				st.deadline = now.Add(st.remaining)
			}
			st.paused = false
			changed = true
		case visible && !st.paused && !st.completed && st.deadline.Before(now):
			// The running timer reached zero (B29).
			st.completed = true
			st.remaining = 0
			changed = true
		}
		st.wasVisible = visible
		s.mu.Unlock()
		if changed {
			s.persistentTimerSave(ctx, w.ID, st)
		}
		if visible || changed {
			s.pushPersistentTimer(ctx, w, cfg, st)
		}
		if st.completed && changed && st.wasVisible {
			s.publish(ctx, eventtype.OverlayTimerCompleted, eventtype.Payload{Platform: platform.Twitch})
		}
	}
}

// pushPersistentTimer pushes the state of the persistent timer (B22, B29).
func (s *Service) pushPersistentTimer(ctx context.Context, w settings.OverlayWidget, cfg *persistentTimerConfig, st *persistentTimerState) {
	state := map[string]any{
		"deadline":  st.deadline.UnixMilli(),
		"paused":    st.paused,
		"pausedMs":  st.remaining.Milliseconds(),
		"completed": st.completed,
		"format":    cfg.Format,
		"disabled":  st.completed && cfg.DisableOnCompletion,
	}
	s.push(ctx, w, state)
}

// StateForPersistentTimer returns the state of the persistent timer for
// the state sync (B4, B20).
func (s *Service) StateForPersistentTimer(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.persistentTimerConfig(ctx, w)
	if cfg == nil {
		return nil
	}
	st, err := s.persistentTimerLoad(ctx, w.ID)
	if err != nil {
		return nil
	}
	return map[string]any{
		"deadline":  st.deadline.UnixMilli(),
		"paused":    st.paused,
		"pausedMs":  st.remaining.Milliseconds(),
		"completed": st.completed,
		"format":    cfg.Format,
		"disabled":  st.completed && cfg.DisableOnCompletion,
	}
}

// ---------- stream_boss (B30) ----------

// streamBossConfig is the configuration of the stream boss.
type streamBossConfig struct {
	// BaseHealth is the health of a new boss.
	BaseHealth int64 `json:"base_health"`
	// KillBonusHealth is the health added to the next boss after a kill.
	KillBonusHealth int64 `json:"kill_bonus_health"`
	// OverkillBonusMultiplier multiplies the overkill into the next boss's
	// health.
	OverkillBonusMultiplier float64 `json:"overkill_bonus_multiplier"`
	// SelfHealMultiplier multiplies the healing when the boss damages
	// itself.
	SelfHealMultiplier float64 `json:"self_heal_multiplier"`
	// CompoundPreviousHealth compounds the previous max health into the
	// next boss; nil for the default of true.
	CompoundPreviousHealth *bool `json:"compound_previous_health"`
	// EventAmounts are the health an event of the type adds, keyed by the
	// event type, e.g. "channel.follow": 5.
	EventAmounts map[string]int64 `json:"event_amounts"`
}

// streamBossState is the running state of the stream boss (B30); it is in
// memory.
type streamBossState struct {
	// boss is the display name of the current boss.
	boss string
	// health is the current health.
	health int64
	// maxHealth is the max health of the current boss, for the compound.
	maxHealth int64
}

func (s *Service) streamBossConfig(ctx context.Context, w settings.OverlayWidget) *streamBossConfig {
	var c streamBossConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the stream boss configuration is unreadable", "widget", w.ID, "error", err)
		return nil
	}
	if c.BaseHealth < 1 {
		s.logger.ErrorContext(ctx, "the stream boss base health must be positive", "widget", w.ID)
		return nil
	}
	return &c
}

// streamBossState returns the running state of the boss, started with the
// configured base health if there is none yet.
func (s *Service) streamBossState(_ context.Context, w settings.OverlayWidget, cfg *streamBossConfig) *streamBossState {
	s.mu.Lock()
	st := s.bosses[w.ID]
	s.mu.Unlock()
	if st != nil {
		return st
	}
	st = &streamBossState{boss: "", health: cfg.BaseHealth, maxHealth: cfg.BaseHealth}
	s.mu.Lock()
	s.bosses[w.ID] = st
	s.mu.Unlock()
	return st
}

// streamBossStep applies the event amount to the visible bosses (B30).
func (s *Service) streamBossStep(ctx context.Context, env event.Envelope) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	key := string(env.Type)
	for _, w := range sec.Widgets {
		if w.Kind != KindStreamBoss || !s.visible(w) {
			continue
		}
		cfg := s.streamBossConfig(ctx, w)
		if cfg == nil {
			continue
		}
		amount, ok := cfg.EventAmounts[key]
		if !ok || amount == 0 {
			continue
		}
		u := (*user.User)(nil)
		if p, ok := event.Payload[eventtype.Payload](env); ok {
			u = p.User
		}
		s.streamBossApply(ctx, w, cfg, amount, u, false)
	}
}

// DamageStreamBoss applies the damage of the operation to the boss (B30,
// B38): a negative amount heals, the caller who is the boss heals with the
// self-heal multiplier, and force forces the damage.
func (s *Service) DamageStreamBoss(ctx context.Context, widgetID string, amount int64, force bool, u *user.User) error {
	w, err := s.widget(ctx, widgetID, KindStreamBoss)
	if err != nil {
		return err
	}
	cfg := s.streamBossConfig(ctx, w)
	if cfg == nil {
		return fmt.Errorf("%w: the stream boss has no valid configuration", ErrUnknownWidget)
	}
	s.streamBossApply(ctx, w, cfg, amount, u, force)
	return nil
}

// streamBossApply changes the health of the boss by damage, publishes the
// effect, and rolls a new boss at zero or below (B30).
func (s *Service) streamBossApply(ctx context.Context, w settings.OverlayWidget, cfg *streamBossConfig, amount int64, u *user.User, force bool) {
	st := s.streamBossState(ctx, w, cfg)
	s.mu.Lock()
	heal := amount < 0
	// The caller who is the boss heals, not damages, while self-heal is
	// configured and the damage is not forced (B30).
	if !force && !heal && u != nil && !u.ID.IsZero() && st.boss == userName(*u) {
		amount = -int64(float64(amount) * cfg.SelfHealMultiplier)
		heal = true
	}
	overkill := int64(0)
	st.health -= amount // amount is negative for a heal: adds the health
	if st.health <= 0 {
		overkill = -st.health
		st.health = 0
	}
	health := st.health
	changed := -amount
	boss := st.boss
	maxHealth := st.maxHealth
	compounded := cfg.CompoundPreviousHealth == nil || *cfg.CompoundPreviousHealth
	s.mu.Unlock()

	if health <= 0 {
		s.streamBossNew(ctx, w, cfg, boss, maxHealth, overkill, compounded)
		s.pushBoss(ctx, w)
		return
	}
	typ := eventtype.OverlayBossHeal
	if !heal {
		typ = eventtype.OverlayBossDamage
	}
	s.pushBoss(ctx, w)
	s.publish(ctx, typ, eventtype.Payload{
		Platform: platform.Twitch,
		User:     u,
		Details: eventtype.Details{OverlayBoss: &eventtype.OverlayBoss{
			Health: health,
			Amount: changed,
		}},
	})
}

// streamBossNew rolls the new boss after the old one is dead (B30): a
// random active user other than the dead one, with the compounded health.
func (s *Service) streamBossNew(ctx context.Context, w settings.OverlayWidget, cfg *streamBossConfig, dead string, prevMax, overkill int64, compounded bool) {
	candidates := s.streamBossCandidates(ctx, dead)
	next := ""
	if len(candidates) > 0 {
		//nolint:gosec // not a cryptographic source, but the random name of the next boss
		next = candidates[rand.Intn(len(candidates))]
	}
	base := cfg.BaseHealth
	if compounded && prevMax > base {
		base = prevMax
	}
	health := base + cfg.KillBonusHealth + int64(float64(overkill)*cfg.OverkillBonusMultiplier)
	s.mu.Lock()
	st := s.bosses[w.ID]
	if st != nil {
		st.boss = next
		st.health = health
		st.maxHealth = health
	}
	s.mu.Unlock()
	s.publish(ctx, eventtype.OverlayBossNew, eventtype.Payload{
		Platform: platform.Twitch,
		Details:  eventtype.Details{OverlayBoss: &eventtype.OverlayBoss{Health: health}},
	})
}

// streamBossCandidates are the active users for the next boss (B30): the
// recent feed events with a user, other than the dead one.
func (s *Service) streamBossCandidates(ctx context.Context, dead string) []string {
	events, err := s.ports.Feed.LatestFeed(ctx, 50)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the boss candidates failed", "error", err)
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range events {
		if e.DisplayName == "" || e.DisplayName == dead || seen[e.DisplayName] {
			continue
		}
		seen[e.DisplayName] = true
		out = append(out, e.DisplayName)
	}
	return out
}

// pushBoss pushes the state of the stream boss (B22).
func (s *Service) pushBoss(ctx context.Context, w settings.OverlayWidget) {
	s.mu.Lock()
	st := s.bosses[w.ID]
	var boss string
	var health, maxHealth int64
	if st != nil {
		boss, health, maxHealth = st.boss, st.health, st.maxHealth
	}
	s.mu.Unlock()
	s.push(ctx, w, map[string]any{"boss": boss, "health": health, "max_health": maxHealth})
}

// StateForStreamBoss returns the state of the stream boss for the state
// sync (B4); nil without a running boss.
func (s *Service) StateForStreamBoss(_ context.Context, w settings.OverlayWidget) any {
	s.mu.Lock()
	st := s.bosses[w.ID]
	s.mu.Unlock()
	if st == nil {
		return nil
	}
	return map[string]any{"boss": st.boss, "health": st.health, "max_health": st.maxHealth}
}

// ---------- game_queue (B36) ----------

// gameQueueStateKey is the state key of the profile-level game queue (B36).
const gameQueueStateKey = "game_queue"

// gameQueueConfig is the configuration of the game queue.
type gameQueueConfig struct {
	// MaxSize is the max size of the queue; 0 for the default of 10.
	MaxSize int `json:"max_size"`
}

// gameEntry is one entry of the game queue.
type gameEntry struct {
	// User is the display name of the user.
	User string `json:"user"`
	// Position is the 1-based position for the runtime; the server fills
	// it, it is not persisted.
	Position int `json:"position"`
}

// gameQueueDoc is the stored form of the game queue; the position is
// derived by the server and not persisted.
type gameQueueDoc struct {
	Entries []gameEntryDoc `json:"entries"`
}

// gameEntryDoc is the stored form of gameEntry.
type gameEntryDoc struct {
	// User is the display name of the user.
	User string `json:"user"`
}

// gameQueueMaxSize is the max size from the first game queue widget, or the
// default of ten (B36).
func (s *Service) gameQueueMaxSize(ctx context.Context) int {
	sec := s.section(ctx)
	if sec != nil {
		for _, w := range sec.Widgets {
			if w.Kind != KindGameQueue {
				continue
			}
			var c gameQueueConfig
			if err := decodeConfig(w.Config, &c); err == nil && c.MaxSize > 0 {
				return c.MaxSize
			}
		}
	}
	return 10
}

// gameQueue returns the queue of the profile, loaded from the state on
// first use (B20, B36).
func (s *Service) gameQueueEntries(ctx context.Context) []gameEntry {
	s.mu.Lock()
	if s.gameQueueLoaded {
		out := append([]gameEntry(nil), s.gameQueue...)
		s.mu.Unlock()
		return out
	}
	s.mu.Unlock()
	doc, found, err := s.ports.State.OverlayWidgetState(ctx, gameQueueStateKey)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the game queue failed", "error", err)
	} else if found {
		var d gameQueueDoc
		if err := json.Unmarshal(doc, &d); err == nil {
			entries := make([]gameEntry, 0, len(d.Entries))
			for _, e := range d.Entries {
				entries = append(entries, gameEntry{User: e.User})
			}
			s.mu.Lock()
			s.gameQueue = entries
			s.gameQueueLoaded = true
			out := append([]gameEntry(nil), entries...)
			s.mu.Unlock()
			return out
		}
	}
	s.mu.Lock()
	s.gameQueueLoaded = true
	s.mu.Unlock()
	return nil
}

// gameQueueSave stores the queue of the profile (B20, B36).
func (s *Service) gameQueueSave(ctx context.Context, entries []gameEntry) {
	s.mu.Lock()
	s.gameQueue = append([]gameEntry(nil), entries...)
	s.gameQueueLoaded = true
	s.mu.Unlock()
	docs := make([]gameEntryDoc, 0, len(entries))
	for _, e := range entries {
		docs = append(docs, gameEntryDoc{User: e.User})
	}
	doc, err := json.Marshal(gameQueueDoc{Entries: docs})
	if err != nil {
		s.logger.ErrorContext(ctx, "storing the game queue failed", "error", err)
		return
	}
	if err := s.ports.State.SetOverlayWidgetState(ctx, gameQueueStateKey, doc); err != nil {
		s.logger.ErrorContext(ctx, "storing the game queue failed", "error", err)
	}
}

// JoinGameQueue adds the user to the queue and publishes
// gamequeue.joined (B36); it is a clear error if the user is already in or
// the queue is full.
func (s *Service) JoinGameQueue(ctx context.Context, u *user.User) error {
	name := userName(*u)
	entries := s.gameQueueEntries(ctx)
	for _, e := range entries {
		if e.User == name {
			return fmt.Errorf("the user %q is already in the queue", name)
		}
	}
	if len(entries) >= s.gameQueueMaxSize(ctx) {
		return errors.New("the game queue is full")
	}
	entries = append(entries, gameEntry{User: name})
	s.gameQueueSave(ctx, entries)
	s.pushGameQueue(ctx)
	s.publish(ctx, eventtype.GameQueueJoined, eventtype.Payload{
		Platform: platform.Twitch,
		User:     u,
		Details:  eventtype.Details{GameQueue: &eventtype.GameQueue{Position: len(entries)}},
	})
	return nil
}

// LeaveGameQueue removes the user from the queue and publishes
// gamequeue.left (B36); it is a clear error without the user.
func (s *Service) LeaveGameQueue(ctx context.Context, u *user.User) error {
	name := userName(*u)
	entries := s.gameQueueEntries(ctx)
	position := 0
	next := entries[:0]
	for _, e := range entries {
		if e.User == name {
			position = len(next) + 1
			continue
		}
		next = append(next, e)
	}
	if position == 0 {
		return fmt.Errorf("the user %q is not in the queue", name)
	}
	s.gameQueueSave(ctx, next)
	s.pushGameQueue(ctx)
	s.publish(ctx, eventtype.GameQueueLeft, eventtype.Payload{
		Platform: platform.Twitch,
		User:     u,
		Details:  eventtype.Details{GameQueue: &eventtype.GameQueue{Position: position}},
	})
	return nil
}

// pushGameQueue pushes the queue to the visible game queue widgets (B22).
func (s *Service) pushGameQueue(ctx context.Context) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	entries := s.gameQueueEntries(ctx)
	for i := range entries {
		entries[i].Position = i + 1 // 1-based for the runtime
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindGameQueue || !s.visible(w) {
			continue
		}
		s.push(ctx, w, map[string]any{"entries": entries})
	}
}

// StateForGameQueue returns the queue for the state sync (B4, B36).
func (s *Service) StateForGameQueue(ctx context.Context, _ settings.OverlayWidget) any {
	entries := s.gameQueueEntries(ctx)
	for i := range entries {
		entries[i].Position = i + 1
	}
	return map[string]any{"entries": entries}
}

// ---------- end_credits (B31) ----------

// endCreditsConfig is the configuration of the end credits.
type endCreditsConfig struct {
	// CustomSections are the names of the custom sections.
	CustomSections []string `json:"custom_sections"`
}

// endCreditsEntry is one entry of a section.
type endCreditsEntry struct {
	// User is the display name of the user; empty for a custom entry.
	User string `json:"user,omitempty"`
	// Text is the text of a custom entry; empty for a user entry.
	Text string `json:"text,omitempty"`
}

// endCreditsState is the data of the end credits, collected per stream
// session while the widget is visible (B31).
type endCreditsState struct {
	sections map[string][]endCreditsEntry
}

// endCreditsSections are the built-in sections, in order (B31).
//
//nolint:gochecknoglobals // static table of sections
var endCreditsSections = []string{
	"chatters", "new_followers", "new_subscribers", "resubscribers",
	"gifted_subscriptions", "all_subscriptions", "raids", "twitch_bits",
}

// endCreditsNew returns a new state with the built-in and the custom
// sections.
func endCreditsNew(cfg *endCreditsConfig) *endCreditsState {
	st := &endCreditsState{sections: map[string][]endCreditsEntry{}}
	for _, name := range endCreditsSections {
		st.sections[name] = nil
	}
	for _, name := range cfg.CustomSections {
		if _, ok := st.sections[name]; !ok {
			st.sections[name] = nil
		}
	}
	return st
}

// endCreditsLoad returns the collected state of the widget, started fresh
// if there is none (B31).
func (s *Service) endCreditsLoad(_ context.Context, widgetID string, cfg *endCreditsConfig) *endCreditsState {
	s.mu.Lock()
	st := s.credits[widgetID]
	s.mu.Unlock()
	if st != nil {
		return st
	}
	st = endCreditsNew(cfg)
	s.mu.Lock()
	s.credits[widgetID] = st
	s.mu.Unlock()
	return st
}

// endCreditsCollect collects the event into the visible end credits (B31).
func (s *Service) endCreditsCollect(ctx context.Context, env event.Envelope) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	payload, ok := event.Payload[eventtype.Payload](env)
	if !ok {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindEndCredits || !s.visible(w) {
			continue
		}
		cfg := s.endCreditsConfig(ctx, w)
		if cfg == nil {
			continue
		}
		st := s.endCreditsLoad(ctx, w.ID, cfg)
		s.endCreditsAdd(ctx, env.Type, st, payload)
		s.pushCredits(ctx, w, st)
	}
}

// endCreditsAdd adds the user of the event to the section it belongs to
// (B31); a ban removes the user from every section.
func (s *Service) endCreditsAdd(_ context.Context, typ event.Type, st *endCreditsState, p eventtype.Payload) {
	switch typ {
	case eventtype.ChatMessage:
		s.endCreditsUser(st, "chatters", p.User)
	case eventtype.ChannelFollow:
		s.endCreditsUser(st, "new_followers", p.User)
	case eventtype.ChannelSubscribe:
		s.endCreditsUser(st, "new_subscribers", p.User)
		s.endCreditsUser(st, "all_subscriptions", p.User)
	case eventtype.ChannelResubscribe:
		s.endCreditsUser(st, "resubscribers", p.User)
		s.endCreditsUser(st, "all_subscriptions", p.User)
	case eventtype.ChannelSubscriptionGift:
		s.endCreditsUser(st, "gifted_subscriptions", p.User)
		s.endCreditsUser(st, "all_subscriptions", p.Target)
	case eventtype.ChannelSubscriptionMassGift:
		s.endCreditsUser(st, "all_subscriptions", p.User)
	case eventtype.ChannelRaid:
		s.endCreditsUser(st, "raids", p.User)
	case eventtype.TwitchBitsCheer:
		s.endCreditsUser(st, "twitch_bits", p.User)
	case eventtype.ChatUserBan, eventtype.ChatUserTimeout:
		s.endCreditsRemove(st, p.User)
	default:
	}
}

// endCreditsUser adds the user to the section, once (B31).
func (s *Service) endCreditsUser(st *endCreditsState, section string, u *user.User) {
	if u == nil || u.ID.IsZero() {
		return
	}
	name := userName(*u)
	s.mu.Lock()
	entries := st.sections[section]
	for _, e := range entries {
		if e.User == name {
			s.mu.Unlock()
			return
		}
	}
	st.sections[section] = append(entries, endCreditsEntry{User: name})
	s.mu.Unlock()
}

// endCreditsRemove removes the user from every section (B31).
func (s *Service) endCreditsRemove(st *endCreditsState, u *user.User) {
	if u == nil || u.ID.IsZero() {
		return
	}
	name := userName(*u)
	s.mu.Lock()
	for section, entries := range st.sections {
		next := entries[:0]
		for _, e := range entries {
			if e.User != name {
				next = append(next, e)
			}
		}
		st.sections[section] = next
	}
	s.mu.Unlock()
}

// endCreditsClear clears the collected data of the visible end credits on
// a new stream session (B31).
func (s *Service) endCreditsClear(ctx context.Context) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindEndCredits {
			continue
		}
		cfg := s.endCreditsConfig(ctx, w)
		if cfg == nil {
			continue
		}
		st := s.endCreditsLoad(ctx, w.ID, cfg)
		s.mu.Lock()
		st.sections = map[string][]endCreditsEntry{}
		for _, name := range append(endCreditsSections, cfg.CustomSections...) {
			st.sections[name] = nil
		}
		s.mu.Unlock()
		if s.visible(w) {
			s.pushCredits(ctx, w, st)
		}
	}
}

// endCreditsConfig decodes the configuration of the end credits.
func (s *Service) endCreditsConfig(ctx context.Context, w settings.OverlayWidget) *endCreditsConfig {
	var c endCreditsConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the end credits configuration is unreadable", "widget", w.ID, "error", err)
		return nil
	}
	return &c
}

// AddToCredits adds a custom entry of the section to the end credits
// (B31, B38).
func (s *Service) AddToCredits(ctx context.Context, widgetID, section, text string) error {
	w, err := s.widget(ctx, widgetID, KindEndCredits)
	if err != nil {
		return err
	}
	cfg := s.endCreditsConfig(ctx, w)
	if cfg == nil {
		return fmt.Errorf("%w: the end credits have no valid configuration", ErrUnknownWidget)
	}
	if !slices.Contains(append(endCreditsSections, cfg.CustomSections...), section) {
		return fmt.Errorf("%w: the section %q is unknown", ErrUnknownWidget, section)
	}
	st := s.endCreditsLoad(ctx, w.ID, cfg)
	s.mu.Lock()
	st.sections[section] = append(st.sections[section], endCreditsEntry{Text: text})
	s.mu.Unlock()
	s.pushCredits(ctx, w, st)
	return nil
}

// PlayCredits starts the sequence of the end credits (B31, B38): the
// sections go to the runtime, which rolls them.
func (s *Service) PlayCredits(ctx context.Context, widgetID string) error {
	w, err := s.widget(ctx, widgetID, KindEndCredits)
	if err != nil {
		return err
	}
	cfg := s.endCreditsConfig(ctx, w)
	if cfg == nil {
		return fmt.Errorf("%w: the end credits have no valid configuration", ErrUnknownWidget)
	}
	st := s.endCreditsLoad(ctx, w.ID, cfg)
	s.push(ctx, w, s.creditsState(cfg, s.creditsSnapshot(st), true))
	return nil
}

// pushCredits pushes the collected data of the end credits (B22).
func (s *Service) pushCredits(ctx context.Context, w settings.OverlayWidget, st *endCreditsState) {
	cfg := s.endCreditsConfig(ctx, w)
	if cfg == nil {
		return
	}
	s.push(ctx, w, s.creditsState(cfg, s.creditsSnapshot(st), false))
}

// creditsSnapshot copies the collected sections under the lock (Review
// 347-1): the bus goroutine and the actions write the sections in
// parallel, and a concurrent map write would end the process.
func (s *Service) creditsSnapshot(st *endCreditsState) map[string][]endCreditsEntry {
	s.mu.Lock()
	sections := make(map[string][]endCreditsEntry, len(st.sections))
	for name, entries := range st.sections {
		sections[name] = append([]endCreditsEntry(nil), entries...)
	}
	s.mu.Unlock()
	return sections
}

// creditsState is the runtime state of the end credits: the sections in
// order; playing is the roll flag (B31).
func (s *Service) creditsState(cfg *endCreditsConfig, sections map[string][]endCreditsEntry, playing bool) map[string]any {
	names := append(append([]string{}, endCreditsSections...), cfg.CustomSections...)
	out := map[string]any{}
	for _, name := range names {
		out[name] = sections[name]
	}
	return map[string]any{"sections": out, "playing": playing}
}

// StateForEndCredits returns the collected data for the state sync (B4).
func (s *Service) StateForEndCredits(ctx context.Context, w settings.OverlayWidget) any {
	cfg := s.endCreditsConfig(ctx, w)
	if cfg == nil {
		return nil
	}
	s.mu.Lock()
	st := s.credits[w.ID]
	s.mu.Unlock()
	if st == nil {
		st = endCreditsNew(cfg)
	}
	return s.creditsState(cfg, s.creditsSnapshot(st), false)
}

// ---------- persistent_emote_effect (B35) ----------

// emoteEffectConfig is the configuration of the persistent emote effect.
type emoteEffectConfig struct {
	// Effect is the effect: rain, burst or random.
	Effect string `json:"effect"`
	// Duration is the duration of the effect as text, e.g. "5s".
	Duration string `json:"duration"`
	// PerEmoteShown is the count shown per occurring emote.
	PerEmoteShown int `json:"per_emote_shown"`
	// MaxShown is the overall cap of shown emotes.
	MaxShown int `json:"max_shown"`
	// ComboCount is the count of the same emote that triggers the effect.
	ComboCount int `json:"combo_count"`
	// ComboTimeframe is the window, in seconds, of the combo.
	ComboTimeframe int `json:"combo_timeframe"`
	// IgnoreDuplicates counts each emote once per message.
	IgnoreDuplicates bool `json:"ignore_duplicates"`
	duration         time.Duration
	timeframe        time.Duration
}

// emoteEffectState is the combo state of one emote effect widget (B35).
type emoteEffectState struct {
	// occurrences are the recent times per emote code.
	occurrences map[string][]time.Time
}

func (s *Service) emoteEffectConfig(ctx context.Context, w settings.OverlayWidget) *emoteEffectConfig {
	var c emoteEffectConfig
	if err := decodeConfig(w.Config, &c); err != nil {
		s.logger.ErrorContext(ctx, "the emote effect configuration is unreadable", "widget", w.ID, "error", err)
		return nil
	}
	if c.Effect != "rain" && c.Effect != "burst" && c.Effect != "random" {
		s.logger.ErrorContext(ctx, "the emote effect is unknown", "widget", w.ID, "effect", c.Effect)
		return nil
	}
	if c.Duration != "" {
		d, err := time.ParseDuration(c.Duration)
		if err != nil {
			s.logger.ErrorContext(ctx, "the emote effect duration is unknown", "widget", w.ID, "duration", c.Duration)
			return nil
		}
		c.duration = d
	}
	if c.ComboCount < 1 {
		c.ComboCount = 3
	}
	if c.ComboTimeframe < 1 {
		c.ComboTimeframe = 10
	}
	c.timeframe = time.Duration(c.ComboTimeframe) * time.Second
	return &c
}

// emoteEffectMessage runs the persistent emote effect on a chat message
// (B35): it counts the combos of the emotes and triggers the effect once a
// combo is reached.
func (s *Service) emoteEffectMessage(ctx context.Context, env event.Envelope) {
	sec := s.section(ctx)
	if sec == nil {
		return
	}
	payload, ok := event.Payload[eventtype.Payload](env)
	if !ok || payload.Details.Message == nil {
		return
	}
	codes := payload.Details.Message.Emotes
	if len(codes) == 0 {
		return
	}
	for _, w := range sec.Widgets {
		if w.Kind != KindEmoteEffect || !s.visible(w) {
			continue
		}
		cfg := s.emoteEffectConfig(ctx, w)
		if cfg == nil {
			continue
		}
		s.emoteEffectCombo(ctx, w, cfg, codes)
	}
}

// emoteEffectCombo counts the emotes of the message and triggers the
// effect for the reached combos (B35).
func (s *Service) emoteEffectCombo(ctx context.Context, w settings.OverlayWidget, cfg *emoteEffectConfig, codes []string) {
	if cfg.IgnoreDuplicates {
		seen := map[string]bool{}
		next := codes[:0]
		for _, c := range codes {
			if !seen[c] {
				seen[c] = true
				next = append(next, c)
			}
		}
		codes = next
	}
	st := s.emoteEffectState(w.ID)
	now := time.Now()
	cutoff := now.Add(-cfg.timeframe)
	for _, code := range codes {
		var keep []time.Time
		for _, t := range st.occurrences[code] {
			if t.After(cutoff) {
				keep = append(keep, t)
			}
		}
		keep = append(keep, now)
		st.occurrences[code] = keep
		if len(keep) >= cfg.ComboCount {
			s.emoteEffectTrigger(ctx, w, cfg, code)
			st.occurrences[code] = nil
		}
	}
}

// emoteEffectState returns the combo state of the widget, started fresh if
// there is none.
func (s *Service) emoteEffectState(widgetID string) *emoteEffectState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.emoteCombos[widgetID]
	if st == nil {
		st = &emoteEffectState{occurrences: map[string][]time.Time{}}
		s.emoteCombos[widgetID] = st
	}
	return st
}

// emoteEffectTrigger resolves the emote to its image and pushes the effect
// to the runtime (B35); a name without a known emote is dropped.
func (s *Service) emoteEffectTrigger(ctx context.Context, w settings.OverlayWidget, cfg *emoteEffectConfig, code string) {
	images := s.emoteImages(ctx)
	image, ok := images[code]
	if !ok {
		s.logger.DebugContext(ctx, "the emote of the effect is unknown", "widget", w.ID, "code", code)
		return
	}
	count := max(cfg.PerEmoteShown, 1)
	if cfg.MaxShown > 0 && count > cfg.MaxShown {
		count = cfg.MaxShown
	}
	s.push(ctx, w, map[string]any{
		"effect":          cfg.Effect,
		"duration_ms":     cfg.duration.Milliseconds(),
		"code":            code,
		"image":           image,
		"per_emote_shown": count,
	})
}

// emoteImages resolves the emote codes of the channel to their image URLs
// (B35).
func (s *Service) emoteImages(ctx context.Context) map[string]string {
	images := map[string]string{}
	for _, e := range s.ports.Emotes(ctx) {
		images[e.Code] = e.Image
	}
	return images
}

// StateForEmoteEffect returns nil: the emote effect has no persistent
// state to sync, the effect is a one-shot push (B4, B35).
func (s *Service) StateForEmoteEffect(_ context.Context, _ settings.OverlayWidget) any { return nil }

// weightedPick draws an index with the weights of probs; a non-positive
// weight never wins, and a zero total falls back to a uniform draw.
func weightedPick(probs []float64) int {
	total := 0.0
	for _, p := range probs {
		if p > 0 {
			total += p
		}
	}
	if total <= 0 {
		//nolint:gosec // not a cryptographic source, but the spin draw of the wheel
		return rand.Intn(len(probs))
	}
	//nolint:gosec // not a cryptographic source, but the spin draw of the wheel
	r := rand.Float64() * total
	acc := 0.0
	for i, p := range probs {
		if p <= 0 {
			continue
		}
		acc += p
		if r <= acc {
			return i
		}
	}
	return len(probs) - 1
}

// widget returns the widget with the ID and the kind; an unknown ID or a
// kind mismatch is a clear error for the action (B38).
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
	case KindPoll:
		return s.StateForPoll(ctx, w)
	case KindLeaderboard:
		return s.StateForLeaderboard(ctx, w)
	case KindWheel:
		return s.StateForWheel(ctx, w)
	case KindPersistentTimer:
		return s.StateForPersistentTimer(ctx, w)
	case KindStreamBoss:
		return s.StateForStreamBoss(ctx, w)
	case KindGameQueue:
		return s.StateForGameQueue(ctx, w)
	case KindEndCredits:
		return s.StateForEndCredits(ctx, w)
	case KindEmoteEffect:
		return s.StateForEmoteEffect(ctx, w)
	case KindDiscordVoice:
		return s.StateForDiscordVoice(ctx, w)
	default:
		return nil
	}
}
