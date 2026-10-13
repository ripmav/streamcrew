// SPDX-License-Identifier: MIT

package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chatsvc "github.com/ripmav/streamcrew/internal/chat"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/connectortest"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/settings"
)

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

// fixture is a chat service on three platforms: Twitch with replies and
// whispers and a connected bot, YouTube without either, and Kick with
// whispers.
type fixture struct {
	t       *testing.T
	svc     *chatsvc.Service
	cfg     *settings.Chat
	twitch  *connectortest.Platform
	youtube *connectortest.Platform
	kick    *connectortest.Platform
	logs    *logs
	dir     string
}

// newFixture returns a fixture. Create it inside synctest.Test.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:       t,
		twitch:  connectortest.New(platform.Twitch, connectortest.Features{Replies: true, Whispers: true}),
		youtube: connectortest.New(platform.YouTube, connectortest.Features{}),
		kick:    connectortest.New(platform.Kick, connectortest.Features{Whispers: true}),
		logs:    &logs{},
	}
	cfg := settings.DefaultChat()
	f.cfg = &cfg
	f.dir = t.TempDir()
	f.twitch.SetStatus(connector.Status{Streamer: true, Bot: true})
	set, err := connector.NewSet(f.twitch, f.youtube, f.kick)
	require.NoError(t, err)
	f.svc, err = chatsvc.New(chatsvc.Ports{
		Platforms: set,
		Settings: func(_ context.Context) (settings.Chat, error) {
			f.mu()
			return *f.cfg, nil
		},
		DataDir: f.dir,
		Logger:  slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, f.svc.Close()) })
	return f
}

func (f *fixture) mu() { f.logs.mu.Lock(); defer f.logs.mu.Unlock() }

// alice is a user with an account on Twitch and on Kick.
func alice() user.User {
	return user.User{ID: id.New(), Identities: []user.Identity{
		{Platform: platform.Twitch, PlatformUserID: "t-alice", Login: "alice", DisplayName: "Alice"},
		{Platform: platform.Kick, PlatformUserID: "k-alice", Login: "alice_k", DisplayName: "Alice"},
	}}
}

func TestNew(t *testing.T) {
	t.Parallel()
	set, err := connector.NewSet()
	require.NoError(t, err)
	for name, edit := range map[string]func(*chatsvc.Ports){
		"platforms": func(p *chatsvc.Ports) { p.Platforms = nil },
		"settings":  func(p *chatsvc.Ports) { p.Settings = nil },
		"datadir":   func(p *chatsvc.Ports) { p.DataDir = "" },
	} {
		p := chatsvc.Ports{
			Platforms: set,
			Settings:  func(_ context.Context) (settings.Chat, error) { return settings.Chat{}, nil },
			DataDir:   t.TempDir(),
		}
		edit(&p)
		_, err := chatsvc.New(p)
		assert.ErrorIs(t, err, chatsvc.ErrInvalidOption, name)
	}
}

// TestSend covers B5 to B9: the message goes to every connected platform
// from the account B6 says, with its channel; it fails if a platform
// fails, and the others keep the message.
func TestSend(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		ctx := t.Context()
		err := f.svc.Send(ctx, chatsvc.SendRequest{Text: "Hello", Channel: "other"})
		require.NoError(t, err)
		assert.Equal(t, []connectortest.Call{
			{Op: connectortest.OpSend, From: connector.AccountBot, Channel: "other", Text: "Hello"},
		}, f.twitch.Calls())
		assert.Equal(t, []connectortest.Call{
			{Op: connectortest.OpSend, From: connector.AccountStreamer, Channel: "other", Text: "Hello"},
		}, f.youtube.Calls(), "the bot is connected only on Twitch")
		assert.Equal(t, []connectortest.Call{
			{Op: connectortest.OpSend, From: connector.AccountStreamer, Channel: "other", Text: "Hello"},
		}, f.kick.Calls())
	})
	t.Run("as streamer", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			err := f.svc.Send(t.Context(), chatsvc.SendRequest{Text: "Hi", AsStreamer: true})
			require.NoError(t, err)
			assert.Equal(t, connector.AccountStreamer, f.twitch.Calls()[0].From)
		})
	})
	t.Run("partial failure", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.Fail(connectortest.OpSend, errors.New("boom"))
			err := f.svc.Send(t.Context(), chatsvc.SendRequest{Text: "Hi"})
			require.Error(t, err)
			assert.Equal(t, "not sent on twitch: twitch: boom", err.Error(), "B9, B66")
			assert.Equal(t, []connectortest.Op{connectortest.OpSend}, f.youtube.Ops())
		})
	})
}

