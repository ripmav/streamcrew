// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/prompt"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

// TestAPIServesTheCounterAndSettings exercises the Counter and Settings
// services through the real ConnectRPC clients (roadmap 6.2.1).
func TestAPIServesTheCounterAndSettings(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	var console bytes.Buffer
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errRun := make(chan error, 1)
	go func() { errRun <- a.Run(ctx) }()
	select {
	case <-a.HTTPServer().Listening():
	case err := <-errRun:
		t.Fatalf("Run returned early: %v", err)
	}

	base := "http://" + a.HTTPServer().Addr().String()
	counterClient := streamcrewv1alpha1connect.NewCounterServiceClient(http.DefaultClient, base)
	settingsClient := streamcrewv1alpha1connect.NewSettingsServiceClient(http.DefaultClient, base)
	name := "apiTestCounter"

	// Counter: create, read, change, delete.
	cr, err := counterClient.Create(t.Context(), connect.NewRequest(&v1alpha1.CreateCounterRequest{Name: name, Value: "10", Step: "2"}))
	require.NoError(t, err)
	assert.Equal(t, "10", cr.Msg.Counter.Value)
	assert.Equal(t, "2", cr.Msg.Counter.Step)

	got, err := counterClient.Get(t.Context(), connect.NewRequest(&v1alpha1.GetCounterRequest{Name: name}))
	require.NoError(t, err)
	assert.Equal(t, "10", got.Msg.Counter.Value)

	add, err := counterClient.Add(t.Context(), connect.NewRequest(&v1alpha1.AddCounterRequest{Name: name}))
	require.NoError(t, err)
	assert.Equal(t, "12", add.Msg.Counter.Value, "empty amount adds one step")

	set, err := counterClient.Set(t.Context(), connect.NewRequest(&v1alpha1.SetCounterRequest{Name: name, Value: "5"}))
	require.NoError(t, err)
	assert.Equal(t, "5", set.Msg.Counter.Value)

	reset, err := counterClient.Reset(t.Context(), connect.NewRequest(&v1alpha1.ResetCounterRequest{Name: name}))
	require.NoError(t, err)
	assert.Equal(t, "0", reset.Msg.Counter.Value)

	list, err := counterClient.List(t.Context(), connect.NewRequest(&v1alpha1.ListCountersRequest{Prefix: name[:3]}))
	require.NoError(t, err)
	assert.NotEmpty(t, list.Msg.Counters, "the prefix filter finds the counter")

	// Update keeps the value, changes the step and the reset-on-start switch.
	up, err := counterClient.Update(t.Context(), connect.NewRequest(&v1alpha1.UpdateCounterRequest{Name: name, Step: "5"}))
	require.NoError(t, err)
	assert.Equal(t, "5", up.Msg.Counter.Step)

	del, err := counterClient.Delete(t.Context(), connect.NewRequest(&v1alpha1.DeleteCounterRequest{Name: name}))
	require.NoError(t, err)
	require.NotNil(t, del.Msg)

	_, err = counterClient.Get(t.Context(), connect.NewRequest(&v1alpha1.GetCounterRequest{Name: name}))
	assert.Error(t, err, "the counter is gone")

	// Settings: the sections are listed, a stored section is read back, and a
	// never-saved section returns its defaults.
	secs, err := settingsClient.ListSections(t.Context(), connect.NewRequest(&v1alpha1.ListSectionsRequest{}))
	require.NoError(t, err)
	assert.NotEmpty(t, secs.Msg.Sections)

	g, err := settingsClient.Get(t.Context(), connect.NewRequest(&v1alpha1.GetSettingsRequest{Name: "chat"}))
	require.NoError(t, err)
	assert.Contains(t, g.Msg.Document, `"type":"chat"`, "a never-saved section returns its defaults")

	u, err := settingsClient.Update(t.Context(), connect.NewRequest(&v1alpha1.UpdateSettingsRequest{Name: "chat", Document: `{"type":"chat","schemaVersion":1,"historySize":7}`}))
	require.NoError(t, err)
	assert.Contains(t, u.Msg.Document, "7")

	g2, err := settingsClient.Get(t.Context(), connect.NewRequest(&v1alpha1.GetSettingsRequest{Name: "chat"}))
	require.NoError(t, err)
	assert.Contains(t, g2.Msg.Document, "7", "the updated section is stored")

	_, err = settingsClient.Update(t.Context(), connect.NewRequest(&v1alpha1.UpdateSettingsRequest{Name: "chat", Document: `{"type":"general","schemaVersion":1}`}))
	assert.Error(t, err, "a document with the wrong type is rejected")
}

