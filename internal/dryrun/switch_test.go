// SPDX-License-Identifier: MIT

package dryrun

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// fakeInner records the writes it receives.
type fakeInner struct {
	sent  []string
	timed []string
}

func (f *fakeInner) Name() platform.Name { return "mock" }

func (f *fakeInner) Status() connector.Status { return connector.Status{Streamer: true} }

func (f *fakeInner) Users() connector.Users { return nil }

func (f *fakeInner) Channel(context.Context) (connector.ChannelInfo, error) {
	return connector.ChannelInfo{}, nil
}

func (f *fakeInner) Chat() connector.Chat { return fakeChat{f} }

func (f *fakeInner) Moderation() connector.Moderation { return fakeMod{f} }

type fakeChat struct{ f *fakeInner }

func (c fakeChat) Send(_ context.Context, m connector.Message) error {
	c.f.sent = append(c.f.sent, m.Text)
	return nil
}
func (c fakeChat) Delete(_ context.Context, _ connector.Channel, _ string) error {
	return nil
}

type fakeMod struct{ f *fakeInner }

func (m fakeMod) Timeout(_ context.Context, _ connector.Channel, target user.Identity, _ time.Duration, _ string) error {
	m.f.timed = append(m.f.timed, target.Login)
	return nil
}
func (m fakeMod) Purge(_ context.Context, _ connector.Channel, _ user.Identity) error { return nil }
func (m fakeMod) ClearChat(_ context.Context, _ connector.Channel) error              { return nil }
func (m fakeMod) Ban(_ context.Context, _ connector.Channel, _ user.Identity, _ string) error {
	return nil
}
func (m fakeMod) Unban(_ context.Context, _ connector.Channel, _ user.Identity) error {
	return nil
}
func (m fakeMod) Mod(_ context.Context, _ connector.Channel, _ user.Identity) error {
	return nil
}
func (m fakeMod) Unmod(_ context.Context, _ connector.Channel, _ user.Identity) error {
	return nil
}

// TestSwitchablePlatform verifies the platform wrapper switches between
// dry-run recording and the inner platform at runtime (roadmap 6.6).
func TestSwitchablePlatform(t *testing.T) {
	inner := &fakeInner{}
	on := &atomic.Bool{}
	on.Store(true)
	w := NewSwitchablePlatform(inner, slog.New(slog.DiscardHandler), on)
	ctx := context.Background()

	if err := w.Chat().Send(ctx, connector.Message{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(inner.sent) != 0 {
		t.Fatalf("dry run on: the inner platform got the write: %v", inner.sent)
	}

	on.Store(false)
	if err := w.Chat().Send(ctx, connector.Message{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if len(inner.sent) != 1 || inner.sent[0] != "hello" {
		t.Fatalf("dry run off: the inner platform did not get the write: %v", inner.sent)
	}
	if err := w.Moderation().Timeout(ctx, "ch", user.Identity{Login: "x"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	if len(inner.timed) != 1 {
		t.Fatalf("moderation not forwarded: %v", inner.timed)
	}
}

// fakeRealOps marks its results so the tests can tell them apart. It
// forwards everything it does not override to the dry-run operations.
type fakeRealOps struct{ Ops }

func (fakeRealOps) Clip(context.Context) (string, error) { return "real", nil }

// TestSwitchOps verifies the operations wrapper switches at runtime
// (roadmap 6.6).
func TestSwitchOps(t *testing.T) {
	on := &atomic.Bool{}
	on.Store(true)
	o := NewSwitchOps(fakeRealOps{}, NewOps(slog.New(slog.DiscardHandler)), on)
	ctx := context.Background()

	if id, err := o.Clip(ctx); err != nil || id != ID {
		t.Fatalf("dry run on: %q, %v", id, err)
	}
	on.Store(false)
	if id, err := o.Clip(ctx); err != nil || id != "real" {
		t.Fatalf("dry run off: %q, %v", id, err)
	}
}
