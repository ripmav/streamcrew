// SPDX-License-Identifier: MIT

package dryrun_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	twitchaction "github.com/ripmav/streamcrew/internal/action/twitch"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/dryrun"
)

// recChat records the calls of the inner chat, so the test can prove the
// dry run suppressed them.
type recChat struct {
	sent    []connector.Message
	deleted []string
}

func (c *recChat) Send(_ context.Context, m connector.Message) error {
	c.sent = append(c.sent, m)
	return nil
}

func (c *recChat) Delete(_ context.Context, messageID string) error {
	c.deleted = append(c.deleted, messageID)
	return nil
}

// recMod records the calls of the inner moderation.
type recMod struct{ calls []string }

func (m *recMod) record(op string) error { m.calls = append(m.calls, op); return nil }

func (m *recMod) Timeout(_ context.Context, _ user.Identity, _ time.Duration, _ string) error {
	return m.record("timeout")
}
func (m *recMod) Purge(_ context.Context, _ user.Identity) error { return m.record("purge") }
func (m *recMod) ClearChat(_ context.Context) error              { return m.record("clear") }
func (m *recMod) Ban(_ context.Context, _ user.Identity, _ string) error {
	return m.record("ban")
}
func (m *recMod) Unban(_ context.Context, _ user.Identity) error { return m.record("unban") }
func (m *recMod) Mod(_ context.Context, _ user.Identity) error   { return m.record("mod") }
func (m *recMod) Unmod(_ context.Context, _ user.Identity) error { return m.record("unmod") }

// stubPlatform is the inner platform: reads are real, writes are recorded.
type stubPlatform struct {
	chat  *recChat
	mod   *recMod
	users user.Identity
	runs  int
}

func (s *stubPlatform) Name() platform.Name              { return platform.Twitch }
func (s *stubPlatform) Status() connector.Status         { return connector.Status{Streamer: true} }
func (s *stubPlatform) Chat() connector.Chat             { return s.chat }
func (s *stubPlatform) Moderation() connector.Moderation { return s.mod }
func (s *stubPlatform) Users() connector.Users           { return stubUsers{s} }
func (s *stubPlatform) Channel(_ context.Context) (connector.ChannelInfo, error) {
	return connector.ChannelInfo{Live: true, Title: "stub"}, nil
}
func (s *stubPlatform) Identity(a connector.Account) (user.Identity, bool) {
	if a != connector.AccountStreamer {
		return user.Identity{}, false
	}
	return s.users, true
}
func (s *stubPlatform) Run(_ context.Context) error { s.runs++; return nil }

type stubUsers struct{ p *stubPlatform }

func (u stubUsers) UserByLogin(_ context.Context, _ string) (user.Identity, error) {
	return u.p.users, nil
}
func (u stubUsers) UserByID(_ context.Context, _ string) (user.Identity, error) {
	return u.p.users, nil
}

func newLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return log, &buf
}

func TestChatRecordedAndNotSent(t *testing.T) {
	ctx := t.Context()
	inner := &recChat{}
	log, buf := newLogger(t)
	p := dryrun.NewPlatform(&stubPlatform{chat: inner, mod: &recMod{}}, log)

	require.NoError(t, p.Chat().Send(ctx, connector.Message{Text: "hello", From: connector.AccountBot}))
	require.NoError(t, p.Chat().Delete(ctx, "msg-1"))

	assert.Empty(t, inner.sent, "the dry run must not send")
	assert.Empty(t, inner.deleted, "the dry run must not delete")
	assert.Contains(t, buf.String(), "dry run: chat send")
	assert.Contains(t, buf.String(), "hello")
	assert.Contains(t, buf.String(), "dry run: chat delete")
	assert.Contains(t, buf.String(), "msg-1")
}

