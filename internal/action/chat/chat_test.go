// SPDX-License-Identifier: MIT

package chat_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/connectortest"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

// logs is a log that tests read.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// fixture is a running engine with the chat types and three platforms:
// Twitch with replies and whispers and a connected bot, YouTube without
// either, and Kick with whispers.
type fixture struct {
	t       *testing.T
	reg     *action.Registry
	harness *actiontest.Harness
	twitch  *connectortest.Platform
	youtube *connectortest.Platform
	kick    *connectortest.Platform
	known   *connectortest.Known
	logs    *logs
}

// newFixture returns a fixture. Create it inside synctest.Test.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:       t,
		twitch:  connectortest.New(platform.Twitch, connectortest.Features{Replies: true, Whispers: true}),
		youtube: connectortest.New(platform.YouTube, connectortest.Features{}),
		kick:    connectortest.New(platform.Kick, connectortest.Features{Whispers: true}),
		known:   &connectortest.Known{},
		logs:    &logs{},
	}
	f.twitch.SetStatus(connector.Status{Streamer: true, Bot: true})
	set, err := connector.NewSet(f.twitch, f.youtube, f.kick)
	require.NoError(t, err)
	f.reg = registry(t, set, f.logs)
	// The run finds known users through the engine (spec command-engine.md,
	// B17).
	f.harness = actiontest.NewHarnessWith(t, f.reg, actiontest.NewCommands(), engine.WithUsers(f.known))
	return f
}

// registry returns the chat types with a template engine that knows the
// arguments, the values of the run and the users.
func registry(t *testing.T, platforms chat.Platforms, log *logs) *action.Registry {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily(), template.UserFamily(nil))
	require.NoError(t, err)
	ds, err := chat.Descriptors(chat.Ports{
		Templates: template.New(identifiers),
		Platforms: platforms,
		Logger:    slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	require.NoError(t, err)
	reg, err := action.NewRegistry(capability.Set{}, ds...)
	require.NoError(t, err)
	return reg
}

// action returns a new chat action of kind k with the message.
func (f *fixture) action(k chat.Kind, message string) chat.Chat {
	f.t.Helper()
	d, ok := f.reg.Descriptor(chat.TypeChat)
	require.True(f.t, ok)
	doc := `{"kind":"` + string(k) + `","message":` + quote(message) + `}`
	a, err := d.Decode([]byte(doc), json.DefaultOptionsV2())
	require.NoError(f.t, err)
	c, ok := a.(chat.Chat)
	require.True(f.t, ok)
	return c
}

// message returns a new chat action that sends the message.
func (f *fixture) message(message string) chat.Chat {
	return f.action(chat.KindMessage, message)
}

// whisper returns a new chat action that whispers the message to the
// recipient.
func (f *fixture) whisper(message, recipient string) chat.Chat {
	c := f.action(chat.KindWhisper, message)
	c.Whisper.Recipient = action.Template(recipient)
	return c
}

// start runs a command with the actions and the parameters p.
func (f *fixture) start(p engine.Params, actions ...command.Action) engine.Instance {
	return f.harness.Start(f.harness.Command("x", actions...), p)
}

// quote returns s as a JSON string; the tests use plain text.
func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// alice is a user with an account on Twitch and on Kick.
func alice() *user.User {
	return &user.User{ID: id.New(), Identities: []user.Identity{
		{Platform: platform.Twitch, PlatformUserID: "t-alice", Login: "alice", DisplayName: "Alice"},
		{Platform: platform.Kick, PlatformUserID: "k-alice", Login: "alice_k", DisplayName: "Alice"},
	}}
}

// chatParams returns the parameters of a run triggered by alice's chat
// message on Twitch with the platform's message ID.
func chatParams() engine.Params {
	return engine.Params{Platform: platform.Twitch, User: alice(), Message: "!hello", MessageID: "m1"}
}

// sent returns a send call.
func sent(from connector.Account, text string) connectortest.Call {
	return connectortest.Call{Op: connectortest.OpSend, From: from, Text: text}
}

func TestConformance(t *testing.T) {
	t.Parallel()
	reg := registry(t, &connector.Set{}, &logs{})
	d, ok := reg.Descriptor(chat.TypeChat)
	require.True(t, ok)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "message", Doc: `{"type":"chat","kind":"message","message":"Hello $username!","asStreamer":false,"reply":true}`, Valid: true},
		{Name: "message with the defaults", Doc: `{"type":"chat","kind":"message","message":"Hi"}`, Valid: true},
		{Name: "as streamer", Doc: `{"type":"chat","enabled":false,"kind":"message","message":"Hi","asStreamer":true}`, Valid: true},
		{Name: "whisper", Doc: `{"type":"chat","kind":"whisper","message":"psst","recipient":"@$arg1text"}`, Valid: true},
		{Name: "whisper with the defaults", Doc: `{"type":"chat","kind":"whisper","message":"psst"}`, Valid: true},
		{Name: "message missing", Doc: `{"type":"chat","kind":"message"}`},
		{Name: "empty message", Doc: `{"type":"chat","kind":"message","message":""}`},
		{Name: "kind missing", Doc: `{"type":"chat","message":"Hi"}`},
		{Name: "unknown kind", Doc: `{"type":"chat","kind":"shout","message":"Hi"}`},
		{Name: "whisper with reply", Doc: `{"type":"chat","kind":"whisper","message":"Hi","reply":false}`},
		{Name: "message with a recipient", Doc: `{"type":"chat","kind":"message","message":"Hi","recipient":"bob"}`},
		{Name: "empty recipient", Doc: `{"type":"chat","kind":"whisper","message":"Hi","recipient":""}`},
		{Name: "reply not a switch", Doc: `{"type":"chat","kind":"message","message":"Hi","reply":"yes"}`},
		{Name: "unknown member", Doc: `{"type":"chat","kind":"message","message":"Hi","platform":"twitch"}`},
	}}.Run(t)
}