// TestSendNothing covers B33 and B7: without a connected platform, on a
// platform that is not connected or the profile does not have, or with a
// white-space-only text, nothing is sent and it is no failure.
func TestSendNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		ctx := t.Context()
		for _, p := range []*connectortest.Platform{f.twitch, f.youtube, f.kick} {
			p.SetStatus(connector.Status{})
		}
		assert.NoError(t, f.svc.Send(ctx, chatsvc.SendRequest{Text: "Hi"}))
		assert.Contains(t, f.logs.String(), "no platform connected")
		f.twitch.SetStatus(connector.Status{Bot: true})
		assert.NoError(t, f.svc.Send(ctx, chatsvc.SendRequest{Text: "Hi", Platform: platform.Kick}), "B67: not connected")
		assert.NoError(t, f.svc.Send(ctx, chatsvc.SendRequest{Text: "Hi", Platform: "velora"}), "not in the profile")
		assert.Contains(t, f.logs.String(), "platform not connected")
		assert.Contains(t, f.logs.String(), "platform=velora")
		assert.Empty(t, f.twitch.Calls())
		f.twitch.SetStatus(connector.Status{Streamer: true, Bot: true})
		assert.NoError(t, f.svc.Send(ctx, chatsvc.SendRequest{Text: "  \t"}))
		assert.Empty(t, f.twitch.Calls(), "B7")
		assert.Contains(t, f.logs.String(), "empty after rendering")
	})
}

// TestSendReply covers B8: the reply answers the triggering message on the
// platform named, where it can; elsewhere it is an ordinary message.
func TestSendReply(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		err := f.svc.Send(t.Context(), chatsvc.SendRequest{
			Text: "Hi", Platform: platform.Twitch,
			ReplyPlatform: platform.Twitch, ReplyMessageID: "m1",
		})
		require.NoError(t, err)
		assert.Equal(t, []connectortest.Call{{Op: connectortest.OpReply, From: connector.AccountBot, Text: "Hi", MessageID: "m1"}}, f.twitch.Calls())
	})
}

// TestWhisper covers B10, B34 and B35: the whisper goes to the recipient
// on every connected platform that knows whispers and an account of that
// name, found first among the known users, then through the platform; it
// fails if no platform whispered.
func TestWhisper(t *testing.T) {
	t.Parallel()
	t.Run("known and looked up", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			known := &connectortest.Known{}
			known.Add(user.User{Identities: []user.Identity{f.twitch.Account("bob")}})
			f.kick.AddAccounts(f.kick.Account("bob"))
			err := f.svc.Whisper(t.Context(), chatsvc.WhisperRequest{Text: "psst", Recipient: "@Bob", Known: known})
			require.NoError(t, err)
			assert.Equal(t, []connectortest.Call{
				{Op: connectortest.OpWhisper, From: connector.AccountBot, Text: "psst", Target: "bob"},
			}, f.twitch.Calls(), "the core knows bob on Twitch")
			assert.Equal(t, []connectortest.Call{
				{Op: connectortest.OpUserByLogin, Target: "Bob"},
				{Op: connectortest.OpWhisper, From: connector.AccountStreamer, Text: "psst", Target: "bob"},
			}, f.kick.Calls(), "Kick looks bob up")
			assert.Empty(t, f.youtube.Calls())
			assert.Contains(t, f.logs.String(), "whisper skipped: the platform has no whispers")
		})
	})
	t.Run("without a lookup", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.AddAccounts(f.twitch.Account("bob"))
			err := f.svc.Whisper(t.Context(), chatsvc.WhisperRequest{Text: "psst", Recipient: "bob"})
			require.NoError(t, err)
			assert.Equal(t, connectortest.OpUserByLogin, f.twitch.Calls()[0].Op, "through the platform only")
		})
	})
	t.Run("possible on no platform", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			err := f.svc.Whisper(t.Context(), chatsvc.WhisperRequest{Text: "psst", Recipient: "carol"})
			require.Error(t, err)
			assert.ErrorIs(t, err, chatsvc.ErrNoWhisper)
		})
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.AddAccounts(f.twitch.Account("bob"))
			assert.NoError(t, f.svc.Whisper(t.Context(), chatsvc.WhisperRequest{Text: " ", Recipient: "bob"}))
			assert.Empty(t, f.twitch.Calls(), "B7")
		})
	})
}

// TestDeleteAndClear cover B4 and B6: the call goes to the platform of the
// profile; a platform without a connection is an error.
func TestDeleteAndClear(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		ctx := t.Context()
		assert.NoError(t, f.svc.Delete(ctx, platform.Twitch, "other", "m1"))
		assert.Equal(t, []connectortest.Call{{Op: connectortest.OpDelete, Channel: "other", MessageID: "m1"}}, f.twitch.Calls())
		assert.NoError(t, f.svc.ClearChat(ctx, platform.Twitch, ""))
		assert.Equal(t, connectortest.OpClearChat, f.twitch.Calls()[1].Op)
		assert.Error(t, f.svc.Delete(ctx, "velora", "", "m1"), "not in the profile")
		f.kick.SetStatus(connector.Status{Bot: true})
		err := f.svc.Delete(ctx, platform.Kick, "", "m1")
		assert.ErrorIs(t, err, connector.ErrNotConnected)
	})
}