// TestAPIServesTheCore starts the core and reads its state through the API
// (roadmap 6.2.1): Status reports the version and the active profile, and
// ListProfiles names the same profile as active.
func TestAPIServesTheCore(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	var console bytes.Buffer
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- a.Run(ctx) }()

	select {
	case <-a.HTTPServer().Listening():
	case err := <-errc:
		t.Fatalf("Run returned early: %v", err)
	}
	base := "http://" + a.HTTPServer().Addr().String()
	client := streamcrewv1alpha1connect.NewSystemServiceClient(http.DefaultClient, base)

	st, err := client.Status(t.Context(), connect.NewRequest(&v1alpha1.StatusRequest{}))
	require.NoError(t, err)
	assert.NotEmpty(t, st.Msg.Version, "the core reports its version")
	assert.NotEmpty(t, st.Msg.Profile, "the core reports its active profile")

	lp, err := client.ListProfiles(t.Context(), connect.NewRequest(&v1alpha1.ListProfilesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, st.Msg.Profile, lp.Msg.Active, "the active profile matches Status")
	seen := false
	for _, p := range lp.Msg.Profiles {
		if p.Name == lp.Msg.Active {
			seen = true
		}
	}
	assert.True(t, seen, "the active profile is in the list")
}

// TestAPIServesTheUserService exercises the UserService through the real
// ConnectRPC client (roadmap 6.2.1): list, get by login and ID, update,
// rankings and merge.
func TestAPIServesTheUserService(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	var console bytes.Buffer
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errRun := make(chan error, 1)
	go func() { errRun <- a.Run(ctx) }()
	select {
	case <-a.HTTPServer().Listening():
	case err := <-errRun:
		t.Fatalf("Run returned early: %v", err)
	}

	// Create two users with chat activity directly in the store.
	st, err := store.Open(ctx, profile.NewManager(cfg.DataDir).Path(profile.DefaultID))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, st.Close()) })
	alice, _, err := st.UpsertIdentity(ctx, user.Identity{Platform: platform.Mock, PlatformUserID: "alice-id", Login: "alice", DisplayName: "Alice"})
	require.NoError(t, err)
	bob, _, err := st.UpsertIdentity(ctx, user.Identity{Platform: platform.Mock, PlatformUserID: "bob-id", Login: "bob", DisplayName: "Bob"})
	require.NoError(t, err)
	require.NoError(t, st.AddStats(ctx, []user.StatsDelta{{UserID: alice.ID, Messages: 5}, {UserID: bob.ID, Messages: 2}}))

	base := "http://" + a.HTTPServer().Addr().String()
	client := streamcrewv1alpha1connect.NewUserServiceClient(http.DefaultClient, base)

	l, err := client.List(ctx, connect.NewRequest(&v1alpha1.ListUsersRequest{}))
	require.NoError(t, err)
	logins := map[string]bool{}
	for _, u := range l.Msg.Users {
		for _, i := range u.Identities {
			logins[i.Login] = true
		}
	}
	assert.True(t, logins["alice"] && logins["bob"], "both created users are listed")

	g, err := client.Get(ctx, connect.NewRequest(&v1alpha1.GetUserRequest{PlatformLogin: "mock/alice"}))
	require.NoError(t, err)
	assert.Equal(t, "alice", g.Msg.User.Identities[0].Login)
	aliceID := g.Msg.User.Id

	gi, err := client.Get(ctx, connect.NewRequest(&v1alpha1.GetUserRequest{Id: aliceID}))
	require.NoError(t, err)
	assert.Equal(t, aliceID, gi.Msg.User.Id)

	three := uint32(3)
	up, err := client.Update(ctx, connect.NewRequest(&v1alpha1.UpdateUserRequest{Id: aliceID, Title: "Queen", Strikes: &three}))
	require.NoError(t, err)
	assert.Equal(t, "Queen", up.Msg.User.Title)
	assert.Equal(t, uint32(3), up.Msg.User.Strikes)

	r, err := client.Rankings(ctx, connect.NewRequest(&v1alpha1.RankingsRequest{}))
	require.NoError(t, err)
	var msgs *v1alpha1.Ranking
	for _, k := range r.Msg.Rankings {
		if k.Stat == "messages" {
			msgs = k
		}
	}
	require.NotNil(t, msgs, "the messages ranking is present")
	require.NotEmpty(t, msgs.Users, "the messages ranking has users")
	assert.EqualValues(t, 5, msgs.Users[0].Value, "alice is on top with 5 messages")

	m, err := client.Merge(ctx, connect.NewRequest(&v1alpha1.MergeUsersRequest{KeepPlatformLogin: "mock/alice", MergePlatformLogin: "mock/bob"}))
	require.NoError(t, err)
	assert.Equal(t, aliceID, m.Msg.Id, "the keep user remains")

	gb, err := client.Get(ctx, connect.NewRequest(&v1alpha1.GetUserRequest{PlatformLogin: "mock/bob"}))
	require.NoError(t, err)
	assert.Equal(t, aliceID, gb.Msg.User.Id, "the merged login now resolves to the keep user")
}

