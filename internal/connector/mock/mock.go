// SPDX-License-Identifier: Apache-2.0

// Package mock is the mock platform (ADR-0004, roadmap 3.6): an adapter
// without a network for tests, demos and development. The mock console
// ("streamcrew mock") and tests drive it. They add simulated users, write
// chat messages as them, start and stop the stream and simulate events,
// which the platform hands to the core like any adapter, through a
// connector.Receiver (spec events.md, B11 to B14).
//
// What the core asks of the platform, such as sending a chat message or a
// timeout, the platform logs and publishes as an event of type
// "mock.output". It keeps no data across runs: users, the stream and the
// IDs it remembers live as long as the Platform.
package mock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
)

// DefaultStreamer is the login name of the streamer's account unless
// WithStreamer sets another.
const DefaultStreamer = "streamer"

var (
	// ErrInvalidOption is returned by New for an option without a valid
	// value.
	ErrInvalidOption = errors.New("invalid option")
	// ErrAlreadyRunning is returned by a second call of Run.
	ErrAlreadyRunning = errors.New("the mock platform runs already")
	// ErrInvalidUser is returned for a simulated user without a valid
	// login name.
	ErrInvalidUser = errors.New("invalid simulated user")
	// ErrNotSimulated is returned by Simulate for an event type the
	// platform does not simulate that way, e.g. a chat message, which Say
	// simulates, or a type the core derives itself.
	ErrNotSimulated = errors.New("event type not simulated")
)

// Publisher publishes events; *event.Bus implements it.
type Publisher interface {
	Publish(ctx context.Context, e event.Envelope) error
}

// Option configures a Platform. An option without a valid value makes New
// fail with ErrInvalidOption; to keep a default, leave the option out.
type Option func(*options) error

type options struct {
	streamer  user.Identity
	bot       *user.Identity
	logger    *slog.Logger
	publisher Publisher
}

// WithStreamer sets the login name of the streamer's account; without it,
// the account is DefaultStreamer.
func WithStreamer(login string) Option {
	return func(o *options) error {
		ident, err := newIdentity(login, role.NewSet(role.Streamer))
		if err != nil {
			return fmt.Errorf("%w: streamer: %w", ErrInvalidOption, err)
		}
		o.streamer = ident
		return nil
	}
}

// WithBot gives the channel a bot account with the login name, connected
// while the platform runs; without it, the channel has no bot.
func WithBot(login string) Option {
	return func(o *options) error {
		ident, err := newIdentity(login, role.NewSet())
		if err != nil {
			return fmt.Errorf("%w: bot: %w", ErrInvalidOption, err)
		}
		o.bot = &ident
		return nil
	}
}

// WithLogger sets the logger; without it, the platform logs nothing.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) error {
		if l == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		o.logger = l
		return nil
	}
}

// WithPublisher sets where the outputs go (TypeOutput); without it, the
// platform publishes none.
func WithPublisher(pub Publisher) Option {
	return func(o *options) error {
		if pub == nil {
			return fmt.Errorf("%w: nil publisher", ErrInvalidOption)
		}
		o.publisher = pub
		return nil
	}
}

// Platform is the mock platform. It implements connector.Platform; its chat
// can reply and whisper. It is safe for concurrent use.
type Platform struct {
	receiver  connector.Receiver
	publisher Publisher
	logger    *slog.Logger
	dedup     *connector.Dedup
	// streamer is the key of the streamer's account and bot that of the
	// bot account; hasBot is false if the channel has none. The accounts
	// are among the users.
	streamer string
	bot      string
	hasBot   bool

	mu sync.Mutex
	// running is true while Run runs: then the accounts are connected.
	running bool
	// started is true once Run was called.
	started bool
	// users are the simulated accounts by the key of their login name,
	// including the streamer's and the bot's.
	users   map[string]user.Identity
	channel connector.ChannelInfo
	// lastID numbers the IDs the platform gives messages and events.
	lastID int
}

// New returns a mock platform that hands what it receives to r.
func New(r connector.Receiver, opts ...Option) (*Platform, error) {
	if r == nil {
		return nil, fmt.Errorf("new mock platform: %w: nil receiver", ErrInvalidOption)
	}
	streamer, err := newIdentity(DefaultStreamer, role.NewSet(role.Streamer))
	if err != nil {
		return nil, err
	}
	o := options{streamer: streamer, logger: slog.New(slog.DiscardHandler), publisher: noPublisher{}}
	var errs []error
	for _, opt := range opts {
		errs = append(errs, opt(&o))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("new mock platform: %w", err)
	}
	dedup, err := connector.NewDedup(connector.DefaultDedupTTL)
	if err != nil {
		return nil, fmt.Errorf("new mock platform: %w", err)
	}
	p := &Platform{
		receiver:  r,
		publisher: o.publisher,
		logger:    o.logger,
		dedup:     dedup,
		streamer:  key(o.streamer.Login),
		users:     map[string]user.Identity{key(o.streamer.Login): o.streamer},
	}
	if o.bot != nil {
		p.bot, p.hasBot = key(o.bot.Login), true
		if p.bot == p.streamer {
			return nil, fmt.Errorf("new mock platform: %w: the bot %q is the streamer", ErrInvalidOption, o.bot.Login)
		}
		p.users[p.bot] = *o.bot
	}
	return p, nil
}

