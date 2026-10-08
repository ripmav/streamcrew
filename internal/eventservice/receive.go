// SPDX-License-Identifier: MIT

package eventservice

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/template"
)

// Message implements connector.Receiver (B13): it fires, in this order,
// "chat.user.new" if the core sees the author in the chat for the first
// time, "chat.user.join" if for the first time in the session, and
// "chat.message"; it triggers the chat command whose trigger the message
// names (commands.md, B16); then it fires "chat.user.first_message" for
// the author's first message ever and, while a stream is live, greets the
// author once per session with "chat.user.entrance" and the author's
// entrance command (command-engine.md, B41). A message of the bot account
// triggers nothing. The roles of the author are those in the message.
func (s *Service) Message(ctx context.Context, m connector.Incoming) error {
	if err := checkAccount(m.Platform, m.Author); err != nil {
		return fmt.Errorf("chat message: %w", err)
	}
	if m.Message.Text == "" {
		return errors.New("chat message: empty text")
	}
	if m.FromBot {
		s.logger.DebugContext(ctx, "chat message of the bot ignored", "platform", m.Platform)
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("chat message: %w", ErrStopped)
	}
	u, err := s.resolve(ctx, m.Author, true)
	if err != nil {
		return fmt.Errorf("chat message: %w", err)
	}
	if err := s.seeLocked(ctx, m.Platform, u); err != nil {
		return fmt.Errorf("chat message: %w", err)
	}
	msg := m.Message
	payload := eventtype.Payload{Platform: m.Platform, User: &u, Details: eventtype.Details{Message: &msg}}
	s.fire(ctx, m.Platform, eventtype.ChatMessage, payload)

	chat := chatParams(m.Platform, &u, msg)
	if err := s.recognizeLocked(ctx, msg.Text, chat); err != nil {
		return fmt.Errorf("chat message: %w", err)
	}

	first, err := s.ports.Store.FirstForUser(ctx, u.ID, eventtype.ChatUserFirstMessage)
	if err != nil {
		return fmt.Errorf("chat message: %w", err)
	}
	if first {
		s.fire(ctx, m.Platform, eventtype.ChatUserFirstMessage, payload)
	}
	if err := s.greetLocked(ctx, m.Platform, u, payload, chat); err != nil {
		return fmt.Errorf("chat message: %w", err)
	}
	return nil
}