// TestNew: a new chat action sends a message without reply as the bot; a
// new whisper goes to the user of the run (actions.md B60).
func TestNew(t *testing.T) {
	t.Parallel()
	reg := registry(t, &connector.Set{}, &logs{})
	d, ok := reg.Descriptor(chat.TypeChat)
	require.True(t, ok)
	c, ok := d.New().(chat.Chat)
	require.True(t, ok)
	assert.True(t, c.Enabled())
	assert.Equal(t, chat.KindMessage, c.Kind)
	assert.False(t, c.AsStreamer)
	assert.Equal(t, &chat.MessageOptions{}, c.Chat)
	assert.Nil(t, c.Whisper)

	f := &fixture{t: t, reg: reg}
	w := f.action(chat.KindWhisper, "psst")
	assert.Equal(t, &chat.WhisperOptions{Recipient: chat.Recipient}, w.Whisper)
	assert.Nil(t, w.Chat)
}

// TestSend covers actions.md B61 and B62: the message goes to every
// connected platform, from the bot where it is connected, and with
// "as streamer" from the streamer's account.
func TestSend(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.kick.SetStatus(connector.Status{Bot: true}) // not connected
		streamer := f.message("as streamer")
		streamer.AsStreamer = true
		in := f.start(chatParams(), f.message("Hello $username"), streamer)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []connectortest.Call{
			sent(connector.AccountBot, "Hello alice"),
			sent(connector.AccountStreamer, "as streamer"),
		}, f.twitch.Calls())
		assert.Equal(t, []connectortest.Call{
			sent(connector.AccountStreamer, "Hello alice"),
			sent(connector.AccountStreamer, "as streamer"),
		}, f.youtube.Calls())
		assert.Empty(t, f.kick.Calls())
	})
}

// TestNoPlatform covers actions.md B62: without a connected platform
// nothing is sent, the core logs it, and the action does not fail.
func TestNoPlatform(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		for _, p := range []*connectortest.Platform{f.twitch, f.youtube, f.kick} {
			p.SetStatus(connector.Status{})
		}
		in := f.start(chatParams(), f.message("Hi"), f.whisper("psst", "bob"))
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors)
		assert.Equal(t, 2, strings.Count(f.logs.String(), "no platform connected"))
		for _, p := range []*connectortest.Platform{f.twitch, f.youtube, f.kick} {
			assert.Empty(t, p.Calls(), p.Name())
		}
	})
}

// TestEmpty covers actions.md B65 and B212: a message that is empty or
// white space after rendering is not sent and is no failure; a token
// without a value stays as written and is sent.
func TestEmpty(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		p := chatParams()
		p.Values = map[string]template.Value{"blank": template.TextValue(" \t\n"), "none": template.TextValue("")}
		in := f.start(p, f.message("$blank"), f.message("$none"), f.whisper("$none", "bob"), f.message("$arg1text"))
		assert.Empty(t, in.Errors)
		assert.Equal(t, 3, strings.Count(f.logs.String(), "empty after rendering"))
		assert.Equal(t, []connectortest.Call{sent(connector.AccountBot, "$arg1text")}, f.twitch.Calls(), "B212")
		assert.Equal(t, []connectortest.Call{sent(connector.AccountStreamer, "$arg1text")}, f.youtube.Calls())
		assert.Equal(t, []connectortest.Call{sent(connector.AccountStreamer, "$arg1text")}, f.kick.Calls())
	})
}

