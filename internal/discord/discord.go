// SPDX-License-Identifier: MIT

// Package discord runs the Discord gateway connection of the
// discord_voice widget (roadmap 7.5, spec overlays.md, B37, A3): with a
// token it connects to the gateway, tracks the occupants of the
// configured voice channel, and publishes the discord.voice events.
// Without a token or a channel it stays inactive.
//
// Restriction (A3): without a voice connection it does not detect who is
// speaking; it sees the channel membership only.
package discord

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/event"
)

// Reconnect is the interval the service reconnects with after a dropped
// connection.
const Reconnect = 5 * time.Second

// Occupant is one of the current occupants of the voice channel (B37).
type Occupant struct {
	// Name is the Discord name of the occupant, as Discord names it.
	Name string `json:"name"`
	// Channel is the ID of the voice channel.
	Channel string `json:"channel"`
}

// Keys are the credentials of the gateway connection (ADR-0019 §9):
// the bot token and the voice channel to track; the watcher swaps them
// when the configuration changes (Code-ADR-0021, Nachtrag 2026-10-29),
// and the service reconnects with the new keys.
type Keys struct {
	Token   string
	Channel string
}

// Ports are the dependencies of the service.
type Ports struct {
	// Bus is where the service publishes its events (B37).
	Bus *event.Bus
	// Logger records the connection and the occupants.
	Logger *slog.Logger
}

// Service is the Discord gateway connection of the core (B37, A3).
type Service struct {
	ports Ports

	mu sync.Mutex
	// keys are the credentials the service runs with (ADR-0019 §9).
	keys Keys
	// occupants are the current occupants of the configured channel by
	// Discord user ID.
	occupants map[string]Occupant
	// names are the known Discord names by user ID, kept across
	// (re)connections, so that a voice state without a member still gets
	// a name.
	names map[string]string
	// sessionCancel cancels the active gateway connection, so that a
	// changed key reconnects with the new keys (Code-ADR-0021, Nachtrag
	// 2026-10-29); nil without an active connection.
	sessionCancel context.CancelFunc
	// rekeyC wakes the run loop from the inactive wait; one pending
	// wake is enough (buffered, non-blocking send).
	rekeyC chan struct{}

	reconnect time.Duration
}

// New returns the service with its ports and without credentials, so it
// starts inactive; the app runs it with the supervisor (Code-ADR-0004)
// and sets the keys from the configuration.
func New(p Ports) *Service {
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{
		ports:     p,
		occupants: map[string]Occupant{},
		names:     map[string]string{},
		rekeyC:    make(chan struct{}, 1),
		reconnect: Reconnect,
	}
}

// SetKeys swaps the credentials of the service (Code-ADR-0021, Nachtrag
// 2026-10-29): the active connection drops and reconnects with the new
// keys, and an empty key deactivates the service and clears the
// occupants.
func (s *Service) SetKeys(k Keys) {
	s.mu.Lock()
	changed := s.keys != k
	s.keys = k
	cancel := s.sessionCancel
	if k.Token == "" || k.Channel == "" {
		clear(s.occupants)
	}
	s.mu.Unlock()
	if !changed {
		return
	}
	if cancel != nil {
		cancel()
	}
	select {
	case s.rekeyC <- struct{}{}:
	default: // a wake is already pending
	}
}

// Keys returns the credentials the service runs with.
func (s *Service) Keys() Keys {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys
}

// Active reports whether the service has a token and a channel (B37).
func (s *Service) Active() bool {
	k := s.Keys()
	return k.Token != "" && k.Channel != ""
}

// Occupants returns the current occupants of the configured channel
// (B37), sorted by name, so that the widget list is stable between
// pushes.
func (s *Service) Occupants() []Occupant {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Occupant, 0, len(s.occupants))
	for _, o := range s.occupants {
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b Occupant) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// Run connects to the gateway and runs it until ctx ends (B37). Without
// a token or a channel the service stays inactive (info log); a key
// change applies without a restart (Code-ADR-0021, Nachtrag 2026-10-29)
// and reconnects at once.
func (s *Service) Run(ctx context.Context) error {
	inactiveLogged := false
	for {
		if !s.Active() {
			if !inactiveLogged {
				s.ports.Logger.InfoContext(ctx, "the discord token or the voice channel is not set, the discord_voice service is inactive")
				inactiveLogged = true
			}
			// A wake means the keys may have changed; re-check at once.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-s.rekeyC:
			}
			continue
		}
		inactiveLogged = false
		rekeyed, err := s.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if rekeyed {
			continue
		}
		s.ports.Logger.WarnContext(ctx, "the discord gateway connection dropped", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.reconnect):
		}
	}
}

