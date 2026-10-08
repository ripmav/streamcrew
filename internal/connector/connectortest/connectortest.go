// SPDX-License-Identifier: MIT

// Package connectortest has fakes of the ports of internal/connector for
// the tests of their consumers, such as the chat and moderation actions:
// Platform records what the core asks of a platform, and Known holds the
// users the core knows.
package connectortest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// Op is an operation of a platform.
type Op string

// The operations a Platform records.
const (
	OpSend        Op = "send"
	OpReply       Op = "reply"
	OpWhisper     Op = "whisper"
	OpDelete      Op = "delete"
	OpTimeout     Op = "timeout"
	OpPurge       Op = "purge"
	OpClearChat   Op = "clear_chat"
	OpBan         Op = "ban"
	OpUnban       Op = "unban"
	OpMod         Op = "mod"
	OpUnmod       Op = "unmod"
	OpUserByLogin Op = "user_by_login"
	OpUserByID    Op = "user_by_id"
	OpChannel     Op = "channel"
)

// Call is an operation a Platform was asked for, with what it got. Fields
// the operation does not have stay empty.
type Call struct {
	Op Op
	// From is the account of a message.
	From connector.Account
	// Text is the text of a message.
	Text string
	// MessageID is the message a reply answers or Delete deletes.
	MessageID string
	// Target is the login name of the recipient of a whisper or the target
	// of a moderation, or what a lookup searched for.
	Target string
	// Duration is the duration of a timeout.
	Duration time.Duration
	// Reason is the reason of a timeout or ban.
	Reason string
}

// Features are the optional capabilities of a Platform.
type Features struct {
	// Replies lets the chat implement connector.Replier.
	Replies bool
	// Whispers lets the chat implement connector.Whisperer.
	Whispers bool
}

// Platform is a fake platform. It records every call, fails operations
// with the errors tests set and knows the accounts tests add. Like an
// adapter, it rejects a message from an account that is not connected. It
// is safe for concurrent use.
type Platform struct {
	name     platform.Name
	features Features

	mu       sync.Mutex
	status   connector.Status
	calls    []Call
	errs     map[Op]error
	accounts []user.Identity
	channel  connector.ChannelInfo
}

// New returns a platform name with the features whose streamer account is
// connected and that has no bot.
func New(name platform.Name, f Features) *Platform {
	return &Platform{
		name: name, features: f,
		status: connector.Status{Streamer: true},
		errs:   make(map[Op]error),
	}
}

// SetStatus sets which accounts are connected.
func (p *Platform) SetStatus(s connector.Status) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = s
}

// Fail lets op fail with err from now on; nil lets it succeed again.
func (p *Platform) Fail(op Op, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		delete(p.errs, op)
		return
	}
	p.errs[op] = err
}

// AddAccounts adds accounts the platform knows; Users finds them.
func (p *Platform) AddAccounts(accounts ...user.Identity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.accounts = append(p.accounts, accounts...)
}

// Account returns an account on the platform with the login name, the
// display name in uppercase and an ID from the login.
func (p *Platform) Account(login string) user.Identity {
	return user.Identity{
		Platform: p.name, PlatformUserID: "id-" + login,
		Login: login, DisplayName: strings.ToUpper(login),
	}
}

// SetChannel sets what Channel reports.
func (p *Platform) SetChannel(c connector.ChannelInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.channel = c
}

// Calls returns the calls so far, in order.
func (p *Platform) Calls() []Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

// ForgetCalls forgets the calls so far, so that Calls and Ops start anew.
func (p *Platform) ForgetCalls() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
}

// Ops returns the operations of the calls so far, in order.
func (p *Platform) Ops() []Op {
	p.mu.Lock()
	defer p.mu.Unlock()
	ops := make([]Op, len(p.calls))
	for i, c := range p.calls {
		ops[i] = c.Op
	}
	return ops
}

// Name implements connector.Platform.
func (p *Platform) Name() platform.Name {
	return p.name
}

// Status implements connector.Platform.
func (p *Platform) Status() connector.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Chat implements connector.Platform; the result implements
// connector.Replier and connector.Whisperer as the features say.
func (p *Platform) Chat() connector.Chat {
	c := chat{p: p}
	switch {
	case p.features.Replies && p.features.Whispers:
		return replyWhisperChat{c}
	case p.features.Replies:
		return replyChat{c}
	case p.features.Whispers:
		return whisperChat{c}
	default:
		return c
	}
}

// Moderation implements connector.Platform.
func (p *Platform) Moderation() connector.Moderation {
	return moderation{p: p}
}

// Users implements connector.Platform.
func (p *Platform) Users() connector.Users {
	return users{p: p}
}

// Channel implements connector.Platform.
func (p *Platform) Channel(ctx context.Context) (connector.ChannelInfo, error) {
	if err := p.record(ctx, Call{Op: OpChannel}); err != nil {
		return connector.ChannelInfo{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.channel, nil
}

// record records c and returns the error of its operation: ctx's error if
// ctx is done, the error a test set, or ErrNotConnected for a message from
// an account that is not connected.
func (p *Platform) record(ctx context.Context, c Call) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, c)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.errs[c.Op]; err != nil {
		return err
	}
	connected := p.status.Streamer
	if c.From == connector.AccountBot {
		connected = p.status.Bot
	}
	if c.From != "" && !connected {
		return fmt.Errorf("%s %s: %w", p.name, c.From, connector.ErrNotConnected)
	}
	return nil
}