// TestReply covers actions.md B64: the reply answers the triggering message
// on the platform of the run if the platform knows replies; elsewhere, and
// without the ID of the message, it is an ordinary message.
func TestReply(t *testing.T) {
	t.Parallel()
	reply := func(f *fixture, on bool) chat.Chat {
		c := f.message("Hi")
		c.Chat.Reply = on
		return c
	}
	t.Run("on the platform of the run", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			in := f.start(chatParams(), reply(f, true), reply(f, false))
			assert.Empty(t, in.Errors)
			assert.Equal(t, []connectortest.Call{
				{Op: connectortest.OpReply, From: connector.AccountBot, Text: "Hi", MessageID: "m1"},
				sent(connector.AccountBot, "Hi"),
			}, f.twitch.Calls())
			assert.Equal(t, []connectortest.Op{connectortest.OpSend, connectortest.OpSend}, f.youtube.Ops())
			assert.Equal(t, []connectortest.Op{connectortest.OpSend, connectortest.OpSend}, f.kick.Ops())
		})
	})
	t.Run("the platform of the run has no replies", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			p := chatParams()
			p.Platform = platform.YouTube
			in := f.start(p, reply(f, true))
			assert.Empty(t, in.Errors)
			for _, pl := range []*connectortest.Platform{f.twitch, f.youtube, f.kick} {
				assert.Equal(t, []connectortest.Op{connectortest.OpSend}, pl.Ops(), pl.Name())
			}
		})
	})
	t.Run("without a triggering message", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			p := chatParams()
			p.MessageID = ""
			in := f.start(p, reply(f, true))
			assert.Empty(t, in.Errors)
			assert.Equal(t, []connectortest.Op{connectortest.OpSend}, f.twitch.Ops())
		})
	})
}

// TestWhisper covers actions.md B63: the whisper goes to the recipient on
// every connected platform that has whispers and an account of that name;
// the others are skipped with a log entry.
func TestWhisper(t *testing.T) {
	t.Parallel()
	whispered := func(from connector.Account, text, to string) connectortest.Call {
		return connectortest.Call{Op: connectortest.OpWhisper, From: from, Text: text, Target: to}
	}
	t.Run("known and looked up", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.known.Add(user.User{Identities: []user.Identity{f.twitch.Account("bob")}})
			f.kick.AddAccounts(f.kick.Account("bob"))
			in := f.start(chatParams(), f.whisper("psst $username", "@Bob"))
			assert.Empty(t, in.Errors)
			assert.Equal(t, []connectortest.Call{whispered(connector.AccountBot, "psst alice", "bob")}, f.twitch.Calls(),
				"the core knows bob on Twitch")
			assert.Equal(t, []connectortest.Call{
				{Op: connectortest.OpUserByLogin, Target: "Bob"},
				whispered(connector.AccountStreamer, "psst alice", "bob"),
			}, f.kick.Calls(), "Kick looks bob up")
			assert.Empty(t, f.youtube.Calls())
			assert.Contains(t, f.logs.String(), "whisper skipped: the platform has no whispers")
			assert.Contains(t, f.logs.String(), "platform=youtube")
		})
	})
	t.Run("to the user of the run", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.known.Add(*alice())
			in := f.start(chatParams(), f.action(chat.KindWhisper, "psst"))
			assert.Empty(t, in.Errors)
			assert.Equal(t, []connectortest.Call{whispered(connector.AccountBot, "psst", "alice")}, f.twitch.Calls())
			assert.Equal(t, []connectortest.Op{connectortest.OpUserByLogin}, f.kick.Ops(),
				"$username is alice, her login on Twitch; on Kick she is alice_k")
			assert.Contains(t, f.logs.String(), "whisper skipped: unknown recipient")
		})
	})
	t.Run("as streamer", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.AddAccounts(f.twitch.Account("bob"))
			w := f.whisper("psst", "bob")
			w.AsStreamer = true
			in := f.start(chatParams(), w)
			assert.Empty(t, in.Errors)
			assert.Equal(t, whispered(connector.AccountStreamer, "psst", "bob"), f.twitch.Calls()[1])
		})
	})
	t.Run("possible on no platform", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			in := f.start(chatParams(), f.whisper("psst", "@carol"), f.whisper("psst", "$none"))
			require.Len(t, in.Errors, 2)
			assert.Equal(t, `recipient: no connected platform can whisper to the recipient "carol"`, in.Errors[0].Message)
			assert.Equal(t, `recipient: no connected platform can whisper to the recipient "$none"`, in.Errors[1].Message)
			assert.Equal(t, chat.TypeChat, in.Errors[0].Type)
		})
	})
	t.Run("no platform has whispers", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.SetStatus(connector.Status{})
			f.kick.SetStatus(connector.Status{})
			f.known.Add(*alice())
			in := f.start(chatParams(), f.whisper("psst", "alice"))
			require.Len(t, in.Errors, 1)
			assert.Contains(t, in.Errors[0].Message, "no connected platform can whisper")
			assert.Empty(t, f.youtube.Calls())
		})
	})
}

