// SPDX-License-Identifier: MIT

// Package users has the user lookup action (spec actions.md, B90 to B93):
// it finds a user by name or platform ID, among the users the core knows
// and then over the platform, and sets result values about them. It
// belongs to the category "users" (Code-ADR-0013).
package users

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeUserLookup is the type ID of the user lookup action (Code-ADR-0013,
// point 1).
const TypeUserLookup = "user_lookup"

// [Interop] The fixed names of the result values follow the original
// (actions.md B5, B93) and may be replaced after the legal assessment
// (roadmap Gate O, O.1).
const (
	// ResultUserName is the login name.
	ResultUserName = "lookupusername"
	// ResultDisplayName is the display name.
	ResultDisplayName = "lookupdisplayname"
	// ResultID is the ID on the platform.
	ResultID = "lookupid"
	// ResultAvatarURL is the address of the profile picture.
	ResultAvatarURL = "lookupavatarurl"
	// ResultSuccess is "True" if the lookup found the user, else "False".
	ResultSuccess = "lookupsuccess"
)

// Results returns the fixed names of the result values, in the order of
// B93.
func Results() []string {
	return []string{ResultUserName, ResultDisplayName, ResultID, ResultAvatarURL, ResultSuccess}
}

// LookupInterval is how often the core looks up a user over a platform at
// most, across all user lookups (B92).
const LookupInterval = time.Minute

// Platforms are the platforms of the profile; *connector.Set implements it.
type Platforms interface {
	// Platform returns the platform name; ok is false if the profile has
	// none.
	Platform(name platform.Name) (p connector.Platform, ok bool)
}

// Users are the users the core knows; the user service implements it
// (roadmap 5.2).
type Users interface {
	// UserByPlatformID finds the user with the account platformUserID on
	// platform p; ok is false if there is none.
	UserByPlatformID(ctx context.Context, p platform.Name, platformUserID string) (u user.User, ok bool, err error)
	// UpsertIdentity stores an account found over a platform like a newly
	// seen one, or updates its names if the core knows it (spec
	// users-and-roles.md, B4).
	UpsertIdentity(ctx context.Context, ident user.Identity) (u user.User, created bool, err error)
}

// Ports are what the user lookup needs.
type Ports struct {
	// Templates renders the name or ID.
	Templates *template.Engine
	// Platforms are the platforms users are looked up on.
	Platforms Platforms
	// Users are the users the core knows.
	Users Users
	// Logger records why a lookup over a platform did not happen (B92).
	Logger *slog.Logger
}

// ports are the ports of the user lookup, with the limit of lookups over a
// platform, which all user lookups of the registry share (B92).
type ports struct {
	Ports
	limit limiter
}

// Descriptors returns the user lookup with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("user action types: no template engine")
	case p.Platforms == nil:
		return nil, errors.New("user action types: no platforms")
	case p.Users == nil:
		return nil, errors.New("user action types: no users")
	case p.Logger == nil:
		return nil, errors.New("user action types: no logger")
	}
	ports := &ports{Ports: p}
	return descriptors(ports), nil
}

// Catalog returns the user types without ports, for the type catalog and
// commands as code (Code-ADR-0013, point 3): their actions decode,
// validate and encode, but must not run.
func Catalog() []action.Descriptor {
	return descriptors(nil)
}

// descriptors returns the user types with ports, which are nil in the
// catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeUserLookup,
			Version:  1,
			Category: action.CategoryUsers,
			Results:  Results(),
			Schema: schema.Document(
				schema.Property{Name: "platform", Schema: schema.Platform(), Required: true},
				schema.Property{Name: "user", Schema: schema.NonEmpty(schema.UIUser), Required: true},
			),
		}.WithNew(func() UserLookup {
			return UserLookup{Common: action.On(), ports: ports}
		}),
	}
}

// UserLookup is the user lookup action (actions.md B90 to B93).
type UserLookup struct {
	action.Common `json:",embed"`
	// Platform is the platform of the user; a new action has none. Saving
	// takes names of platforms this version has no adapter for, as with the
	// platform message.
	Platform platform.Name `json:"platform,omitzero"`
	// User is the login name, with or without "@", or the platform ID, as a
	// template; a new action has none.
	User  action.Template `json:"user,omitzero"`
	ports *ports
}

// DocType implements command.Action.
func (UserLookup) DocType() string { return TypeUserLookup }

// Validate implements command.Action.
func (l UserLookup) Validate() error {
	if err := l.Platform.Validate(); err != nil {
		return field("platform", fmt.Errorf("%w: %w", action.ErrInvalid, err))
	}
	if l.User == "" {
		return field("user", fmt.Errorf("%w: empty user", action.ErrInvalid))
	}
	return nil
}