// chat is the chat without optional capabilities.
type chat struct {
	p *Platform
}

func (c chat) Send(ctx context.Context, m connector.Message) error {
	return c.p.record(ctx, Call{Op: OpSend, From: m.From, Text: m.Text})
}

func (c chat) Delete(ctx context.Context, messageID string) error {
	return c.p.record(ctx, Call{Op: OpDelete, MessageID: messageID})
}

func (c chat) reply(ctx context.Context, messageID string, m connector.Message) error {
	return c.p.record(ctx, Call{Op: OpReply, From: m.From, Text: m.Text, MessageID: messageID})
}

func (c chat) whisper(ctx context.Context, to user.Identity, m connector.Message) error {
	return c.p.record(ctx, Call{Op: OpWhisper, From: m.From, Text: m.Text, Target: to.Login})
}

// replyChat is a chat with replies.
type replyChat struct{ chat }

func (c replyChat) Reply(ctx context.Context, messageID string, m connector.Message) error {
	return c.reply(ctx, messageID, m)
}

// whisperChat is a chat with whispers.
type whisperChat struct{ chat }

func (c whisperChat) Whisper(ctx context.Context, to user.Identity, m connector.Message) error {
	return c.whisper(ctx, to, m)
}

// replyWhisperChat is a chat with replies and whispers.
type replyWhisperChat struct{ chat }

func (c replyWhisperChat) Reply(ctx context.Context, messageID string, m connector.Message) error {
	return c.reply(ctx, messageID, m)
}

func (c replyWhisperChat) Whisper(ctx context.Context, to user.Identity, m connector.Message) error {
	return c.whisper(ctx, to, m)
}

// moderation records the moderation operations.
type moderation struct {
	p *Platform
}

func (m moderation) Timeout(ctx context.Context, target user.Identity, d time.Duration, reason string) error {
	return m.p.record(ctx, Call{Op: OpTimeout, Target: target.Login, Duration: d, Reason: reason})
}

func (m moderation) Purge(ctx context.Context, target user.Identity) error {
	return m.p.record(ctx, Call{Op: OpPurge, Target: target.Login})
}

func (m moderation) ClearChat(ctx context.Context) error {
	return m.p.record(ctx, Call{Op: OpClearChat})
}

func (m moderation) Ban(ctx context.Context, target user.Identity, reason string) error {
	return m.p.record(ctx, Call{Op: OpBan, Target: target.Login, Reason: reason})
}

func (m moderation) Unban(ctx context.Context, target user.Identity) error {
	return m.p.record(ctx, Call{Op: OpUnban, Target: target.Login})
}

func (m moderation) Mod(ctx context.Context, target user.Identity) error {
	return m.p.record(ctx, Call{Op: OpMod, Target: target.Login})
}

func (m moderation) Unmod(ctx context.Context, target user.Identity) error {
	return m.p.record(ctx, Call{Op: OpUnmod, Target: target.Login})
}

// users finds the accounts tests added.
type users struct {
	p *Platform
}

func (u users) UserByLogin(ctx context.Context, login string) (user.Identity, error) {
	return u.find(ctx, OpUserByLogin, login, func(i user.Identity) bool { return strings.EqualFold(i.Login, login) })
}

func (u users) UserByID(ctx context.Context, platformUserID string) (user.Identity, error) {
	return u.find(ctx, OpUserByID, platformUserID, func(i user.Identity) bool { return i.PlatformUserID == platformUserID })
}

func (u users) find(ctx context.Context, op Op, key string, match func(user.Identity) bool) (user.Identity, error) {
	if err := u.p.record(ctx, Call{Op: op, Target: key}); err != nil {
		return user.Identity{}, err
	}
	u.p.mu.Lock()
	defer u.p.mu.Unlock()
	i := slices.IndexFunc(u.p.accounts, match)
	if i < 0 {
		return user.Identity{}, fmt.Errorf("%s %q: %w", u.p.name, key, connector.ErrUnknownUser)
	}
	return u.p.accounts[i], nil
}

// Known is a fake of the users the core knows (connector.Known). It is safe
// for concurrent use.
type Known struct {
	mu    sync.Mutex
	users []user.User
	err   error
	asked int
}

// Add adds users.
func (k *Known) Add(users ...user.User) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.users = append(k.users, users...)
}

// Fail lets every lookup fail with err from now on; nil lets them succeed
// again.
func (k *Known) Fail(err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.err = err
}

// Asked returns how many lookups there were.
func (k *Known) Asked() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.asked
}

// UserByName implements connector.Known.
func (k *Known) UserByName(_ context.Context, p platform.Name, name string) (user.User, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.asked++
	if k.err != nil {
		return user.User{}, false, k.err
	}
	for _, u := range k.users {
		for _, i := range u.Identities {
			if i.Platform == p && strings.EqualFold(i.Login, name) {
				return u, true, nil
			}
		}
	}
	return user.User{}, false, nil
}