// TestFails covers actions.md B66: if sending fails on a platform, the
// action fails and names it; the other platforms keep the message.
func TestFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	t.Run("message", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.Fail(connectortest.OpSend, boom)
			f.kick.Fail(connectortest.OpSend, errors.New("rate limited"))
			in := f.start(chatParams(), f.message("Hi"), f.message("next"))
			require.Len(t, in.Errors, 2, "the error policy continue runs the next action")
			assert.Equal(t, "not sent on twitch, kick: twitch: boom; kick: rate limited", in.Errors[0].Message)
			assert.Equal(t, []connectortest.Call{
				sent(connector.AccountStreamer, "Hi"),
				sent(connector.AccountStreamer, "next"),
			}, f.youtube.Calls())
		})
	})
	t.Run("the bot is gone", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			// The bot drops after the action has read the status; the fake
			// checks the account when it gets the message, as an adapter.
			f.twitch.Fail(connectortest.OpSend, connector.ErrNotConnected)
			in := f.start(chatParams(), f.message("Hi"))
			require.Len(t, in.Errors, 1)
			assert.Equal(t, "not sent on twitch: twitch: account not connected", in.Errors[0].Message)
		})
	})
	t.Run("reply", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.Fail(connectortest.OpReply, boom)
			c := f.message("Hi")
			c.Chat.Reply = true
			in := f.start(chatParams(), c)
			require.Len(t, in.Errors, 1)
			assert.Equal(t, "not sent on twitch: twitch: boom", in.Errors[0].Message)
		})
	})
	t.Run("whisper", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.AddAccounts(f.twitch.Account("bob"))
			f.kick.AddAccounts(f.kick.Account("bob"))
			f.kick.Fail(connectortest.OpWhisper, boom)
			in := f.start(chatParams(), f.whisper("psst", "bob"))
			require.Len(t, in.Errors, 1)
			assert.Equal(t, "not sent on kick: kick: boom", in.Errors[0].Message)
			assert.Equal(t, connectortest.OpWhisper, f.twitch.Calls()[1].Op, "Twitch keeps the whisper")
		})
	})
	t.Run("lookup", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.known.Fail(boom)
			in := f.start(chatParams(), f.whisper("psst", "bob"))
			require.Len(t, in.Errors, 1)
			assert.Equal(t, `not sent on twitch, kick: twitch: find account "bob" on twitch: look up user "bob" on twitch after 3 attempts: boom; `+
				`kick: find account "bob" on kick: look up user "bob" on kick after 3 attempts: boom`,
				in.Errors[0].Message)
		})
	})
}

// TestRenderAtRun covers actions.md B3: the message is rendered when the
// action runs and sees the values that earlier actions set.
func TestRenderAtRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		set := probe{fn: func(run *engine.Run) { run.Scope().SetValue("score", template.IntValue(42)) }}
		in := f.start(chatParams(), set, f.message("Score: $score"))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []connectortest.Call{sent(connector.AccountStreamer, "Score: 42")}, f.youtube.Calls())
	})
}

func TestPorts(t *testing.T) {
	t.Parallel()
	full := chat.Ports{
		Templates: template.New(nil),
		Platforms: &connector.Set{},
		Logger:    slog.New(slog.DiscardHandler),
	}
	_, err := chat.Descriptors(full)
	require.NoError(t, err)
	for name, edit := range map[string]func(*chat.Ports){
		"templates": func(p *chat.Ports) { p.Templates = nil },
		"platforms": func(p *chat.Ports) { p.Platforms = nil },
		"logger":    func(p *chat.Ports) { p.Logger = nil },
	} {
		p := full
		edit(&p)
		_, err := chat.Descriptors(p)
		require.Error(t, err, name)
	}
}

// probe is an action that runs fn.
type probe struct {
	fn func(run *engine.Run)
}

func (probe) DocType() string { return "probe" }
func (probe) Validate() error { return nil }
func (probe) Enabled() bool   { return true }
func (p probe) Perform(_ context.Context, run *engine.Run) error {
	p.fn(run)
	return nil
}
