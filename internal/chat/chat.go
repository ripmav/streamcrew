// SPDX-License-Identifier: MIT

// Package chat is the chat service (spec chat-service.md, roadmap 5.1):
// the only write path of the core to the platform chats (send, whisper,
// delete, clear), the chat history in memory, the optional chat protocol
// on disk and the muted chat. The chat actions, the deletion of the
// triggering message and the pipeline of the event service delegate to it
// (B4).
//
// Splitting long messages and the rate limits stay with the platform
// adapters (B11).
package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/settings"
)

// ErrInvalidOption is returned by New for a port without a valid value.
var ErrInvalidOption = errors.New("invalid option")

// ErrNoWhisper is the error of a whisper no connected platform can
// deliver (B35).
var ErrNoWhisper = errors.New("no connected platform can whisper to the recipient")

// Platforms is the set of the platforms of the profile.
type Platforms interface {
	// Connected returns the platforms that are connected now (B5).
	Connected() []connector.Platform
	// Platform returns the platform name; ok is false if the profile has
	// none (B67 of actions.md).
	Platform(name platform.Name) (connector.Platform, bool)
}

// Ports are what the service needs. All are required.
type Ports struct {
	// Platforms are the platforms of the profile.
	Platforms Platforms
	// Settings reads the settings section "chat" whenever a rule needs
	// it, so a change applies at once (B15, B17).
	Settings func(ctx context.Context) (settings.Chat, error)
	// DataDir is the data directory of the profile; the protocol lives in
	// <DataDir>/chat (B17).
	DataDir string
	// Logger records the messages that are not sent (B5, B7, B10) and the
	// protocol write failures (B18).
	Logger *slog.Logger
}

// Service is the chat service. It is safe for concurrent use.
type Service struct {
	ports          Ports
	mu             sync.Mutex
	muted          bool
	histories      map[platform.Name]*ring
	protocols      map[platform.Name]*protocolFile
	protocolFailed map[platform.Name]bool
}

// New returns the service; a port without a valid value fails with
// ErrInvalidOption.
func New(p Ports) (*Service, error) {
	if p.Platforms == nil || p.Settings == nil || p.DataDir == "" {
		return nil, ErrInvalidOption
	}
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{
		ports:          p,
		histories:      make(map[platform.Name]*ring),
		protocols:      make(map[platform.Name]*protocolFile),
		protocolFailed: make(map[platform.Name]bool),
	}, nil
}

// SendRequest is a message the service sends (B5 to B8).
type SendRequest struct {
	// Text is the text, not empty; a white-space-only text is not sent
	// and is no failure (B7).
	Text string
	// AsStreamer sends from the streamer's account even if a bot is
	// connected (B6).
	AsStreamer bool
	// Channel is the channel the message goes to; empty for the home
	// channel.
	Channel connector.Channel
	// Platform limits the send to one platform (B67 of actions.md);
	// empty for every connected platform (B5). A platform that is not
	// connected is logged, and is no failure (B33).
	Platform platform.Name
	// ReplyPlatform and ReplyMessageID answer the message with the
	// platform's ID on the platform of the run, where it can (B8); both
	// empty for a message that answers nothing.
	ReplyPlatform  platform.Name
	ReplyMessageID string
}

// Send sends the message to the chat (B5 to B9). The account is the one
// B6 says, per platform. It fails if a platform fails, and the others
// keep the message (B9).
func (s *Service) Send(ctx context.Context, req SendRequest) error {
	targets := s.sendTargets(ctx, req.Platform)
	if len(targets) == 0 {
		s.ports.Logger.InfoContext(ctx, "chat message not sent: no platform connected")
		return nil
	}
	if strings.TrimSpace(req.Text) == "" {
		s.ports.Logger.DebugContext(ctx, "chat message not sent: empty after rendering")
		return nil
	}
	m := connector.Message{Text: req.Text, Channel: req.Channel}
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		msg := m
		msg.From = target.Status().Sender(req.AsStreamer)
		chat := target.Chat()
		wg.Go(func() {
			if r, ok := chat.(connector.Replier); ok && req.ReplyPlatform == target.Name() && req.ReplyMessageID != "" {
				errs[i] = r.Reply(ctx, req.ReplyMessageID, msg)
				return
			}
			errs[i] = chat.Send(ctx, msg)
		})
	}
	wg.Wait()
	return connector.JoinErrors("not sent", targets, errs)
}

// WhisperRequest is a private message the service sends (B10).
type WhisperRequest struct {
	// Text is the text, not empty.
	Text string
	// AsStreamer sends from the streamer's account even if a bot is
	// connected (B6).
	AsStreamer bool
	// Channel is the channel the message goes to; empty for the home
	// channel.
	Channel connector.Channel
	// Recipient is the user name, with or without "@" (B10).
	Recipient string
	// Known finds the users the core knows before the platform; nil for a
	// lookup through the platform only.
	Known connector.Known
}

