// SPDX-License-Identifier: Apache-2.0

// Package moderation has the moderation action (spec actions.md, B80 to
// B86): it times out, bans and unbans users, removes their messages,
// clears the chat, makes users moderators, counts strikes and mutes the
// chat. It belongs to the category "moderation" (Code-ADR-0013).
//
// The platforms act through the ports of internal/connector. Strikes and
// the muted chat are the core's own (B84, B85): the user service keeps the
// strikes, the chat service deletes messages while the chat is muted.
package moderation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeModeration is the type ID of the moderation action (Code-ADR-0013,
// point 1).
const TypeModeration = "moderation"

// MaxTimeout is the longest timeout in seconds, 14 days, the longest that
// Twitch allows; a platform that allows less refuses a longer one (B86).
const MaxTimeout = 14 * 24 * 60 * 60

// secondsRange is the range of the duration of a timeout (B80).
func secondsRange() action.Range {
	return action.Range{Min: 1, Max: MaxTimeout, Integer: true}
}

// Kind is what a moderation action does (actions.md B80).
type Kind string

// The kinds of the moderation action.
const (
	// KindTimeout bans a user from the chat for a while. New moderation
	// actions do this.
	KindTimeout Kind = "timeout"
	// KindPurge removes the messages of a user.
	KindPurge Kind = "purge"
	// KindClearChat removes every message.
	KindClearChat Kind = "clear_chat"
	// KindBan bans a user; KindUnban lifts a ban or timeout.
	KindBan   Kind = "ban"
	KindUnban Kind = "unban"
	// KindMod makes a user a moderator; KindUnmod takes the role back.
	KindMod   Kind = "mod"
	KindUnmod Kind = "unmod"
	// KindAddStrike and KindRemoveStrike change the strikes of a user by
	// one, KindResetStrikes sets those of every user to 0 (B84).
	KindAddStrike    Kind = "add_strike"
	KindRemoveStrike Kind = "remove_strike"
	KindResetStrikes Kind = "reset_strikes"
	// KindDisableChat mutes the chat, KindEnableChat ends that (B85).
	KindDisableChat Kind = "disable_chat"
	KindEnableChat  Kind = "enable_chat"
)

// Kinds returns the kinds, in the order editors show them.
func Kinds() []Kind {
	return []Kind{
		KindTimeout, KindPurge, KindClearChat, KindBan, KindUnban, KindMod, KindUnmod,
		KindAddStrike, KindRemoveStrike, KindResetStrikes, KindDisableChat, KindEnableChat,
	}
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	return slices.Contains(Kinds(), k)
}

// hasUser reports whether actions of kind k name a user (B81).
func (k Kind) hasUser() bool {
	return slices.Contains([]Kind{
		KindTimeout, KindPurge, KindBan, KindUnban, KindMod, KindUnmod, KindAddStrike, KindRemoveStrike,
	}, k)
}

// hasReason reports whether actions of kind k pass on a reason (B83).
func (k Kind) hasReason() bool {
	return k == KindTimeout || k == KindBan
}

// Platforms are the platforms of the profile; *connector.Set implements it.
type Platforms interface {
	// Connected returns the platforms that are connected now.
	Connected() []connector.Platform
	// Platform returns the platform name; ok is false if the profile has
	// none.
	Platform(name platform.Name) (p connector.Platform, ok bool)
}

// Users are the users the core knows; the user service implements it
// (roadmap 5.2).
type Users interface {
	// UserByName finds the user with the login name on platform p,
	// regardless of case; ok is false if there is none.
	UserByName(ctx context.Context, p platform.Name, name string) (u user.User, ok bool, err error)
	// UpsertIdentity stores an account found over a platform like a newly
	// seen one, or updates its names if the core knows it (spec
	// users-and-roles.md, B4); it returns the user.
	UpsertIdentity(ctx context.Context, ident user.Identity) (u user.User, created bool, err error)
}

// Strikes change the strikes of users (B84); the user service implements
// it (roadmap 5.2).
type Strikes interface {
	// UpdateUser changes a user in one transaction: fn gets the stored
	// user and changes it; an error from fn discards the changes.
	UpdateUser(ctx context.Context, userID id.ID, fn func(*user.User) error) (user.User, error)
	// ResetStrikes sets the strikes of every user to 0.
	ResetStrikes(ctx context.Context) error
}

// ChatMute is the muted chat (B85): while it is on, the chat service
// deletes every new chat message on the connected platforms, except those
// of the streamer and the bot and those the platform does not let it
// delete (roadmap 5.1). It is not stored.
type ChatMute interface {
	// Mute turns the muted chat on.
	Mute(ctx context.Context)
	// Unmute turns it off.
	Unmute(ctx context.Context)
}

// Ports are what the moderation action needs.
type Ports struct {
	// Templates renders the user, the reason and the duration.
	Templates *template.Engine
	// Platforms are the platforms the action acts on.
	Platforms Platforms
	// Users finds the users the core knows and stores those found over a
	// platform for a strike.
	Users Users
	// Strikes changes strikes.
	Strikes Strikes
	// Mute is the muted chat.
	Mute ChatMute
	// Logger records actions without a platform to act on.
	Logger *slog.Logger
}

// ports are the ports of the moderation action.
type ports struct {
	Ports
}

// Descriptors returns the moderation action with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("moderation action types: no template engine")
	case p.Platforms == nil:
		return nil, errors.New("moderation action types: no platforms")
	case p.Users == nil:
		return nil, errors.New("moderation action types: no users")
	case p.Strikes == nil:
		return nil, errors.New("moderation action types: no strikes")
	case p.Mute == nil:
		return nil, errors.New("moderation action types: no chat mute")
	case p.Logger == nil:
		return nil, errors.New("moderation action types: no logger")
	}
	ports := &ports{Ports: p}
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeModeration,
			Version:  1,
			Category: action.CategoryModeration,
			Schema:   moderationSchema(),
		}.WithKinds(KindTimeout, func(k Kind) (Moderation, bool) {
			return Moderation{Common: action.On(), Kind: k, ports: ports}, k.Valid()
		}),
	}, nil
}

