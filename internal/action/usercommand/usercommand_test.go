// SPDX-License-Identifier: MIT

package usercommand_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/usercommand"
	"github.com/ripmav/streamcrew/internal/capability"
	chatsvc "github.com/ripmav/streamcrew/internal/chat"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

type fakeAnswers struct {
	mu   sync.Mutex
	data map[string]string
}

func (f *fakeAnswers) SetUserAnswer(_ context.Context, userID id.ID, key, answer string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key+"|"+userID.String()] = answer
	return nil
}

func (f *fakeAnswers) UserAnswer(_ context.Context, userID id.ID, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key+"|"+userID.String()]
	return v, ok, nil
}

type fakeChat struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeChat) Send(_ context.Context, req chatsvc.SendRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, req.Text)
	return nil
}

type fixture struct {
	t       *testing.T
	reg     *action.Registry
	harness *actiontest.Harness
	answers *fakeAnswers
	chat    *fakeChat
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	answers := &fakeAnswers{data: map[string]string{}}
	chat := &fakeChat{}
	ds, err := usercommand.Descriptors(usercommand.Ports{
		Templates: template.New(identifiers),
		Chat:      chat,
		Answers:   answers,
		Logger:    slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	reg, err := action.NewRegistry(capability.Set{}, ds...)
	require.NoError(t, err)
	return &fixture{t: t, reg: reg, harness: actiontest.NewHarness(t, reg), answers: answers, chat: chat}
}

func (f *fixture) action() usercommand.UserCommand {
	f.t.Helper()
	d, ok := f.reg.Descriptor(usercommand.TypeUserCommand)
	require.True(f.t, ok)
	a, ok := d.New().(usercommand.UserCommand)
	require.True(f.t, ok)
	a.Kind, a.Text = usercommand.KindRemember, action.Template("$allargs")
	require.NoError(f.t, a.Validate())
	return a
}

func alice() *user.User { return &user.User{ID: id.New()} }

func TestRememberStoresWithArgs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		u := alice()
		cmd := f.harness.Command("mynote", f.action())
		inst := f.harness.Start(cmd, engine.Params{User: u, Args: []string{"hi", "there"}, ArgsText: "hi there"})
		assert.Equal(t, engine.StateCompleted, inst.State)
		ans, found, err := f.answers.UserAnswer(t.Context(), u.ID, cmd.ID.String())
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "hi there", ans)
	})
}

func TestRememberAnswersStored(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		u := alice()
		cmd := f.harness.Command("mynote", f.action())
		require.NoError(t, f.answers.SetUserAnswer(t.Context(), u.ID, cmd.ID.String(), "stored note"))
		inst := f.harness.Start(cmd, engine.Params{User: u})
		assert.Equal(t, engine.StateCompleted, inst.State)
		require.Len(t, f.chat.sent, 1)
		assert.Equal(t, "stored note", f.chat.sent[0])
	})
}

func TestRememberDefaultWhenNoneStored(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		u := alice()
		a := f.action()
		a.Default = action.Template("no note yet")
		cmd := f.harness.Command("mynote", a)
		inst := f.harness.Start(cmd, engine.Params{User: u})
		assert.Equal(t, engine.StateCompleted, inst.State)
		require.Len(t, f.chat.sent, 1)
		assert.Equal(t, "no note yet", f.chat.sent[0])
	})
}

func TestRememberSilentWhenNoneStoredAndNoDefault(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		u := alice()
		cmd := f.harness.Command("mynote", f.action())
		inst := f.harness.Start(cmd, engine.Params{User: u})
		assert.Equal(t, engine.StateCompleted, inst.State)
		assert.Empty(t, f.chat.sent, "nothing is sent without a stored answer or default")
	})
}

func TestPerUserAnswersAreSeparate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		ada, bob := alice(), alice()
		cmd := f.harness.Command("mynote", f.action())
		f.harness.Start(cmd, engine.Params{User: ada, Args: []string{"ada"}, ArgsText: "ada"})
		f.harness.Start(cmd, engine.Params{User: bob, Args: []string{"bob"}, ArgsText: "bob"})
		a, _, _ := f.answers.UserAnswer(t.Context(), ada.ID, cmd.ID.String())
		b, _, _ := f.answers.UserAnswer(t.Context(), bob.ID, cmd.ID.String())
		assert.Equal(t, "ada", a)
		assert.Equal(t, "bob", b)
	})
}

func TestRenameKeepsTheAnswer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		u := alice()
		cmd := f.harness.Command("mynote", f.action())
		f.harness.Start(cmd, engine.Params{User: u, Args: []string{"hi"}, ArgsText: "hi"})
		// A rename, even a case-only one, must not orphan the answer (B127).
		renamed := cmd
		renamed.Name = "Mynote"
		inst := f.harness.Start(renamed, engine.Params{User: u})
		assert.Equal(t, engine.StateCompleted, inst.State)
		require.Len(t, f.chat.sent, 1)
		assert.Equal(t, "hi", f.chat.sent[0])
	})
}

func TestNeedsUser(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		cmd := f.harness.Command("mynote", f.action())
		inst := f.harness.Start(cmd, engine.Params{Args: []string{"x"}, ArgsText: "x"})
		assert.NotEmpty(t, inst.Errors, "a run without a user fails the action")
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		assert.NoError(t, f.action().Validate())
		a := f.action()
		a.Text = ""
		assert.Error(t, a.Validate(), "the text is required")
		a.Text = action.Template("$allargs")
		a.Kind = ""
		assert.Error(t, a.Validate(), "an unknown kind is invalid")
	})
}