func TestModerationRecordedAndNotExecuted(t *testing.T) {
	ctx := t.Context()
	inner := &recMod{}
	log, buf := newLogger(t)
	p := dryrun.NewPlatform(&stubPlatform{chat: &recChat{}, mod: inner}, log)
	target := user.Identity{Platform: platform.Twitch, Login: "trouble"}

	require.NoError(t, p.Moderation().Timeout(ctx, target, time.Minute, "spam"))
	require.NoError(t, p.Moderation().Purge(ctx, target))
	require.NoError(t, p.Moderation().ClearChat(ctx))
	require.NoError(t, p.Moderation().Ban(ctx, target, "reason"))
	require.NoError(t, p.Moderation().Unban(ctx, target))
	require.NoError(t, p.Moderation().Mod(ctx, target))
	require.NoError(t, p.Moderation().Unmod(ctx, target))

	assert.Empty(t, inner.calls, "the dry run must not moderate")
	for _, op := range []string{"timeout", "purge", "clear chat", "ban", "unban", "mod", "unmod"} {
		assert.Contains(t, buf.String(), "dry run: "+op, "missing the record of %q", op)
	}
}

func TestReadsPassThrough(t *testing.T) {
	ctx := t.Context()
	log, _ := newLogger(t)
	target := user.Identity{Platform: platform.Twitch, Login: "reader"}
	inner := &stubPlatform{chat: &recChat{}, mod: &recMod{}, users: target}
	p := dryrun.NewPlatform(inner, log)

	assert.Equal(t, platform.Twitch, p.Name())
	assert.True(t, p.Status().Streamer)

	id, err := p.Users().UserByLogin(ctx, "reader")
	require.NoError(t, err)
	assert.Equal(t, "reader", id.Login)

	info, err := p.Channel(ctx)
	require.NoError(t, err)
	assert.True(t, info.Live)
	assert.Equal(t, "stub", info.Title)

	ident, ok := p.Identity(connector.AccountStreamer)
	assert.True(t, ok)
	assert.Equal(t, "reader", ident.Login)

	require.NoError(t, p.Run(ctx))
	assert.Equal(t, 1, inner.runs, "Run must reach the inner platform")
}

func TestOpsRecordAndReturnSynthetic(t *testing.T) {
	ctx := t.Context()
	log, buf := newLogger(t)
	o := dryrun.NewOps(log)

	clip, err := o.Clip(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dry-run", clip)

	marker, err := o.Marker(ctx, "a moment")
	require.NoError(t, err)
	assert.Equal(t, "dry-run", marker)

	require.NoError(t, o.StartAd(ctx, 30))
	require.NoError(t, o.Raid(ctx, "target"))
	require.NoError(t, o.Shoutout(ctx, "target"))

	poll, err := o.CreatePoll(ctx, twitchaction.PollInput{Title: "t", Choices: []string{"a", "b"}, Duration: 60})
	require.NoError(t, err)
	assert.Equal(t, "dry-run", poll)
	st, err := o.PollStatus(ctx, poll)
	require.NoError(t, err)
	assert.True(t, st.Ended, "a dry-run poll is ended")

	pred, err := o.CreatePrediction(ctx, twitchaction.PredictionInput{OutcomeA: "yes", OutcomeB: "no", Duration: 30})
	require.NoError(t, err)
	assert.Equal(t, "dry-run", pred)
	pst, err := o.PredictionStatus(ctx, pred)
	require.NoError(t, err)
	assert.True(t, pst.Ended, "a dry-run prediction is ended")

	reward, err := o.CreateReward(ctx, "reward", 100)
	require.NoError(t, err)
	assert.Equal(t, "dry-run", reward)
	require.NoError(t, o.SetReward(ctx, "reward", false))
	require.NoError(t, o.DeleteReward(ctx, "reward"))

	for _, op := range []string{"clip", "marker", "start ad", "raid", "shoutout",
		"create poll", "poll status", "create prediction", "prediction status",
		"create reward", "set reward", "delete reward"} {
		assert.Contains(t, buf.String(), "dry run: "+op, "missing the record of %q", op)
	}
}