// moderationSchema returns the schema of the moderation action: its kind
// decides whether it names a user, has a duration and a reason.
func moderationSchema() *schema.Schema {
	usr := schema.Property{Name: "user", Schema: schema.NonEmpty(schema.UIUser), Required: true}
	seconds := schema.Property{Name: "seconds", Schema: secondsRange().Schema(), Required: true}
	reason := schema.Property{Name: "reason", Schema: schema.NonEmpty(schema.UITemplate)}
	variants := make([]schema.Variant, 0, len(Kinds()))
	for _, k := range Kinds() {
		v := schema.Variant{Kind: string(k)}
		if k.hasUser() {
			v.Props = append(v.Props, usr)
		}
		if k == KindTimeout {
			v.Props = append(v.Props, seconds)
		}
		if k.hasReason() {
			v.Props = append(v.Props, reason)
		}
		variants = append(variants, v)
	}
	return schema.Kinds(nil, variants...)
}

// Moderation is the moderation action (actions.md B80 to B86). Which
// members it has depends on its kind; members a kind does not have are
// nil.
type Moderation struct {
	action.Common `json:",embed"`
	Kind          Kind `json:"kind"`
	// User names the user, with or without "@", as a template (B81); a
	// new action has none.
	User *action.Template `json:"user,omitzero"`
	// Seconds is the duration of a timeout, a whole number from 1 to
	// MaxTimeout (B80); a new action has none.
	Seconds *action.Amount `json:"seconds,omitzero"`
	// Reason is passed on with a timeout or a ban where the platform takes
	// one (B83); nil for none. It is not empty.
	Reason *action.Template `json:"reason,omitzero"`
	ports  *ports
}

// DocType implements command.Action.
func (Moderation) DocType() string { return TypeModeration }

// Validate implements command.Action.
func (m Moderation) Validate() error {
	k := m.Kind
	switch {
	case !k.Valid():
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, k))
	case k.hasUser() && m.User == nil:
		return field("user", fmt.Errorf("%w: %s needs a user", action.ErrInvalid, k))
	case !k.hasUser() && m.User != nil:
		return field("user", fmt.Errorf("%w: %s names no user", action.ErrInvalid, k))
	case m.User != nil && *m.User == "":
		return field("user", fmt.Errorf("%w: empty user", action.ErrInvalid))
	case !k.hasReason() && m.Reason != nil:
		return field("reason", fmt.Errorf("%w: %s has no reason", action.ErrInvalid, k))
	case m.Reason != nil && *m.Reason == "":
		return field("reason", fmt.Errorf("%w: empty reason", action.ErrInvalid))
	case k == KindTimeout && m.Seconds == nil:
		return field("seconds", fmt.Errorf("%w: timeout needs a duration", action.ErrInvalid))
	case k != KindTimeout && m.Seconds != nil:
		return field("seconds", fmt.Errorf("%w: only timeout has a duration", action.ErrInvalid))
	case m.Seconds != nil:
		return field("seconds", m.Seconds.Validate(secondsRange()))
	default:
		return nil
	}
}

