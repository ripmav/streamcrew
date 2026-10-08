// SPDX-License-Identifier: MIT

package template

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// [Interop] The subject names in this file follow the original (spec
// template.md, B60) and may be replaced after the legal assessment (roadmap
// Gate O, O.1). The properties are in userprops.go.

// Account names an account of the channel.
type Account int

// The accounts of the channel.
const (
	// StreamerAccount is the account the channel is connected with.
	StreamerAccount Account = iota
	// BotAccount is the account that writes as the bot.
	BotAccount
)

// Users gives the user identifiers access to users beyond the run. The user
// service implements it (roadmap phase 5.2).
type Users interface {
	// Account returns the streamer's or the bot's account on platform p; ok
	// is false if there is none.
	Account(ctx context.Context, p platform.Name, a Account) (u user.User, ok bool, err error)
	// UserByName finds the user with the login name on platform p,
	// regardless of case; ok is false if there is none.
	UserByName(ctx context.Context, p platform.Name, name string) (u user.User, ok bool, err error)
	// Chatters returns the users in the chat on platform p.
	Chatters(ctx context.Context, p platform.Name) ([]user.User, error)
}

// subject finds the user an identifier is about.
type subject func(ctx context.Context, s *Scope) (user.User, bool, error)

// userSubjects returns the subjects with fixed names (B60).
func userSubjects(users Users) map[string]subject {
	return map[string]subject{
		"user": func(_ context.Context, s *Scope) (user.User, bool, error) {
			return deref(s.User)
		},
		// Without a target, the target is the triggering user.
		"targetuser": func(_ context.Context, s *Scope) (user.User, bool, error) {
			if s.Target != nil {
				return *s.Target, true, nil
			}
			return deref(s.User)
		},
		"streameruser":         account(users, StreamerAccount),
		"botuser":              account(users, BotAccount),
		"randomuser":           randomUser(users, "randomuser", func(role.Set) bool { return true }),
		"randomfolloweruser":   randomUser(users, "randomfolloweruser", hasRole(role.Follower)),
		"randomsubscriberuser": randomUser(users, "randomsubscriberuser", hasRole(role.Subscriber)),
		"randomregularuser":    randomUser(users, "randomregularuser", hasRole(role.Regular)),
	}
}

// UserFamily returns the identifiers of users: a subject that names a user
// followed by a property of that user, such as $username or
// $targetuserfollowage (B60). The subjects are $user (the triggering user),
// $targetuser (the target, without one the triggering user), $streameruser,
// $botuser, $arg<n>user (the user the n-th argument names, with or without
// "@") and random users from the chat. Without the user or with nil users,
// the identifiers have no value (B4).
func UserFamily(users Users) Family {
	subjects := userSubjects(users)
	properties := userProperties()
	prefixes := append([]string{"arg"}, slices.Sorted(maps.Keys(subjects))...)
	return Family{
		Name: "user",
		Patterns: []Pattern{{
			Name:     "<subject><property>",
			Prefixes: prefixes,
			Match: func(token string) (int, Resolver) {
				key, get := matchSubject(subjects, users, token)
				if get == nil {
					return 0, nil
				}
				name, prop := matchProperty(properties, token[len(key):])
				if prop == nil {
					return 0, nil
				}
				return len(key) + len(name), func(ctx context.Context, s *Scope) (Value, bool, error) {
					u, ok, err := memoUser(ctx, s, key, get)
					if !ok || err != nil {
						return Value{}, false, err
					}
					v, ok := prop(s, u)
					return v, ok, nil
				}
			},
		}},
	}
}

// matchSubject returns the longest subject that token starts with, and its
// name, e.g. "arg2user".
func matchSubject(subjects map[string]subject, users Users, token string) (string, subject) {
	var key string
	var get subject
	for name, sub := range subjects {
		if len(name) > len(key) && strings.HasPrefix(token, name) {
			key, get = name, sub
		}
	}
	if rest, ok := strings.CutPrefix(token, "arg"); ok {
		n, digits, ok := number(rest, maxArg)
		if ok && n >= 1 && strings.HasPrefix(rest[digits:], "user") {
			key, get = token[:len("arg")+digits+len("user")], argUser(users, n)
		}
	}
	return key, get
}

// matchProperty returns the longest property that rest starts with.
func matchProperty(properties map[string]property, rest string) (string, property) {
	var name string
	var prop property
	for n, p := range properties {
		if len(n) > len(name) && strings.HasPrefix(rest, n) {
			name, prop = n, p
		}
	}
	return name, prop
}

// memoUser finds the user of a subject once per render, so that all
// properties of a random user belong to the same user (B22).
func memoUser(ctx context.Context, s *Scope, key string, get subject) (user.User, bool, error) {
	type found struct {
		u  user.User
		ok bool
	}
	f, err := s.Memo("user.subject."+key, func() (found, error) {
		u, ok, err := get(ctx, s)
		return found{u: u, ok: ok}, err
	})
	if err != nil {
		return user.User{}, false, fmt.Errorf("find $%s: %w", key, err)
	}
	return f.u, f.ok, nil
}

// deref returns *u; ok is false for nil.
func deref(u *user.User) (user.User, bool, error) {
	if u == nil {
		return user.User{}, false, nil
	}
	return *u, true, nil
}

// account returns the subject of the streamer's or the bot's account.
func account(users Users, a Account) subject {
	return func(ctx context.Context, s *Scope) (user.User, bool, error) {
		if users == nil {
			return user.User{}, false, nil
		}
		return users.Account(ctx, s.Platform, a)
	}
}

// argUser returns the subject of the user the n-th argument names.
func argUser(users Users, n int) subject {
	return func(ctx context.Context, s *Scope) (user.User, bool, error) {
		if users == nil || n > len(s.Args) {
			return user.User{}, false, nil
		}
		name := strings.TrimPrefix(s.Args[n-1], "@")
		if name == "" {
			return user.User{}, false, nil
		}
		return users.UserByName(ctx, s.Platform, name)
	}
}

// randomUser returns the subject of a random user from the chat whose roles
// match. Users with the exception of B7 of the user spec are never picked;
// the streamer and the bot can be (B22).
func randomUser(users Users, name string, match func(role.Set) bool) subject {
	return func(ctx context.Context, s *Scope) (user.User, bool, error) {
		if users == nil {
			return user.User{}, false, nil
		}
		chatters, err := users.Chatters(ctx, s.Platform)
		if err != nil {
			return user.User{}, false, fmt.Errorf("list chatters for $%s: %w", name, err)
		}
		var candidates []user.User
		for _, u := range chatters {
			if !u.Excluded && match(u.Roles(s.Platform)) {
				candidates = append(candidates, u)
			}
		}
		if len(candidates) == 0 {
			return user.User{}, false, nil
		}
		i, err := randomBetween(0, int64(len(candidates)-1))
		if err != nil {
			return user.User{}, false, err
		}
		return candidates[i], true, nil
	}
}

// hasRole returns a match for users with role r.
func hasRole(r role.Role) func(role.Set) bool {
	return func(s role.Set) bool { return s.Has(r) }
}
