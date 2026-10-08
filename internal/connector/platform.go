// SPDX-License-Identifier: MIT

// Package connector connects the core with the streaming platforms (plan
// §6.11): it has the ports of what the core asks of a platform adapter,
// namely chat, moderation, channel information and looking up users, and
// the errors adapters report. Every adapter implements Platform: the mock
// platform (roadmap 3.6) and Twitch (phase 4), each in a package below
// this one. Set holds the platforms of a profile.
//
// The names of the platforms are in internal/domain/platform.
package connector

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

var (
	// ErrNotConnected is returned for an operation that needs an account
	// that is not connected.
	ErrNotConnected = errors.New("account not connected")
	// ErrUnknownUser is returned when the platform has no account with the
	// login name or ID.
	ErrUnknownUser = errors.New("unknown user")
	// ErrRefused is wrapped by the error of an operation the platform
	// refused, e.g. for lack of rights or because the target is the
	// streamer; the error text has the reason of the platform (actions.md
	// B86).
	ErrRefused = errors.New("refused by the platform")
)

// Account is an account of the channel on a platform. The platform is
// connected with the streamer's account; a bot account is optional (plan
// §6.11).
type Account string

// The accounts of the channel.
const (
	// AccountStreamer is the account the channel belongs to.
	AccountStreamer Account = "streamer"
	// AccountBot is the account that writes as the bot.
	AccountBot Account = "bot"
)

// Accounts returns the accounts, the streamer's first.
func Accounts() []Account {
	return []Account{AccountStreamer, AccountBot}
}

// Valid reports whether a is a known account.
func (a Account) Valid() bool {
	return slices.Contains(Accounts(), a)
}

// Status says which accounts of a platform are connected.
type Status struct {
	// Streamer reports whether the streamer's account is connected. The
	// platform counts as connected exactly then (actions.md B62).
	Streamer bool
	// Bot reports whether the bot account is connected; false if the
	// platform has none.
	Bot bool
}

// Connected reports whether the platform is connected: its streamer
// account is (actions.md B62).
func (s Status) Connected() bool {
	return s.Streamer
}

// Sender returns the account chat messages go from (actions.md B61): with
// asStreamer the streamer's, otherwise the bot if it is connected and else
// the streamer's.
func (s Status) Sender(asStreamer bool) Account {
	if !asStreamer && s.Bot {
		return AccountBot
	}
	return AccountStreamer
}

// Platform is the port every platform adapter implements (plan §6.11). Its
// methods are safe for concurrent use. Optional capabilities are
// interfaces of their own, discovered by type assertion, e.g. Replier and
// Whisperer on the result of Chat.
type Platform interface {
	// Name returns the name of the platform, e.g. platform.Twitch.
	Name() platform.Name
	// Status reports which accounts are connected.
	Status() Status
	// Chat returns the chat of the channel.
	Chat() Chat
	// Moderation returns the moderation of the channel.
	Moderation() Moderation
	// Users returns the lookup of accounts on the platform.
	Users() Users
	// Channel returns the current information about the channel and its
	// stream.
	Channel(ctx context.Context) (ChannelInfo, error)
}

// Chat is the chat of the channel on a platform.
type Chat interface {
	// Send sends m to the chat. The adapter splits a text that is longer
	// than the platform allows and keeps to the rate limits of the
	// platform (actions.md B65); Send returns when the platform has taken
	// every part (actions.md B2). An error wraps ErrNotConnected if the
	// account of m is not connected.
	Send(ctx context.Context, m Message) error
	// Delete deletes the chat message with the platform's ID messageID,
	// e.g. the triggering message of a command (requirements.md) or a
	// message in a muted chat (actions.md B85).
	Delete(ctx context.Context, messageID string) error
}

// Message is a chat message the core sends.
type Message struct {
	// Text is the text; it is not empty.
	Text string
	// From is the account that sends the message.
	From Account
}

// Replier is the optional capability of a Chat to send a message as a
// reply to another one (actions.md B64).
type Replier interface {
	// Reply sends m as a reply to the message with the platform's ID
	// messageID, otherwise like Chat.Send.
	Reply(ctx context.Context, messageID string, m Message) error
}

// Whisperer is the optional capability of a Chat to send private messages
// (actions.md B63).
type Whisperer interface {
	// Whisper sends m privately to the account to, otherwise like
	// Chat.Send.
	Whisper(ctx context.Context, to user.Identity, m Message) error
}

// Moderation moderates the chat of the channel on a platform (actions.md
// B80 to B86). Strikes and a muted chat are the core's own (B84, B85). The
// error of an operation the platform refuses wraps ErrRefused (B86).
type Moderation interface {
	// Timeout bans target from the chat for d. The platform gets reason
	// where it takes one; empty means no reason (B83).
	Timeout(ctx context.Context, target user.Identity, d time.Duration, reason string) error
	// Purge removes the messages of target from the chat.
	Purge(ctx context.Context, target user.Identity) error
	// ClearChat removes every message from the chat.
	ClearChat(ctx context.Context) error
	// Ban bans target from the channel; reason as for Timeout.
	Ban(ctx context.Context, target user.Identity, reason string) error
	// Unban lifts the ban or timeout of target.
	Unban(ctx context.Context, target user.Identity) error
	// Mod makes target a moderator of the channel.
	Mod(ctx context.Context, target user.Identity) error
	// Unmod takes the moderator role from target.
	Unmod(ctx context.Context, target user.Identity) error
}

// Users looks up accounts on a platform, also of people who were never in
// the chat. An error wraps ErrUnknownUser if the platform has no such
// account.
type Users interface {
	// UserByLogin returns the account with the login name, regardless of
	// case.
	UserByLogin(ctx context.Context, login string) (user.Identity, error)
	// UserByID returns the account with the platform user ID.
	UserByID(ctx context.Context, platformUserID string) (user.Identity, error)
}

// ChannelInfo is what a platform reports about the channel and its stream.
type ChannelInfo struct {
	// Live reports whether the stream is live.
	Live bool
	// Title and Game are the title and the category of the stream; empty
	// if unknown.
	Title string
	Game  string
	// StartedAt is the start of the current stream; zero if the stream is
	// offline or the start is unknown.
	StartedAt time.Time
	// Viewers, Chatters and Followers are nil if unknown.
	Viewers   *int64
	Chatters  *int64
	Followers *int64
}