// Perform implements engine.Performer.
func (m Moderation) Perform(ctx context.Context, run *engine.Run) error {
	switch m.Kind {
	case KindDisableChat:
		m.ports.Mute.Mute(ctx)
		return nil
	case KindEnableChat:
		m.ports.Mute.Unmute(ctx)
		return nil
	case KindResetStrikes:
		return m.ports.Strikes.ResetStrikes(ctx)
	case KindTimeout, KindPurge, KindClearChat, KindBan, KindUnban, KindMod, KindUnmod,
		KindAddStrike, KindRemoveStrike:
	default:
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, m.Kind))
	}
	in, err := m.render(ctx, run.Scope())
	if err != nil {
		return err
	}
	if m.Kind == KindAddStrike || m.Kind == KindRemoveStrike {
		return m.strike(ctx, run.Params(), in.login)
	}
	targets, err := m.targets(run.Params())
	if err != nil {
		return err
	}
	if len(targets) == 0 && !m.Kind.hasUser() {
		m.ports.Logger.InfoContext(ctx, "moderation skipped: no platform connected",
			"instance_id", run.InstanceID(), "kind", m.Kind)
		return nil
	}
	return m.moderate(ctx, run.Params(), targets, in)
}

// inputs are the rendered members of an action.
type inputs struct {
	// login is the login name of the user; empty for kinds without one.
	login string
	// duration is the duration of a timeout.
	duration time.Duration
	// reason is the reason; empty for none.
	reason string
}

// render renders the user, the reason and the duration in one render
// (B3).
func (m Moderation) render(ctx context.Context, s *template.Scope) (inputs, error) {
	var ts []template.Template
	if m.User != nil {
		ts = append(ts, m.User.Parse())
	}
	if m.Reason != nil {
		ts = append(ts, m.Reason.Parse())
	}
	var seconds []template.Template
	if m.Seconds != nil {
		var err error
		if seconds, err = m.Seconds.Templates(); err != nil {
			return inputs{}, field("seconds", err)
		}
	}
	rendered, err := m.ports.Templates.RenderEach(ctx, append(ts, seconds...), s)
	if err != nil {
		return inputs{}, err
	}
	texts := make([]string, len(rendered))
	for i, r := range rendered {
		texts[i] = r.Text
	}
	var in inputs
	if m.User != nil {
		in.login, texts = connector.Login(texts[0]), texts[1:]
	}
	if m.Reason != nil {
		in.reason, texts = strings.TrimSpace(texts[0]), texts[1:]
	}
	if m.Seconds != nil {
		v, err := m.Seconds.EvalWithTexts(texts, secondsRange())
		if err != nil {
			return inputs{}, field("seconds", err)
		}
		in.duration = time.Duration(v) * time.Second
	}
	return in, nil
}

// targets returns the platforms the action acts on (B82): the platform of
// the run, which must be connected, or without one every connected
// platform.
func (m Moderation) targets(p engine.Params) ([]connector.Platform, error) {
	if p.Platform == "" {
		return m.ports.Platforms.Connected(), nil
	}
	target, ok := m.ports.Platforms.Platform(p.Platform)
	if !ok || !target.Status().Connected() {
		return nil, fmt.Errorf("%s: %w", p.Platform, connector.ErrNotConnected)
	}
	return []connector.Platform{target}, nil
}