// noPublisher publishes nothing; it stands in without WithPublisher.
type noPublisher struct{}

func (noPublisher) Publish(context.Context, event.Envelope) error { return nil }

// Run connects the accounts until ctx ends (Code-ADR-0004). After
// connecting, it tells the receiver whether the stream is live (events.md,
// B11). A second call returns ErrAlreadyRunning.
func (p *Platform) Run(ctx context.Context) error {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return fmt.Errorf("run mock platform: %w", ErrAlreadyRunning)
	}
	p.started, p.running = true, true
	live := p.channel.Live
	streamer := p.users[p.streamer].Login
	bot := "none"
	if p.hasBot {
		bot = p.users[p.bot].Login
	}
	p.mu.Unlock()
	p.logger.InfoContext(ctx, "mock platform connected", "streamer", streamer, "bot", bot)

	defer func() {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		p.logger.InfoContext(context.WithoutCancel(ctx), "mock platform disconnected")
	}()
	if err := p.receiver.Stream(ctx, platform.Mock, live); err != nil {
		p.logger.ErrorContext(ctx, "handing over the state of the stream failed", "error", err)
	}
	<-ctx.Done()
	return nil
}

// Name implements connector.Platform.
func (p *Platform) Name() platform.Name {
	return platform.Mock
}

// Status implements connector.Platform: the streamer's account and the bot,
// if the channel has one, are connected while Run runs.
func (p *Platform) Status() connector.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return connector.Status{Streamer: p.running, Bot: p.running && p.hasBot}
}

// Chat implements connector.Platform; the chat implements connector.Replier
// and connector.Whisperer.
func (p *Platform) Chat() connector.Chat {
	return chat{p: p}
}

// Moderation implements connector.Platform.
func (p *Platform) Moderation() connector.Moderation {
	return moderation{p: p}
}

// Users implements connector.Platform: it finds the simulated accounts.
func (p *Platform) Users() connector.Users {
	return users{p: p}
}

// Channel implements connector.Platform.
func (p *Platform) Channel(context.Context) (connector.ChannelInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.channel, nil
}

// Streamer returns the streamer's account.
func (p *Platform) Streamer() user.Identity {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.users[p.streamer]
}

// Bot returns the bot account; ok is false if the channel has none.
func (p *Platform) Bot() (ident user.Identity, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.hasBot {
		return user.Identity{}, false
	}
	return p.users[p.bot], true
}

// UserSpec describes a simulated user.
type UserSpec struct {
	// Login is the login name: letters, digits and underscores, at most 25
	// characters. Its lowercase form is the user's ID on the platform.
	Login string
	// DisplayName is the name in the chat; empty for the login name, as
	// adapters do whose platform has no display name.
	DisplayName string
	// Roles are the roles in the channel; the user role is implied.
	Roles role.Set
}

// AddUser adds the simulated user, or updates it if the platform knows its
// login name, and returns its account. The streamer keeps the streamer
// role.
func (p *Platform) AddUser(spec UserSpec) (user.Identity, error) {
	ident, err := newIdentity(spec.Login, spec.Roles)
	if err != nil {
		return user.Identity{}, err
	}
	if spec.DisplayName != "" {
		ident.DisplayName = spec.DisplayName
	}
	if err := ident.Validate(); err != nil {
		return user.Identity{}, fmt.Errorf("%w: %w", ErrInvalidUser, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	k := key(ident.Login)
	if k == p.streamer {
		ident.Roles = ident.Roles.With(role.Streamer)
	}
	p.users[k] = ident
	return ident, nil
}

// User returns the account of the simulated user with the login name,
// regardless of case; ok is false if the platform does not know it.
func (p *Platform) User(login string) (ident user.Identity, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ident, ok = p.users[key(login)]
	return ident, ok
}

// account returns the account with the login name; a login name the
// platform does not know becomes a new user without roles.
func (p *Platform) account(login string) (user.Identity, error) {
	if ident, ok := p.User(login); ok {
		return ident, nil
	}
	return p.AddUser(UserSpec{Login: login})
}

// newID returns a new ID for a message or an event.
func (p *Platform) newID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastID++
	return "mock-" + strconv.Itoa(p.lastID)
}

// maxLogin is the longest login name of a simulated user.
const maxLogin = 25

// newIdentity returns the account of a simulated user with the login name
// and roles.
func newIdentity(login string, roles role.Set) (user.Identity, error) {
	if login == "" || len(login) > maxLogin {
		return user.Identity{}, fmt.Errorf("%w: login name %q: want 1 to %d characters", ErrInvalidUser, login, maxLogin)
	}
	for _, r := range login {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return user.Identity{}, fmt.Errorf("%w: login name %q: only letters, digits and underscores are allowed", ErrInvalidUser, login)
		}
	}
	return user.Identity{
		Platform:       platform.Mock,
		PlatformUserID: key(login),
		Login:          login,
		DisplayName:    login,
		Roles:          roles,
	}, nil
}

// key returns the key of a login name: its lowercase form, which is also
// the ID of the account.
func key(login string) string {
	return strings.ToLower(login)
}