// Perform implements engine.Performer. It sets the result values for the
// rest of the instance (B93): those of the user, and empty texts if it
// finds none, which is no failure. Errors of the known users, of the
// platform other than an unknown user, and of storing a user let it fail.
func (l UserLookup) Perform(ctx context.Context, run *engine.Run) error {
	text, err := l.ports.Templates.Render(ctx, l.User.Parse(), run.Scope(), template.Text)
	if err != nil {
		return err
	}
	ident, found, err := l.find(ctx, run, connector.Login(text))
	if err != nil {
		return field("user", err)
	}
	success := "False"
	if found {
		success = "True"
	}
	s := run.Scope()
	s.SetValue(ResultUserName, template.TextValue(ident.Login))
	s.SetValue(ResultDisplayName, template.TextValue(ident.DisplayName))
	s.SetValue(ResultID, template.TextValue(ident.PlatformUserID))
	s.SetValue(ResultAvatarURL, template.TextValue(ident.AvatarURL))
	s.SetValue(ResultSuccess, template.TextValue(success))
	return nil
}

// find returns the account with the login name key, regardless of case, or
// with the platform ID key (B91): first among the known users, by name
// before ID, then over the platform at most once per LookupInterval (B92).
// An account found over the platform is stored. found is false if there is
// none, also for an empty key.
func (l UserLookup) find(ctx context.Context, run *engine.Run, key string) (ident user.Identity, found bool, err error) {
	if key == "" {
		l.ports.Logger.InfoContext(ctx, "user lookup without result: empty name",
			"instance_id", run.InstanceID(), "platform", l.Platform)
		return user.Identity{}, false, nil
	}
	if ident, ok, err := l.known(ctx, run, key); err != nil || ok {
		return ident, ok, err
	}
	target, ok := l.ports.Platforms.Platform(l.Platform)
	if !ok || !target.Status().Connected() {
		l.ports.Logger.InfoContext(ctx, "user lookup without result: platform not connected",
			"instance_id", run.InstanceID(), "platform", l.Platform)
		return user.Identity{}, false, nil
	}
	if !l.ports.limit.allow() {
		l.ports.Logger.InfoContext(ctx, "user lookup without result: one lookup over a platform per minute",
			"instance_id", run.InstanceID(), "platform", l.Platform)
		return user.Identity{}, false, nil
	}
	ident, err = target.Users().UserByLogin(ctx, key)
	if errors.Is(err, connector.ErrUnknownUser) {
		ident, err = target.Users().UserByID(ctx, key)
	}
	switch {
	case errors.Is(err, connector.ErrUnknownUser):
		return user.Identity{}, false, nil
	case err != nil:
		return user.Identity{}, false, fmt.Errorf("look up %q on %s: %w", key, l.Platform, err)
	}
	if _, _, err := l.ports.Users.UpsertIdentity(ctx, ident); err != nil {
		return user.Identity{}, false, fmt.Errorf("store %q of %s: %w", key, l.Platform, err)
	}
	return ident, true, nil
}

// known returns the account of a known user with the login name key,
// found through the lookup of the run (spec command-engine.md, B17), or
// with the platform ID key.
func (l UserLookup) known(ctx context.Context, run *engine.Run, key string) (user.Identity, bool, error) {
	u, ok, err := run.UserByName(ctx, l.Platform, key)
	if err != nil {
		return user.Identity{}, false, err
	}
	if ok {
		if ident, ok := account(u, l.Platform, func(i user.Identity) bool { return strings.EqualFold(i.Login, key) }); ok {
			return ident, true, nil
		}
	}
	u, ok, err = l.ports.Users.UserByPlatformID(ctx, l.Platform, key)
	if err != nil || !ok {
		return user.Identity{}, false, err
	}
	ident, ok := account(u, l.Platform, func(i user.Identity) bool { return i.PlatformUserID == key })
	return ident, ok, nil
}

// account returns the account of u on platform p that match accepts.
func account(u user.User, p platform.Name, match func(user.Identity) bool) (user.Identity, bool) {
	for _, i := range u.Identities {
		if i.Platform == p && match(i) {
			return i, true
		}
	}
	return user.Identity{}, false
}

// limiter lets one lookup over a platform through per LookupInterval
// (B92). It is safe for concurrent use; the zero value lets the first one
// through.
type limiter struct {
	mu   sync.Mutex
	next time.Time
}

// allow reports whether a lookup over a platform may happen now; if so, the
// next one may happen after LookupInterval.
func (l *limiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Before(l.next) {
		return false
	}
	l.next = now.Add(LookupInterval)
	return true
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