// TestMute covers B20 and B24: the state is not stored and applies to the
// next read.
func TestMute(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	assert.False(t, f.svc.Muted())
	f.svc.Mute(ctx)
	assert.True(t, f.svc.Muted())
	f.svc.Mute(ctx)
	assert.True(t, f.svc.Muted())
	f.svc.Unmute(ctx)
	assert.False(t, f.svc.Muted())
	f.svc.Unmute(ctx)
	assert.False(t, f.svc.Muted())
	assert.Contains(t, f.logs.String(), "chat muted")
	assert.Contains(t, f.logs.String(), "chat unmuted")
}

// entry returns a history entry of alice on Twitch.
func entry(n int, text string) chatsvc.Entry {
	return chatsvc.Entry{
		Time:      time.Unix(int64(n), 0).UTC(),
		Platform:  platform.Twitch,
		UserID:    alice().ID,
		Name:      "alice",
		Text:      text,
		MessageID: fmt.Sprintf("m%d", n),
	}
}

// TestRecord covers B12 to B16: the history is a ring buffer per platform
// with the size of the section, the oldest entries fall out, and a changed
// size applies from the next save.
func TestRecord(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	f.cfg.HistorySize = 3
	for i := 1; i <= 5; i++ {
		require.NoError(t, f.svc.Record(ctx, entry(i, fmt.Sprintf("msg %d", i))))
	}
	es := f.svc.History(platform.Twitch)
	require.Len(t, es, 3)
	assert.Equal(t, "msg 3", es[0].Text, "the oldest fall out, oldest first")
	assert.Equal(t, "msg 5", es[2].Text)
	f.cfg.HistorySize = 2
	require.NoError(t, f.svc.Record(ctx, entry(6, "msg 6")))
	es = f.svc.History(platform.Twitch)
	require.Len(t, es, 2)
	assert.Equal(t, "msg 5", es[0].Text, "the new size applies from the next save")
	assert.Empty(t, f.svc.History(platform.Kick), "per platform")
	// A zero time is filled in (B13).
	require.NoError(t, f.svc.Record(ctx, chatsvc.Entry{Platform: platform.Kick, Text: "hi"}))
	assert.False(t, f.svc.History(platform.Kick)[0].Time.IsZero())
	// A history read is a copy.
	es = f.svc.History(platform.Kick)
	es[0].Text = "tampered"
	assert.Equal(t, "hi", f.svc.History(platform.Kick)[0].Text)
}

// TestRecordSettings covers the settings error of Record.
func TestRecordSettings(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		twitch := connectortest.New(platform.Twitch, connectortest.Features{})
		twitch.SetStatus(connector.Status{Streamer: true})
		set, err := connector.NewSet(twitch)
		require.NoError(t, err)
		svc, err := chatsvc.New(chatsvc.Ports{
			Platforms: set,
			Settings:  func(_ context.Context) (settings.Chat, error) { return settings.Chat{}, errors.New("boom") },
			DataDir:   t.TempDir(),
		})
		require.NoError(t, err)
		assert.Error(t, svc.Record(t.Context(), chatsvc.Entry{Platform: platform.Twitch, Text: "hi"}))
	})
}

// TestProtocol covers B17 to B19 and B37: the protocol writes JSON lines
// per platform while it is on, and a write failure is a warning, not an
// error.
func TestProtocol(t *testing.T) {
	t.Parallel()
	t.Run("on and off", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			ctx := t.Context()
			require.NoError(t, f.svc.Record(ctx, entry(1, "one")))
			_, err := os.Stat(filepath.Join(f.dir, "chat", "twitch.jsonl"))
			assert.True(t, os.IsNotExist(err), "the protocol is off by default")
			f.cfg.Protocol = true
			require.NoError(t, f.svc.Record(ctx, entry(1, "one")))
			require.NoError(t, f.svc.Record(ctx, entry(2, "two")))
			lines, err := os.ReadFile(filepath.Join(f.dir, "chat", "twitch.jsonl"))
			require.NoError(t, err)
			got := strings.Split(strings.TrimSpace(string(lines)), "\n")
			require.Len(t, got, 2)
			var e chatsvc.Entry
			require.NoError(t, json.Unmarshal([]byte(got[1]), &e))
			assert.Equal(t, "two", e.Text)
			assert.Equal(t, platform.Twitch, e.Platform)
			assert.Equal(t, "m2", e.MessageID)
		})
	})
	t.Run("write failure is a warning", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			ctx := t.Context()
			// A read-only directory makes the open of the file fail.
			broken := filepath.Join(f.dir, "chat")
			require.NoError(t, os.MkdirAll(broken, 0o500))
			f.cfg.Protocol = true
			assert.NoError(t, f.svc.Record(ctx, entry(1, "one")), "B37")
			assert.Contains(t, f.logs.String(), "chat protocol not written")
			assert.NoError(t, f.svc.Record(ctx, entry(2, "two")), "not retried, still no error")
			assert.Len(t, f.svc.History(platform.Twitch), 2, "the history is independent")
		})
	})
}
