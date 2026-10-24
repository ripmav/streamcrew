// SPDX-License-Identifier: MIT

package userservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/template"
)

// LookupInterval is how often the service looks up a user over a platform
// at most, across all lookups and platforms (actions.md, B92).
const LookupInterval = time.Minute

// lookupLimiter lets one lookup over a platform through per
// LookupInterval (actions.md, B92). It is safe for concurrent use; the zero
// value lets the first one through.
type lookupLimiter struct {
	mu   sync.Mutex
	next time.Time
}

// allow reports whether a lookup over a platform may happen now; if so,
// the next one may happen after LookupInterval.
func (l *lookupLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Before(l.next) {
		return false
	}
	l.next = now.Add(LookupInterval)
	return true
}

// UserByName finds the user with the login name on p (spec command-engine.md,
// B17; actions.md, B91): first among the users the core knows, by the login
// name and then by the platform ID, then over the platform, which is
// limited to one lookup per minute (B92). An account found over the
// platform is stored like a newly seen one (users-and-roles.md, B4). ok is
// false if there is none; an error means the platform rejected the lookup.
func (s *Service) UserByName(ctx context.Context, p platform.Name, name string) (user.User, bool, error) {
	return s.search(ctx, p, name, true)
}

// Lookup finds the user with the login name on p as UserByName, but without
// storing an account found over the platform (actions.md, B81): the
// moderation kinds that act on a platform do not create users for it.
func (s *Service) Lookup(ctx context.Context, p platform.Name, name string) (user.User, bool, error) {
	return s.search(ctx, p, name, false)
}

// search finds the user: known by login name, the channel's accounts, known
// by platform ID, then over the platform (B92); with store, an account
// found over the platform or a channel account is stored (B4).
func (s *Service) search(ctx context.Context, p platform.Name, name string, store bool) (user.User, bool, error) {
	if u, ok, err := s.ports.Store.UserByLogin(ctx, p, name); err != nil || ok {
		return u, ok, err
	}
	if u, ok := s.accountByLogin(ctx, p, name, store); ok {
		return u, true, nil
	}
	if u, ok, err := s.ports.Store.UserByPlatformID(ctx, p, name); err != nil || ok {
		return u, ok, err
	}
	return s.platformSearch(ctx, p, name, store)
}

// platformSearch finds the account by login name, then by platform ID, over
// the platform p, and with store stores it (actions.md, B91, B92); ok is
// false if there is none or the platform is not reachable for the lookup.
func (s *Service) platformSearch(ctx context.Context, p platform.Name, name string, store bool) (user.User, bool, error) {
	adapter, ok := s.ports.Platforms.Platform(p)
	if !ok || !adapter.Status().Connected() {
		s.logger.InfoContext(ctx, "user lookup without result: platform not connected", "platform", p)
		return user.User{}, false, nil
	}
	if !s.lookup.allow() {
		s.logger.InfoContext(ctx, "user lookup without result: one lookup over a platform per minute", "platform", p)
		return user.User{}, false, nil
	}
	ident, err := adapter.Users().UserByLogin(ctx, name)
	if errors.Is(err, connector.ErrUnknownUser) {
		ident, err = adapter.Users().UserByID(ctx, name)
	}
	switch {
	case errors.Is(err, connector.ErrUnknownUser):
		return user.User{}, false, nil
	case err != nil:
		return user.User{}, false, fmt.Errorf("look up %q on %s: %w", name, p, err)
	}
	if !store {
		return user.User{Identities: []user.Identity{ident}}, true, nil
	}
	u, _, err := s.ports.Store.UpsertIdentity(ctx, ident)
	if err != nil {
		return user.User{}, false, fmt.Errorf("store %q of %s: %w", name, p, err)
	}
	return u, true, nil
}

// accountByLogin returns the streamer's or the bot's account with the login
// name on p, and with store stores it like a newly seen one (B4); ok is
// false if the platform does not report one.
func (s *Service) accountByLogin(ctx context.Context, p platform.Name, name string, store bool) (user.User, bool) {
	adapter, ok := s.ports.Platforms.Platform(p)
	if !ok {
		return user.User{}, false
	}
	idents, ok := adapter.(connector.Identities)
	if !ok {
		return user.User{}, false
	}
	login := connector.Login(name)
	if login == "" {
		return user.User{}, false
	}
	for _, a := range connector.Accounts() {
		ident, ok := idents.Identity(a)
		if ok && strings.EqualFold(ident.Login, login) {
			if !store {
				return user.User{Identities: []user.Identity{ident}}, true
			}
			u, _, err := s.ports.Store.UpsertIdentity(ctx, ident)
			if err != nil {
				s.logger.ErrorContext(ctx, "storing the channel account failed", "platform", p, "error", err)
				return user.User{}, false
			}
			return u, true
		}
	}
	return user.User{}, false
}

