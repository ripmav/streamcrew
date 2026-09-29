// SPDX-License-Identifier: MIT

// Package user is the model of the people in chat (spec users-and-roles.md):
// a user with an own ID, the accounts on the platforms (identities) with
// their roles, the statistics and the data the streamer keeps about a user.
//
// How statistics are counted and when the regular role is granted is decided
// by the user service (roadmap phase 5.2); this package holds the data.
package user

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
)

// User is a person from streamcrew's point of view, independent of the
// platforms (B1).
type User struct {
	// ID never changes (B1).
	ID id.ID
	// Title is the custom title the streamer gave the user; empty for the
	// default title (B5).
	Title string
	// Notes is free text of the streamer (B6).
	Notes string
	// Excluded exempts the user from currency and rank gains and
	// requirements, random picks in games and leaderboards (B7).
	Excluded bool
	// Regular is the regular role, which streamcrew grants itself (B26).
	Regular bool
	// EntranceCommand is the command that runs on the user's first message
	// in a stream session; zero for none (B8).
	EntranceCommand id.ID
	// Stats are the stored statistics (B9).
	Stats Stats
	// Identities are the user's accounts on the platforms, at least one (B2).
	Identities []Identity
	// CreatedAt and UpdatedAt are maintained by the repository.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Identity is the account of a user on a platform (B2).
type Identity struct {
	// Platform and PlatformUserID together identify the account; the same
	// account never belongs to two users (B3).
	Platform       platform.Name
	PlatformUserID string
	// Login and DisplayName follow the platform and are both required
	// (B2); they are updated on the next contact after a change (B4). An
	// adapter whose platform has no display name uses the login.
	Login       string
	DisplayName string
	// Color is the chat color, e.g. "#1E90FF"; empty if unknown.
	Color string
	// AvatarURL is the URL of the profile picture; empty if unknown.
	AvatarURL string
	// Roles are the roles on this platform's channel (B24). Regular is not
	// among them; it belongs to the user (B26).
	Roles role.Set
	// Data is the cached data the platform provides (B10).
	Data PlatformData
}

// PlatformData caches what the platform reports about an account (B10). Zero
// times mean unknown or not applicable.
type PlatformData struct {
	FollowedAt       time.Time
	SubscribedAt     time.Time
	SubTier          int // 0 for none, else the tier, e.g. 1 to 3 on Twitch
	AccountCreatedAt time.Time
	// UpdatedAt is when the data was last fetched; zero for never.
	UpdatedAt time.Time
}

// Stats are the statistics stored per user (B9).
type Stats struct {
	WatchMinutes   int64
	Messages       int64
	CommandsRun    int64
	Mentions       int64
	StreamsWatched int64
	// FirstSeen and LastSeen are the first and last contact; zero for none.
	FirstSeen time.Time
	LastSeen  time.Time
	// DonatedCents is the sum of donations in hundredths of the main
	// currency unit; converting currencies is up to the donation
	// integrations (roadmap phase 10).
	DonatedCents int64
	// Strikes counts moderation strikes.
	Strikes int64
}

// Identity returns the user's identity on platform p.
func (u User) Identity(p platform.Name) (Identity, bool) {
	for _, i := range u.Identities {
		if i.Platform == p {
			return i, true
		}
	}
	return Identity{}, false
}

// Roles returns the user's roles when acting on platform p: the roles of the
// identities on p, Regular if the user is a regular, and always User (B21,
// B24, B26).
func (u User) Roles(p platform.Name) role.Set {
	s := role.NewSet(role.User)
	if u.Regular {
		s = s.With(role.Regular)
	}
	for _, i := range u.Identities {
		if i.Platform == p {
			s = s.Union(i.Roles)
		}
	}
	return s
}

// ErrInvalid is wrapped by validation errors.
var ErrInvalid = errors.New("invalid user data")

// Validate checks an identity before it is stored.
func (i Identity) Validate() error {
	if err := i.Platform.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := text("platform user ID", i.PlatformUserID, true); err != nil {
		return err
	}
	if err := text("login", i.Login, true); err != nil {
		return err
	}
	if err := text("display name", i.DisplayName, true); err != nil {
		return err
	}
	if i.Roles.Has(role.Regular) {
		return fmt.Errorf("%w: regular is granted by streamcrew, not by a platform", ErrInvalid)
	}
	if i.Data.SubTier < 0 {
		return fmt.Errorf("%w: negative subscription tier", ErrInvalid)
	}
	return nil
}

// text checks a single-line value from a platform.
func text(what, v string, required bool) error {
	if required && v == "" {
		return fmt.Errorf("%w: empty %s", ErrInvalid, what)
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains a control character", ErrInvalid, what)
		}
	}
	return nil
}

// Repository stores users; *store.Store implements it. Methods return an
// error wrapping store.ErrNotFound for a missing user or identity and
// store.ErrConflict for an identity that belongs to another user (B3).
type Repository interface {
	// User returns a user with statistics and identities.
	User(ctx context.Context, userID id.ID) (User, error)
	// UserByIdentity finds a user by platform and platform user ID, never by
	// name (B3).
	UserByIdentity(ctx context.Context, p platform.Name, platformUserID string) (User, error)
	// UpsertIdentity updates login, display name, color and avatar of a
	// known identity (B4) or creates a new user with it. created reports
	// the latter.
	UpsertIdentity(ctx context.Context, ident Identity) (u User, created bool, err error)
	// AddIdentity links another platform account to an existing user.
	AddIdentity(ctx context.Context, userID id.ID, ident Identity) error
	// SetRoles replaces the roles of an identity, e.g. after a sync with the
	// platform (B42).
	SetRoles(ctx context.Context, p platform.Name, platformUserID string, roles role.Set) error
	// SetPlatformData replaces the cached platform data of an identity (B10).
	SetPlatformData(ctx context.Context, p platform.Name, platformUserID string, data PlatformData) error
	// UpdateUser changes title, notes, exclusion, regular role, entrance
	// command and statistics in one transaction: fn gets the stored user and
	// changes it; an error from fn discards the changes.
	UpdateUser(ctx context.Context, userID id.ID, fn func(*User) error) (User, error)
	// DeleteUser deletes a user with identities and statistics.
	DeleteUser(ctx context.Context, userID id.ID) error
}