// Join implements connector.Receiver (B14): it fires "chat.user.new" and
// "chat.user.join" as a message does, but greets nobody.
func (s *Service) Join(ctx context.Context, p platform.Name, who user.Identity) error {
	if err := checkAccount(p, who); err != nil {
		return fmt.Errorf("join: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("join: %w", ErrStopped)
	}
	u, err := s.resolve(ctx, who, false)
	if err != nil {
		return fmt.Errorf("join: %w", err)
	}
	if err := s.seeLocked(ctx, p, u); err != nil {
		return fmt.Errorf("join: %w", err)
	}
	return nil
}

// seeLocked fires what seeing u in the chat of p fires (B11, B13):
// "chat.user.new" the first time ever, "chat.user.join" the first time in
// the session. s.mu is held.
func (s *Service) seeLocked(ctx context.Context, p platform.Name, u user.User) error {
	payload := eventtype.Payload{Platform: p, User: &u}
	first, err := s.ports.Store.FirstForUser(ctx, u.ID, eventtype.ChatUserNew)
	if err != nil {
		return err
	}
	if first {
		s.fire(ctx, p, eventtype.ChatUserNew, payload)
	}
	first, err = s.ports.Store.FirstInSession(ctx, p, eventtype.ChatUserJoin, u.ID)
	if err != nil {
		return err
	}
	if first {
		s.fire(ctx, p, eventtype.ChatUserJoin, payload)
	}
	return nil
}

// recognizeLocked triggers the chat command that text names (commands.md,
// B16) with the parameters of the message. s.mu is held.
func (s *Service) recognizeLocked(ctx context.Context, text string, chat engine.Params) error {
	r, err := s.ports.Commands.Recognize(ctx, text)
	if err != nil {
		return err
	}
	switch r.Outcome {
	case command.RecognitionTriggered:
		chat.Args, chat.ArgsText = r.Args, r.ArgsText
		s.submit(ctx, engine.Request{Command: r.Command, Source: engine.SourceChat, Params: chat})
	case command.RecognitionAmbiguous:
		names := make([]string, len(r.Ambiguous))
		for i, cmd := range r.Ambiguous {
			names[i] = cmd.Name
		}
		s.logger.InfoContext(ctx, "chat message matches the triggers of several commands regardless of case; none runs",
			"commands", names)
	case command.RecognitionNone:
	}
	return nil
}

// greetLocked greets u while the stream is live on any platform, once per
// session of p (command-engine.md, B41): it fires "chat.user.entrance",
// whose event command the engine knows as a greeting, and triggers the
// entrance command of u. s.mu is held.
func (s *Service) greetLocked(ctx context.Context, p platform.Name, u user.User, payload eventtype.Payload, chat engine.Params) error {
	if !s.anyLiveLocked() {
		return nil
	}
	first, err := s.ports.Store.FirstInSession(ctx, p, eventtype.ChatUserEntrance, u.ID)
	if err != nil || !first {
		return err
	}
	s.fire(ctx, p, eventtype.ChatUserEntrance, payload)
	if u.EntranceCommand.IsZero() {
		return nil
	}
	cmd, err := s.ports.Commands.Command(ctx, u.EntranceCommand)
	if err != nil {
		s.logger.WarnContext(ctx, "loading the entrance command failed", "user", u.ID, "error", err)
		return nil
	}
	s.submit(ctx, engine.Request{Command: cmd, Source: engine.SourceChat, Params: chat, Entrance: true})
	return nil
}

// Event implements connector.Receiver: it checks that the data of e fit
// its type (B9), fires a mass gift or one gift per recipient by the
// threshold (B5), drops an event that fired before in the session for the
// same user (B3), and fires the event with its platform-neutral type after
// it (B2). Chat messages, joins and the stream come their own ways, and
// the events the core derives itself are not taken (ErrNotReceived).
func (s *Service) Event(ctx context.Context, e connector.Event) error {
	d, err := s.checkEvent(e)
	if err != nil {
		return fmt.Errorf("event %s: %w", e.Type, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("event %s: %w", e.Type, ErrStopped)
	}
	payload := eventtype.Payload{Platform: e.Platform, Details: e.Details}
	if payload.User, err = s.resolveOptional(ctx, e.User); err != nil {
		return fmt.Errorf("event %s: %w", e.Type, err)
	}
	if payload.Target, err = s.resolveOptional(ctx, e.Target); err != nil {
		return fmt.Errorf("event %s: %w", e.Type, err)
	}
	if single, ok := eventtype.SingleGift(e.Type); ok {
		return s.massGiftLocked(ctx, e, single, payload)
	}
	if d.Once == eventtype.PerUserSession && payload.User != nil {
		neutral := cmp.Or(d.Neutral, d.Type)
		first, err := s.ports.Store.FirstInSession(ctx, e.Platform, neutral, payload.User.ID)
		if err != nil {
			return fmt.Errorf("event %s: %w", e.Type, err)
		}
		if !first {
			s.logger.DebugContext(ctx, "event fired before in this stream session", "type", e.Type, "user", payload.User.ID)
			return nil
		}
	}
	s.fire(ctx, e.Platform, e.Type, payload)
	return nil
}

// checkEvent checks e before it is taken and returns the descriptor of its
// type.
func (s *Service) checkEvent(e connector.Event) (eventtype.Descriptor, error) {
	if err := e.Platform.Validate(); err != nil {
		return eventtype.Descriptor{}, err
	}
	d, ok := eventtype.Lookup(e.Type)
	if !ok {
		return d, fmt.Errorf("%w %q", event.ErrUnknownType, e.Type)
	}
	if d.Platform != "" && d.Platform != e.Platform {
		return d, fmt.Errorf("%w: a type of %s on %s", ErrNotReceived, d.Platform, e.Platform)
	}
	switch cmp.Or(d.Neutral, d.Type) {
	case eventtype.ChannelFollow, eventtype.ChannelRaid, eventtype.ChannelSubscribe, eventtype.ChannelResubscribe,
		eventtype.ChannelSubscriptionGift, eventtype.ChannelSubscriptionMassGift, eventtype.ChatWhisper,
		eventtype.ChatMessageDelete, eventtype.ChatUserLeave, eventtype.ChatUserTimeout, eventtype.ChatUserBan:
	default:
		return d, fmt.Errorf("%w: chat messages, joins and the stream have their own ways, and the core derives the other types itself", ErrNotReceived)
	}
	sh, ok := eventtype.ShapeOf(e.Type)
	if !ok {
		return d, fmt.Errorf("%w: its payload is not specified yet", ErrNotReceived)
	}
	if err := sh.Check(e.Type, e.User != nil, e.Target != nil, e.Details); err != nil {
		return d, err
	}
	for _, who := range append([]*user.Identity{e.User, e.Target}, refs(e.Recipients)...) {
		if who != nil {
			if err := checkAccount(e.Platform, *who); err != nil {
				return d, err
			}
		}
	}
	_, mass := eventtype.SingleGift(e.Type)
	switch {
	case mass && len(e.Recipients) != e.Details.Gift.Count:
		return d, fmt.Errorf("%w: %d recipients of %d gifts", eventtype.ErrShape, len(e.Recipients), e.Details.Gift.Count)
	case !mass && len(e.Recipients) > 0:
		return d, fmt.Errorf("%w: recipients without a mass gift", eventtype.ErrShape)
	}
	return d, nil
}

// massGiftLocked fires a mass gift: from the threshold of the profile on
// the mass gift alone, below it one gift per recipient, never both (B5).
// s.mu is held.
func (s *Service) massGiftLocked(ctx context.Context, e connector.Event, single event.Type, payload eventtype.Payload) error {
	cfg, err := s.settings(ctx)
	if err != nil {
		return fmt.Errorf("mass gift: %w", err)
	}
	if len(e.Recipients) >= cfg.MassGiftThreshold {
		s.fire(ctx, e.Platform, e.Type, payload)
		return nil
	}
	recipients := make([]user.User, 0, len(e.Recipients))
	for _, ident := range e.Recipients {
		u, err := s.resolve(ctx, ident, false)
		if err != nil {
			return fmt.Errorf("mass gift: %w", err)
		}
		recipients = append(recipients, u)
	}
	gift := *e.Details.Gift
	gift.Count = 1
	for _, u := range recipients {
		p := payload
		p.Target = &u
		p.Details.Gift = &gift
		s.fire(ctx, e.Platform, single, p)
	}
	return nil
}

// fireOn fires the event of platform p whose platform-neutral type is
// neutral: its own type if p has one, which brings the neutral one (B2),
// and otherwise the neutral one. s.mu is held.
func (s *Service) fireOn(ctx context.Context, p platform.Name, neutral event.Type, payload eventtype.Payload) {
	t, ok := eventtype.Specific(p, neutral)
	if !ok {
		t = neutral
	}
	s.fire(ctx, p, t, payload)
}

// fire publishes an event of type t from platform p and, after it, its
// platform-neutral type (B2), and triggers their event commands (B12).
// s.mu is held, so events and commands keep the order of their causes.
func (s *Service) fire(ctx context.Context, p platform.Name, t event.Type, payload eventtype.Payload) {
	types := []event.Type{t}
	if d, ok := eventtype.Lookup(t); ok && d.Neutral != "" {
		types = append(types, d.Neutral)
	}
	src := event.Source{Kind: event.SourcePlatform, Name: string(p)}
	for _, typ := range types {
		s.publish(ctx, event.New(src, typ, payload))
		s.triggerEvent(ctx, typ, params(typ, payload))
	}
}

// params returns the parameters of the event command of an event of type
// t (B12; command-engine.md, B80, B81).
func params(t event.Type, payload eventtype.Payload) engine.Params {
	p := engine.Params{Platform: payload.Platform, User: payload.User, Target: payload.Target, Values: values(t, payload.Details)}
	neutral := t
	if d, ok := eventtype.Lookup(t); ok && d.Neutral != "" {
		neutral = d.Neutral
	}
	switch m := payload.Details.Message; neutral {
	case eventtype.ChatMessage, eventtype.ChatUserEntrance, eventtype.ChatUserFirstMessage:
		if m != nil {
			chat := chatParams(payload.Platform, payload.User, *m)
			p.Message, p.MessageID, p.Emotes = chat.Message, chat.MessageID, chat.Emotes
		}
	default:
	}
	return p
}

// values returns the values of an event of type t for the identifiers of
// B7.
func values(t event.Type, d eventtype.Details) map[string]template.Value {
	v := make(map[string]template.Value)
	if m := d.Message; m != nil && m.Text != "" {
		v[template.EventMessage] = template.TextValue(m.Text)
	}
	if r := d.Raid; r != nil {
		v[template.EventRaidViewerCount] = template.NumberValue(decimal.New(r.Viewers))
	}
	if sub := d.Subscription; sub != nil {
		v[template.EventSubPlan] = template.TextValue(sub.Plan)
		if sub.PlanName != "" {
			v[template.EventSubPlanName] = template.TextValue(sub.PlanName)
		}
	}
	if g := d.Gift; g != nil {
		v[template.EventAnonymous] = template.TextValue(strconv.FormatBool(g.Anonymous))
		if _, mass := eventtype.SingleGift(t); mass {
			v[template.EventGiftedSubs] = template.NumberValue(decimal.New(int64(g.Count)))
		}
	}
	return v
}

// chatParams returns the parameters of a run for the chat message m of the
// user u on platform p.
func chatParams(p platform.Name, u *user.User, m eventtype.Message) engine.Params {
	return engine.Params{Platform: p, User: u, Message: m.Text, MessageID: m.ID, Emotes: m.Emotes}
}

// resolve returns the user of the account ident, created if new; with
// roles, the account gets the roles of ident, which a chat message brings
// (B13).
func (s *Service) resolve(ctx context.Context, ident user.Identity, roles bool) (user.User, error) {
	u, _, err := s.ports.Store.UpsertIdentity(ctx, ident)
	if err != nil || !roles {
		return u, err
	}
	for i, have := range u.Identities {
		if have.Platform != ident.Platform || have.PlatformUserID != ident.PlatformUserID || have.Roles == ident.Roles {
			continue
		}
		if err := s.ports.Store.SetRoles(ctx, ident.Platform, ident.PlatformUserID, ident.Roles); err != nil {
			return u, err
		}
		u.Identities[i].Roles = ident.Roles
	}
	return u, nil
}

// resolveOptional returns the user of the account ident, or nil for none.
func (s *Service) resolveOptional(ctx context.Context, ident *user.Identity) (*user.User, error) {
	if ident == nil {
		return nil, nil
	}
	u, err := s.resolve(ctx, *ident, false)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// checkAccount checks that ident is a valid account on platform p.
func checkAccount(p platform.Name, ident user.Identity) error {
	if ident.Platform != p {
		return fmt.Errorf("an account of %s on %s", ident.Platform, p)
	}
	return ident.Validate()
}

// refs returns pointers to the elements of idents.
func refs(idents []user.Identity) []*user.Identity {
	out := make([]*user.Identity, len(idents))
	for i := range idents {
		out[i] = &idents[i]
	}
	return out
}