// Account returns the streamer's or the bot's account on p, stored like a
// newly seen one (template.md, B60); ok is false if the platform has none.
func (s *Service) Account(ctx context.Context, p platform.Name, a template.Account) (user.User, bool, error) {
	adapter, ok := s.ports.Platforms.Platform(p)
	if !ok {
		return user.User{}, false, fmt.Errorf("account: %s: no platform", p)
	}
	idents, ok := adapter.(connector.Identities)
	if !ok {
		return user.User{}, false, fmt.Errorf("account: %s: %w", p, connector.ErrNotConnected)
	}
	ident, ok := idents.Identity(connectorAccount(a))
	if !ok {
		return user.User{}, false, nil
	}
	u, _, err := s.ports.Store.UpsertIdentity(ctx, ident)
	return u, err == nil, err
}

// Chatters returns the users in the chat on p, stored like newly seen ones
// (template.md, B22).
func (s *Service) Chatters(ctx context.Context, p platform.Name) ([]user.User, error) {
	adapter, ok := s.ports.Platforms.Platform(p)
	if !ok {
		return nil, fmt.Errorf("chatters: %s: no platform", p)
	}
	ch, ok := adapter.(connector.Chatters)
	if !ok {
		return nil, fmt.Errorf("chatters: %s: %w", p, connector.ErrNotConnected)
	}
	idents, err := ch.Chatters(ctx)
	if err != nil {
		return nil, fmt.Errorf("chatters: %s: %w", p, err)
	}
	out := make([]user.User, 0, len(idents))
	for _, ident := range idents {
		u, _, err := s.ports.Store.UpsertIdentity(ctx, ident)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// UserByPlatformID returns the user of the account on p; ok is false if the
// core has not seen it (actions.md, B91).
func (s *Service) UserByPlatformID(ctx context.Context, p platform.Name, platformUserID string) (user.User, bool, error) {
	return s.ports.Store.UserByPlatformID(ctx, p, platformUserID)
}

// UpsertIdentity stores an account found over a platform like a newly seen
// one, or updates its names if the core knows it (users-and-roles.md, B4).
func (s *Service) UpsertIdentity(ctx context.Context, ident user.Identity) (user.User, bool, error) {
	return s.ports.Store.UpsertIdentity(ctx, ident)
}

// StreamerUser returns the ID of the user of the streamer's account on p,
// stored like a newly seen one (requirements.md, B4); an error means it is
// not known.
func (s *Service) StreamerUser(ctx context.Context, p platform.Name) (id.ID, error) {
	adapter, ok := s.ports.Platforms.Platform(p)
	if !ok {
		return id.ID{}, fmt.Errorf("streamer on %s: no platform", p)
	}
	idents, ok := adapter.(connector.Identities)
	if !ok {
		return id.ID{}, fmt.Errorf("streamer on %s: no identities", p)
	}
	ident, ok := idents.Identity(connector.AccountStreamer)
	if !ok {
		return id.ID{}, fmt.Errorf("streamer on %s: no account", p)
	}
	u, _, err := s.ports.Store.UpsertIdentity(ctx, ident)
	if err != nil {
		return id.ID{}, err
	}
	return u.ID, nil
}

// UpdateUser changes a user in one transaction (actions.md, B84); it
// delegates to the store.
func (s *Service) UpdateUser(ctx context.Context, userID id.ID, fn func(*user.User) error) (user.User, error) {
	return s.ports.Store.UpdateUser(ctx, userID, fn)
}

// ResetStrikes sets the strikes of every user to 0 (actions.md, B84).
func (s *Service) ResetStrikes(ctx context.Context) error {
	return s.ports.Store.ResetStrikes(ctx)
}

// connectorAccount returns the account of the platform the account of the
// template is.
func connectorAccount(a template.Account) connector.Account {
	if a == template.BotAccount {
		return connector.AccountBot
	}
	return connector.AccountStreamer
}