// moderate acts on the targets (B80 to B83). Kinds without a user act on
// each target. Kinds with a user look for it on the targets in their
// order, first among the known users, then over the platform (B81), and act
// only where they find it first (B82); with a platform of the run that is
// the only target. A lookup that fails for another reason than an unknown
// user lets the action fail, because the first match may lie there. A
// refusal of a platform lets the action fail with the reason of the
// platform (B86).
func (m Moderation) moderate(ctx context.Context, p engine.Params, targets []connector.Platform, in inputs) error {
	if !m.Kind.hasUser() {
		errs := make([]error, len(targets))
		for i, target := range targets {
			errs[i] = m.act(ctx, target.Moderation(), nil, in)
		}
		return connector.JoinErrors("failed", targets, errs)
	}
	for _, target := range targets {
		on := []connector.Platform{target}
		ident, err := connector.FindAccount(ctx, m.ports.Users, target, in.login)
		switch {
		case errors.Is(err, connector.ErrUnknownUser):
			continue
		case err != nil:
			return field("user", connector.JoinErrors("failed", on, []error{err}))
		}
		return connector.JoinErrors("failed", on, []error{m.act(ctx, target.Moderation(), &ident, in)})
	}
	return field("user", unknown(in.login, p.Platform))
}

// act does what the kind says on one platform; to is the user's account
// there, nil for kinds without a user.
func (m Moderation) act(ctx context.Context, mod connector.Moderation, to *user.Identity, in inputs) error {
	switch m.Kind {
	case KindTimeout:
		return mod.Timeout(ctx, *to, in.duration, in.reason)
	case KindPurge:
		return mod.Purge(ctx, *to)
	case KindClearChat:
		return mod.ClearChat(ctx)
	case KindBan:
		return mod.Ban(ctx, *to, in.reason)
	case KindUnban:
		return mod.Unban(ctx, *to)
	case KindMod:
		return mod.Mod(ctx, *to)
	case KindUnmod:
		return mod.Unmod(ctx, *to)
	default:
		return fmt.Errorf("%w: %s is no moderation on a platform", action.ErrInvalid, m.Kind)
	}
}

// strike adds a strike to the user or removes one, not below 0 (B84). The
// user is found as for the other kinds, the first match counts (B81, B82),
// but strikes are the core's own: on the platform of the run the known
// users count even if it is not connected, and a user found only over a
// platform is stored like a newly seen one.
func (m Moderation) strike(ctx context.Context, p engine.Params, login string) error {
	var names []platform.Name
	if p.Platform != "" {
		names = []platform.Name{p.Platform}
	} else {
		for _, target := range m.ports.Platforms.Connected() {
			names = append(names, target.Name())
		}
	}
	for _, name := range names {
		u, ok, err := m.find(ctx, name, login)
		if err != nil {
			return field("user", err)
		}
		if ok {
			return field("user", m.addStrikes(ctx, u.ID))
		}
	}
	return field("user", unknown(login, p.Platform))
}

// addStrikes adds a strike to the user or, for KindRemoveStrike, removes
// one, not below 0 (B84).
func (m Moderation) addStrikes(ctx context.Context, userID id.ID) error {
	delta := int64(1)
	if m.Kind == KindRemoveStrike {
		delta = -1
	}
	_, err := m.ports.Strikes.UpdateUser(ctx, userID, func(u *user.User) error {
		u.Stats.Strikes = max(0, u.Stats.Strikes+delta)
		return nil
	})
	return err
}

// find returns the user with the login name on platform name: a known one,
// or one the platform reports if it is connected, which is then stored; ok
// is false if there is none.
func (m Moderation) find(ctx context.Context, name platform.Name, login string) (u user.User, ok bool, err error) {
	if login == "" {
		return user.User{}, false, nil
	}
	if u, ok, err := m.ports.Users.UserByName(ctx, name, login); err != nil || ok {
		return u, ok, err
	}
	target, ok := m.ports.Platforms.Platform(name)
	if !ok || !target.Status().Connected() {
		return user.User{}, false, nil
	}
	ident, err := target.Users().UserByLogin(ctx, login)
	if errors.Is(err, connector.ErrUnknownUser) {
		return user.User{}, false, nil
	}
	if err != nil {
		return user.User{}, false, fmt.Errorf("find %q on %s: %w", login, name, err)
	}
	u, _, err = m.ports.Users.UpsertIdentity(ctx, ident)
	if err != nil {
		return user.User{}, false, fmt.Errorf("store %q of %s: %w", login, name, err)
	}
	return u, true, nil
}

// unknown returns the error of a user that was not found (B81).
func unknown(login string, on platform.Name) error {
	if on == "" {
		return fmt.Errorf("%w %q on any connected platform", connector.ErrUnknownUser, login)
	}
	return fmt.Errorf("%w %q on %s", connector.ErrUnknownUser, login, on)
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