// TestAPIServesTheChatService exercises the ChatService through the real
// ConnectRPC client (roadmap 6.2.1): history, send, delete and clear.
func TestAPIServesTheChatService(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	a, mp, coll := apiCore(t, cfg, settings.DefaultChat())
	ctx := t.Context()

	base := "http://" + a.HTTPServer().Addr().String()
	client := streamcrewv1alpha1connect.NewChatServiceClient(http.DefaultClient, base)

	// A message with a known platform ID lands in the history.
	_, err := mp.SayWithID(ctx, "alice", "hi there", "msg-1")
	require.NoError(t, err)

	h, err := client.History(ctx, connect.NewRequest(&v1alpha1.HistoryChatRequest{}))
	require.NoError(t, err)
	var seen bool
	for _, m := range h.Msg.Messages {
		if m.MessageId == "msg-1" && m.Text == "hi there" {
			seen = true
		}
	}
	assert.True(t, seen, "the message is in the history")

	// Send goes through the chat service to the platform.
	_, err = client.Send(ctx, connect.NewRequest(&v1alpha1.SendChatRequest{Text: "hello from api"}))
	require.NoError(t, err)
	coll.waitFor(t, "the sent message on the platform", func(e event.Envelope) bool { return mockSent(e, "hello from api") })

	// Delete removes a message by its platform ID.
	_, err = client.Delete(ctx, connect.NewRequest(&v1alpha1.DeleteChatRequest{MessageId: "msg-1", Platform: "mock"}))
	require.NoError(t, err)
	coll.waitFor(t, "the delete on the platform", func(e event.Envelope) bool {
		if e.Type != mock.TypeOutput {
			return false
		}
		o, ok := event.Payload[mock.Output](e)
		return ok && o.Op == mock.OpDelete && o.MessageID == "msg-1"
	})

	// Clear empties the channel on the platform.
	_, err = client.Clear(ctx, connect.NewRequest(&v1alpha1.ClearChatRequest{Platform: "mock"}))
	require.NoError(t, err)
	coll.waitFor(t, "the clear on the platform", func(e event.Envelope) bool {
		if e.Type != mock.TypeOutput {
			return false
		}
		o, ok := event.Payload[mock.Output](e)
		return ok && o.Op == mock.OpClearChat
	})
}

// apiCore starts the core with the mock platform and returns the app, the
// platform and a collector for the command instances and the mock outputs.
func apiCore(t *testing.T, cfg config.Config, ch settings.Chat) (*app.App, *mock.Platform, *eventCollector) {
	t.Helper()
	ctx := t.Context()
	newProfileWithSettings(t, cfg, func(svc *settings.Service) {
		require.NoError(t, settings.Save(ctx, svc, ch))
	})
	var mp *mock.Platform
	a, err := app.New(ctx, cfg,
		app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			var err error
			mp, err = mock.New(b.Receiver, mock.WithLogger(b.Logger), mock.WithPublisher(b.Publisher))
			return mp, err
		}),
	)
	require.NoError(t, err)

	coll := &eventCollector{}
	sub := a.Bus().Subscribe(ctx, event.WithPrefixes("command.instance.", "mock."), event.WithBuffer(256))
	go func() {
		for e := range sub.C() {
			coll.add(e)
		}
	}()
	runCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() { errc <- a.Run(runCtx) }()
	require.Eventually(t, a.Ready, 10*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return mp.Status().Connected() }, 5*time.Second, 10*time.Millisecond, "the mock platform to connect")
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errc)
	})
	return a, mp, coll
}