// connect opens the gateway connection with the current keys and runs
// it until it drops, the core context ends, or the keys change; it
// reports whether a key change ended the connection.
func (s *Service) connect(ctx context.Context) (rekeyed bool, err error) {
	k := s.Keys()
	session, err := discordgo.New(k.Token)
	if err != nil {
		return false, err
	}
	// Plain JSON packets; the service needs only the voice states, so
	// the intents stay minimal.
	session.Identify.Compress = false
	session.Identify.Intents = discordgo.IntentGuilds | discordgo.IntentGuildVoiceStates
	// The session context ends with the connection, so that a key
	// change cancels it; the publishes of the handlers run on it.
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.sessionCancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.sessionCancel = nil
		s.mu.Unlock()
	}()
	session.AddHandler(s.onReady)
	session.AddHandler(s.onGuildCreate)
	session.AddHandler(s.onVoiceState(sctx))
	if err := session.Open(); err != nil {
		return false, err
	}
	defer session.Close()
	<-sctx.Done()
	// The session context ends with the core context; a live core
	// context means the keys changed.
	return ctx.Err() == nil, nil
}

// onReady resets the occupants at every (re)connection; the GUILD_CREATE
// events that follow reseed them without events.
func (s *Service) onReady(*discordgo.Session, *discordgo.Ready) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.occupants = map[string]Occupant{}
}

// onGuildCreate reseeds the occupants of the configured channel from the
// initial voice states, without publishing events (B37).
func (s *Service) onGuildCreate(_ *discordgo.Session, g *discordgo.GuildCreate) {
	for _, vs := range g.VoiceStates {
		s.seed(vs)
	}
}

// onVoiceState returns the handler that applies one voice state update
// of the gateway (B37): a join, a leave, or a move into or out of the
// configured channel. The events are published on the context of the
// connection the handler belongs to.
func (s *Service) onVoiceState(ctx context.Context) func(_ *discordgo.Session, evt *discordgo.VoiceStateUpdate) {
	return func(_ *discordgo.Session, evt *discordgo.VoiceStateUpdate) {
		if evt.UserID == "" {
			return
		}
		// The membership before the update; the seed below may add the
		// occupant, so the join is the difference.
		s.mu.Lock()
		_, was := s.occupants[evt.UserID]
		channel := s.keys.Channel
		s.mu.Unlock()
		s.seed(evt.VoiceState)
		now := evt.ChannelID == channel
		s.mu.Lock()
		var name string
		switch {
		case now && !was:
			name = s.nameLocked(evt.UserID)
			s.occupants[evt.UserID] = Occupant{Name: name, Channel: channel}
		case !now && was:
			name = s.nameLocked(evt.UserID)
			delete(s.occupants, evt.UserID)
		}
		s.mu.Unlock()
		switch {
		case now && !was:
			s.publish(ctx, eventtype.DiscordVoiceJoined, name, channel)
		case !now && was:
			s.publish(ctx, eventtype.DiscordVoiceLeft, name, channel)
		}
	}
}

// seed remembers the name of the user of the voice state and, while the
// state is in the configured channel, the occupant.
func (s *Service) seed(vs *discordgo.VoiceState) {
	if vs == nil || vs.UserID == "" {
		return
	}
	s.mu.Lock()
	if name := memberName(vs.Member); name != "" {
		s.names[vs.UserID] = name
	}
	if vs.ChannelID == s.keys.Channel {
		s.occupants[vs.UserID] = Occupant{Name: s.nameLocked(vs.UserID), Channel: s.keys.Channel}
	}
	s.mu.Unlock()
}

// nameLocked is the known name of the user ID; the ID while Discord has
// not named it yet. The caller holds the lock.
func (s *Service) nameLocked(userID string) string {
	if n, ok := s.names[userID]; ok && n != "" {
		return n
	}
	return userID
}

// publish runs typ as a bus event with the occupant as details (B37) on
// the context of the connection it came from; it fails only if the event
// does not fit the catalog, which the shape of the type already
// guarantees.
func (s *Service) publish(ctx context.Context, typ event.Type, name, channel string) {
	env := event.New(event.Source{Kind: event.SourceIntegration, Name: "discord"}, typ, eventtype.Payload{
		Details: eventtype.Details{DiscordVoice: &eventtype.DiscordVoice{Name: name, Channel: channel}},
	})
	if err := s.ports.Bus.Publish(ctx, env); err != nil {
		s.ports.Logger.ErrorContext(ctx, "publishing the discord voice event failed", "type", typ, "error", err)
	}
}

// memberName is the display name of the member; empty without one.
func memberName(m *discordgo.Member) string {
	if m == nil {
		return ""
	}
	if m.Nick != "" {
		return m.Nick
	}
	if m.User != nil && m.User.Username != "" {
		return m.User.Username
	}
	return ""
}
