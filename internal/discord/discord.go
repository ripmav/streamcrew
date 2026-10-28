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
	"context"
	"log/slog"
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

// Ports are the dependencies of the service.
type Ports struct {
	// Token is the bot token from the environment
	// STREAMCREW_DISCORD_TOKEN (ADR-0019 §9); without it the service is
	// inactive.
	Token string
	// Channel is the ID of the voice channel to track
	// (discord-voice-channel); without it the service is inactive.
	Channel string
	// Bus is where the service publishes its events (B37).
	Bus *event.Bus
	// Logger records the connection and the occupants.
	Logger *slog.Logger
}

// Service is the Discord gateway connection of the core (B37, A3).
type Service struct {
	ports Ports

	mu sync.Mutex
	// occupants are the current occupants of the configured channel by
	// Discord user ID.
	occupants map[string]Occupant
	// names are the known Discord names by user ID, kept across
	// (re)connections, so that a voice state without a member still gets
	// a name.
	names map[string]string

	// publishCtx is the context of the active connection for the
	// publishes of the gateway handlers.
	publishCtx context.Context

	reconnect time.Duration
}

// New returns the service with its ports; the app runs it with the
// supervisor (Code-ADR-0004).
func New(p Ports) *Service {
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{
		ports:     p,
		occupants: map[string]Occupant{},
		names:     map[string]string{},
		reconnect: Reconnect,
	}
}

// Active reports whether the service has a token and a channel (B37).
func (s *Service) Active() bool {
	return s.ports.Token != "" && s.ports.Channel != ""
}

// Occupants returns the current occupants of the configured channel
// (B37).
func (s *Service) Occupants() []Occupant {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Occupant, 0, len(s.occupants))
	for _, o := range s.occupants {
		out = append(out, o)
	}
	return out
}

// Run connects to the gateway and runs it until ctx ends (B37). Without
// a token or a channel the service is inactive (info log).
func (s *Service) Run(ctx context.Context) error {
	if !s.Active() {
		s.ports.Logger.InfoContext(ctx, "the discord token or the voice channel is not set, the discord_voice service is inactive")
		return nil
	}
	for {
		err := s.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.ports.Logger.WarnContext(ctx, "the discord gateway connection dropped", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.reconnect):
		}
	}
}

// connect opens the gateway connection and runs it until it drops or
// ctx ends.
func (s *Service) connect(ctx context.Context) error {
	session, err := discordgo.New(s.ports.Token)
	if err != nil {
		return err
	}
	// Plain JSON packets; the service needs only the voice states, so
	// the intents stay minimal.
	session.Identify.Compress = false
	session.Identify.Intents = discordgo.IntentGuilds | discordgo.IntentGuildVoiceStates
	session.AddHandler(s.onReady)
	session.AddHandler(s.onGuildCreate)
	session.AddHandler(s.onVoiceState)
	s.publishCtx = ctx
	if err := session.Open(); err != nil {
		return err
	}
	defer session.Close()
	<-ctx.Done()
	return ctx.Err()
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
	for _, vs := range g.Guild.VoiceStates {
		s.seed(vs)
	}
}

// onVoiceState applies one voice state update of the gateway (B37): a
// join, a leave, or a move into or out of the configured channel.
func (s *Service) onVoiceState(_ *discordgo.Session, evt *discordgo.VoiceStateUpdate) {
	if evt.UserID == "" {
		return
	}
	// The membership before the update; the seed below may add the
	// occupant, so the join is the difference.
	s.mu.Lock()
	_, was := s.occupants[evt.UserID]
	s.mu.Unlock()
	s.seed(evt.VoiceState)
	now := evt.ChannelID == s.ports.Channel
	s.mu.Lock()
	var name string
	switch {
	case now && !was:
		name = s.nameLocked(evt.UserID)
		s.occupants[evt.UserID] = Occupant{Name: name, Channel: s.ports.Channel}
	case !now && was:
		name = s.nameLocked(evt.UserID)
		delete(s.occupants, evt.UserID)
	}
	channel := s.ports.Channel
	s.mu.Unlock()
	switch {
	case now && !was:
		s.publish(eventtype.DiscordVoiceJoined, name, channel)
	case !now && was:
		s.publish(eventtype.DiscordVoiceLeft, name, channel)
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
	if vs.ChannelID == s.ports.Channel {
		s.occupants[vs.UserID] = Occupant{Name: s.nameLocked(vs.UserID), Channel: s.ports.Channel}
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

// publish runs typ as a bus event with the occupant as details (B37);
// it fails only if the event does not fit the catalog, which the shape
// of the type already guarantees.
func (s *Service) publish(typ event.Type, name, channel string) {
	if s.publishCtx == nil {
		return
	}
	env := event.New(event.Source{Kind: event.SourceIntegration, Name: "discord"}, typ, eventtype.Payload{
		Details: eventtype.Details{DiscordVoice: &eventtype.DiscordVoice{Name: name, Channel: channel}},
	})
	if err := s.ports.Bus.Publish(s.publishCtx, env); err != nil {
		s.ports.Logger.ErrorContext(s.publishCtx, "publishing the discord voice event failed", "type", typ, "error", err)
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
