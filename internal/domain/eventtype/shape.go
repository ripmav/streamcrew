// SPDX-License-Identifier: MIT

package eventtype

import (
	"errors"
	"fmt"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
)

// Payload is the payload of the platform events on the bus (B9): the
// platform, the users and the values.
type Payload struct {
	Platform platform.Name `json:"platform"`
	// User and Target are the user and the target user (B6); nil if the
	// event has none.
	User   *user.User `json:"user,omitempty"`
	Target *user.User `json:"target,omitempty"`
	// Details are the values of the event (B7).
	Details Details `json:"details"`
}

// ErrShape is wrapped by the error of an event whose data do not fit its
// type (B9).
var ErrShape = errors.New("event data do not fit the type")

// Presence says whether an event of a type has a part (B9).
type Presence string

// Presences.
const (
	// Absent parts must be missing.
	Absent Presence = "absent"
	// Required parts must be there.
	Required Presence = "required"
	// Optional parts may be missing, e.g. the user of an anonymous gift.
	Optional Presence = "optional"
)

// Shape is what an event of a type carries, the table of B9.
type Shape struct {
	User, Target                      Presence
	Message, Raid, Subscription, Gift Presence
}

// ShapeOf returns the shape of type t: the shape of a platform-neutral
// type, or of the neutral type of a platform-specific one. ok is false for
// the application events and for platform-specific types without a
// neutral one, whose payload their phase decides.
func ShapeOf(t event.Type) (sh Shape, ok bool) {
	d, found := Lookup(t)
	if !found {
		return Shape{}, false
	}
	if d.Neutral != "" {
		t = d.Neutral
	} else if d.Platform != "" {
		return Shape{}, false
	}
	sh, ok = shapes()[t]
	return sh, ok
}

// shapes returns the table of B9.
func shapes() map[event.Type]Shape {
	none := Shape{User: Absent, Target: Absent, Message: Absent, Raid: Absent, Subscription: Absent, Gift: Absent}
	with := func(change func(*Shape)) Shape {
		sh := none
		change(&sh)
		return sh
	}
	user := with(func(s *Shape) { s.User = Required })
	userMessage := with(func(s *Shape) { s.User, s.Message = Required, Required })
	return map[event.Type]Shape{
		ChannelStreamStart: none,
		ChannelStreamStop:  none,
		ChannelFollow:      user,
		ChannelRaid:        with(func(s *Shape) { s.User, s.Raid = Required, Required }),
		ChannelSubscribe:   with(func(s *Shape) { s.User, s.Subscription = Required, Required }),
		ChannelResubscribe: with(func(s *Shape) { s.User, s.Subscription, s.Message = Required, Required, Optional }),
		ChannelSubscriptionGift: with(func(s *Shape) {
			s.User, s.Target, s.Subscription, s.Gift = Optional, Required, Required, Required
		}),
		ChannelSubscriptionMassGift: with(func(s *Shape) {
			s.User, s.Subscription, s.Gift = Optional, Required, Required
		}),
		ChatMessage:          userMessage,
		ChatWhisper:          userMessage,
		ChatMessageDelete:    userMessage,
		ChatUserJoin:         user,
		ChatUserLeave:        user,
		ChatUserNew:          user,
		ChatUserEntrance:     userMessage,
		ChatUserFirstMessage: userMessage,
		ChatUserTimeout:      user,
		ChatUserBan:          user,
	}
}

// Check checks the users and the details of an event of type t against
// the shape sh; hasUser and hasTarget say whether it has a user and a
// target user. The error wraps ErrShape.
func (sh Shape) Check(t event.Type, hasUser, hasTarget bool, d Details) error {
	var errs []error
	part := func(name string, p Presence, has bool) {
		switch {
		case p == Required && !has:
			errs = append(errs, fmt.Errorf("no %s", name))
		case p == Absent && has:
			errs = append(errs, fmt.Errorf("a %s it does not have", name))
		}
	}
	part("user", sh.User, hasUser)
	part("target user", sh.Target, hasTarget)
	part("message", sh.Message, d.Message != nil)
	part("raid", sh.Raid, d.Raid != nil)
	part("subscription", sh.Subscription, d.Subscription != nil)
	part("gift", sh.Gift, d.Gift != nil)
	neutral := t
	if desc, ok := Lookup(t); ok && desc.Neutral != "" {
		neutral = desc.Neutral
	}
	if m := d.Message; m != nil && m.Text == "" && neutral != ChatMessageDelete {
		errs = append(errs, errors.New("a message without text"))
	}
	if r := d.Raid; r != nil && r.Viewers < 0 {
		errs = append(errs, fmt.Errorf("%d viewers", r.Viewers))
	}
	if s := d.Subscription; s != nil && s.Plan == "" {
		errs = append(errs, errors.New("a subscription without plan"))
	}
	if g := d.Gift; g != nil {
		if g.Count < 1 || neutral == ChannelSubscriptionGift && g.Count != 1 {
			errs = append(errs, fmt.Errorf("a gift of %d subscriptions", g.Count))
		}
		if g.Anonymous == hasUser {
			errs = append(errs, errors.New("an anonymous gift has no user, any other one has"))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrShape, t, err)
	}
	return nil
}

// SingleGift returns the type of a gift to one user that belongs to the
// mass gift type t (B5); ok is false if t is no mass gift type.
func SingleGift(t event.Type) (event.Type, bool) {
	switch t {
	case ChannelSubscriptionMassGift:
		return ChannelSubscriptionGift, true
	case TwitchSubscriptionMassGift:
		return TwitchSubscriptionGift, true
	default:
		return "", false
	}
}

// Specific returns the type of platform p whose platform-neutral type is
// neutral, e.g. "twitch.stream.start" for Twitch and
// "channel.stream.start" (B2); ok is false if p has none, as the mock
// platform.
func Specific(p platform.Name, neutral event.Type) (event.Type, bool) {
	for _, d := range All() {
		if d.Platform == p && d.Neutral == neutral {
			return d.Type, true
		}
	}
	return "", false
}