// Whisper sends the message privately to the recipient (B10, B34, B35):
// on every connected platform that knows whispers and an account of that
// name, with the recipient found per platform first among the known
// users, then through the platform (actions.md B81). A platform it skips
// is logged; it fails if no platform whispered.
func (s *Service) Whisper(ctx context.Context, req WhisperRequest) error {
	targets := s.sendTargets(ctx, "")
	if len(targets) == 0 {
		s.ports.Logger.InfoContext(ctx, "whisper not sent: no platform connected")
		return nil
	}
	if strings.TrimSpace(req.Text) == "" {
		s.ports.Logger.DebugContext(ctx, "whisper not sent: empty after rendering")
		return nil
	}
	known := req.Known
	if known == nil {
		known = nilKnown{}
	}
	errs := make([]error, len(targets))
	sent := make([]bool, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		w, ok := target.Chat().(connector.Whisperer)
		if !ok {
			s.ports.Logger.InfoContext(ctx, "whisper skipped: the platform has no whispers",
				"platform", target.Name())
			continue
		}
		m := connector.Message{Text: req.Text, From: target.Status().Sender(req.AsStreamer), Channel: req.Channel}
		wg.Go(func() {
			to, err := connector.FindAccount(ctx, known, target, req.Recipient)
			if errors.Is(err, connector.ErrUnknownUser) {
				s.ports.Logger.InfoContext(ctx, "whisper skipped: unknown recipient",
					"platform", target.Name(), "recipient", req.Recipient)
				return
			}
			if err == nil {
				err = w.Whisper(ctx, to, m)
			}
			errs[i], sent[i] = err, err == nil
		})
	}
	wg.Wait()
	if err := connector.JoinErrors("not sent", targets, errs); err != nil {
		return err
	}
	if !slices.Contains(sent, true) {
		return fmt.Errorf("%w %q", ErrNoWhisper, connector.Login(req.Recipient))
	}
	return nil
}

// nilKnown is a lookup without users, for a whisper the core is asked for
// without the lookup of a run; the search goes through the platform only.
type nilKnown struct{}

// UserByName implements connector.Known: it knows no user.
func (nilKnown) UserByName(_ context.Context, _ platform.Name, _ string) (user.User, bool, error) {
	return user.User{}, false, nil
}

// Delete deletes the message with the platform's ID messageID from the
// chat of the channel ch on platform p (B4); the adapter decides which
// account deletes (B6, B23). p must be a platform of the profile and
// connected; an error from the platform, e.g. that it does not let the
// message be deleted, is returned as is.
func (s *Service) Delete(ctx context.Context, p platform.Name, ch connector.Channel, messageID string) error {
	target, ok := s.ports.Platforms.Platform(p)
	if !ok {
		return fmt.Errorf("delete on %s: no platform", p)
	}
	if !target.Status().Connected() {
		return fmt.Errorf("delete on %s: %w", p, connector.ErrNotConnected)
	}
	if err := target.Chat().Delete(ctx, ch, messageID); err != nil {
		return fmt.Errorf("delete on %s: %w", p, err)
	}
	return nil
}

// ClearChat removes every message from the chat of the channel ch on
// platform p (B4).
func (s *Service) ClearChat(ctx context.Context, p platform.Name, ch connector.Channel) error {
	target, ok := s.ports.Platforms.Platform(p)
	if !ok {
		return fmt.Errorf("clear the chat on %s: no platform", p)
	}
	if !target.Status().Connected() {
		return fmt.Errorf("clear the chat on %s: %w", p, connector.ErrNotConnected)
	}
	if err := target.Moderation().ClearChat(ctx, ch); err != nil {
		return fmt.Errorf("clear the chat on %s: %w", p, err)
	}
	return nil
}

// Mute turns the muted chat on (B20, actions.md B85); it is not stored,
// a restart unmutes.
func (s *Service) Mute(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.muted {
		return
	}
	s.muted = true
	s.ports.Logger.InfoContext(ctx, "chat muted")
}

// Unmute turns the muted chat off (B20).
func (s *Service) Unmute(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.muted {
		return
	}
	s.muted = false
	s.ports.Logger.InfoContext(ctx, "chat unmuted")
}

// Muted reports whether the chat is muted now (B21, B24).
func (s *Service) Muted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.muted
}

// Close closes the protocol files; later records skip the protocol.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	for p, f := range s.protocols {
		err = errors.Join(err, f.Close())
		delete(s.protocols, p)
	}
	return err
}

// sendTargets returns the platforms the service writes to: every
// connected platform, or the one named (actions.md B67), which is logged
// and skipped when not connected; in either case, an empty list when
// none (B33).
func (s *Service) sendTargets(ctx context.Context, p platform.Name) []connector.Platform {
	if p != "" {
		target, ok := s.ports.Platforms.Platform(p)
		if !ok || !target.Status().Connected() {
			s.ports.Logger.InfoContext(ctx, "message not sent: platform not connected", "platform", p)
			return nil
		}
		return []connector.Platform{target}
	}
	return s.ports.Platforms.Connected()
}