// TestAPIServesTheBackupService exercises the BackupService through the real
// ConnectRPC client (roadmap 6.2.1): create, list and the refused restore
// while the app runs.
func TestAPIServesTheBackupService(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	var console bytes.Buffer
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errRun := make(chan error, 1)
	go func() { errRun <- a.Run(ctx) }()
	select {
	case <-a.HTTPServer().Listening():
	case err := <-errRun:
		t.Fatalf("Run returned early: %v", err)
	}

	base := "http://" + a.HTTPServer().Addr().String()
	client := streamcrewv1alpha1connect.NewBackupServiceClient(http.DefaultClient, base)

	// Create a manual backup.
	c, err := client.Create(ctx, connect.NewRequest(&v1alpha1.CreateBackupRequest{}))
	require.NoError(t, err)
	created := c.Msg.Backup
	assert.NotEmpty(t, created.Id)
	assert.Equal(t, "manual", created.Kind)
	assert.NotZero(t, created.SchemaVersion)

	// List finds the created backup.
	l, err := client.List(ctx, connect.NewRequest(&v1alpha1.ListBackupsRequest{}))
	require.NoError(t, err)
	var found bool
	for _, b := range l.Msg.Backups {
		if b.Id == created.Id {
			found = true
		}
	}
	assert.True(t, found, "the created backup is listed")

	// Restore is refused while the app runs (the profile is open).
	_, err = client.Restore(ctx, connect.NewRequest(&v1alpha1.RestoreBackupRequest{Id: created.Id}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

// TestAPIServesTheAuthService exercises the AuthService through the real
// ConnectRPC client (roadmap 6.2.1): listing accounts, the refused login of
// an unknown platform, and a cancel with no login in progress.
func TestAPIServesTheAuthService(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	var console bytes.Buffer
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errRun := make(chan error, 1)
	go func() { errRun <- a.Run(ctx) }()
	select {
	case <-a.HTTPServer().Listening():
	case err := <-errRun:
		t.Fatalf("Run returned early: %v", err)
	}

	base := "http://" + a.HTTPServer().Addr().String()
	client := streamcrewv1alpha1connect.NewAuthServiceClient(http.DefaultClient, base)

	// ListAccounts succeeds (the fresh profile has no logged-in account).
	l, err := client.ListAccounts(ctx, connect.NewRequest(&v1alpha1.ListAccountsRequest{}))
	require.NoError(t, err)
	assert.Empty(t, l.Msg.Accounts)

	// Starting a login of an unknown platform is refused.
	_, err = client.StartLogin(ctx, connect.NewRequest(&v1alpha1.StartLoginRequest{Platform: "nope"}))
	require.Error(t, err)

	// Canceling a login that is not in progress is a no-op.
	_, err = client.CancelLogin(ctx, connect.NewRequest(&v1alpha1.CancelLoginRequest{Platform: "nope"}))
	require.NoError(t, err)
}

// TestAPIServesTheCommandService exercises the CommandService through the
// real ConnectRPC client (roadmap 6.2.1): create, list, get, export, the
// type catalog, the instance queue and delete.
func TestAPIServesTheCommandService(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	a, _, _ := apiCore(t, cfg, settings.DefaultChat())
	ctx := t.Context()

	base := "http://" + a.HTTPServer().Addr().String()
	client := streamcrewv1alpha1connect.NewCommandServiceClient(http.DefaultClient, base)

	// The fresh profile comes with the preset commands.
	l0, err := client.List(ctx, connect.NewRequest(&v1alpha1.ListCommandsRequest{}))
	require.NoError(t, err)
	count0 := len(l0.Msg.Commands)
	assert.NotEmpty(t, l0.Msg.Commands)

	// Create a chat command from its document.
	doc := "apiVersion: streamcrew/v1alpha1\n" +
		"kind: ChatCommand\n" +
		"metadata: {name: hug}\n" +
		"spec:\n" +
		"  triggers: [hug]\n" +
		"  triggerMode: exclamation\n" +
		"  errorPolicy: abort\n" +
		"  actions:\n" +
		"    - {type: chat, kind: message, message: \"hi\"}\n"
	cr, err := client.Create(ctx, connect.NewRequest(&v1alpha1.CreateCommandRequest{Document: doc, Format: "yaml"}))
	require.NoError(t, err)
	assert.Equal(t, "hug", cr.Msg.Command.Name)
	id := cr.Msg.Command.Id
	assert.NotEmpty(t, id)

	// The prefix filter finds the new command, and the list grew by one.
	l1, err := client.List(ctx, connect.NewRequest(&v1alpha1.ListCommandsRequest{Prefix: "hu"}))
	require.NoError(t, err)
	assert.Len(t, l1.Msg.Commands, 1)
	assert.Equal(t, "hug", l1.Msg.Commands[0].Name)
	all, err := client.List(ctx, connect.NewRequest(&v1alpha1.ListCommandsRequest{}))
	require.NoError(t, err)
	assert.Len(t, all.Msg.Commands, count0+1)

	// Get returns the command and its document.
	g, err := client.Get(ctx, connect.NewRequest(&v1alpha1.GetCommandRequest{Id: id}))
	require.NoError(t, err)
	assert.Equal(t, "hug", g.Msg.Summary.Name)
	assert.NotEmpty(t, g.Msg.Document)
	assert.Equal(t, "yaml", g.Msg.Format)

	// Export writes the commands to a document.
	ex, err := client.Export(ctx, connect.NewRequest(&v1alpha1.ExportCommandsRequest{Format: "yaml"}))
	require.NoError(t, err)
	assert.Contains(t, ex.Msg.Document, "hug")

	// TypeCatalog lists the action types.
	tc, err := client.TypeCatalog(ctx, connect.NewRequest(&v1alpha1.TypeCatalogRequest{}))
	require.NoError(t, err)
	assert.NotEmpty(t, tc.Msg.Actions)

	// The instance queue is not paused at first.
	li, err := client.ListInstances(ctx, connect.NewRequest(&v1alpha1.ListInstancesRequest{}))
	require.NoError(t, err)
	assert.False(t, li.Msg.Paused)

	// Pause and resume the queue.
	pq, err := client.PauseQueue(ctx, connect.NewRequest(&v1alpha1.PauseQueueRequest{Pause: true}))
	require.NoError(t, err)
	assert.True(t, pq.Msg.Paused)
	_, err = client.PauseQueue(ctx, connect.NewRequest(&v1alpha1.PauseQueueRequest{Pause: false}))
	require.NoError(t, err)

	// Delete removes the command; the list is back to the presets.
	_, err = client.Delete(ctx, connect.NewRequest(&v1alpha1.DeleteCommandRequest{Id: id}))
	require.NoError(t, err)
	l2, err := client.List(ctx, connect.NewRequest(&v1alpha1.ListCommandsRequest{Prefix: "hu"}))
	require.NoError(t, err)
	assert.Empty(t, l2.Msg.Commands)
}

// TestAPIServesTheStreamService exercises the StreamService through the real
// ConnectRPC client (roadmap 6.2.1): a chat message on the stream, a prompt
// delivered through the stream, the refused answer of an unknown prompt and
// the since catch-up for a reconnecting client.
func TestAPIServesTheStreamService(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	a, mp, _ := apiCore(t, cfg, settings.DefaultChat())
	ctx := t.Context()

	base := "http://" + a.HTTPServer().Addr().String()
	client := streamcrewv1alpha1connect.NewStreamServiceClient(http.DefaultClient, base)

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.Subscribe(subCtx, connect.NewRequest(&v1alpha1.SubscribeRequest{}))
	require.NoError(t, err)

	// A chat message arrives on the stream.
	_, err = mp.Say(ctx, "alice", "hello stream")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		if !stream.Receive() {
			return false
		}
		return stream.Msg().GetChat() != nil && stream.Msg().GetChat().GetText() == "hello stream"
	}, 5*time.Second, 10*time.Millisecond, "the chat message on the stream")

	// A prompt is delivered through the stream.
	p := prompt.Prompt{ID: "test-prompt", Kind: prompt.KindConfirmation, Text: "a prompt"}
	require.NoError(t, a.Bus().Publish(ctx, event.New(event.Source{Kind: event.SourceSystem, Name: "prompt"}, prompt.Type, p)))
	require.Eventually(t, func() bool {
		if !stream.Receive() {
			return false
		}
		return stream.Msg().GetPrompt() != nil && stream.Msg().GetPrompt().GetId() == "test-prompt"
	}, 5*time.Second, 10*time.Millisecond, "the prompt on the stream")

	// Answering a prompt that is not pending is refused.
	_, err = client.AnswerPrompt(ctx, connect.NewRequest(&v1alpha1.AnswerPromptRequest{Id: "test-prompt"}))
	require.Error(t, err)

	// A reconnecting client catches up with the since buffer.
	catchup, err := client.Subscribe(ctx, connect.NewRequest(&v1alpha1.SubscribeRequest{Since: 10}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		if !catchup.Receive() {
			return false
		}
		return catchup.Msg().GetChat() != nil && catchup.Msg().GetChat().GetText() == "hello stream"
	}, 5*time.Second, 10*time.Millisecond, "the catch-up of the chat message")
}
