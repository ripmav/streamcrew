// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/apitoken"
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

	base := "http://" + mustServerAddr(t, a)
	counterClient := streamcrewv1alpha1connect.NewCounterServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))
	settingsClient := streamcrewv1alpha1connect.NewSettingsServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))
	name := "apiTestCounter"

	// Counter: create, read, change, delete.
	cr, err := counterClient.Create(t.Context(), &v1alpha1.CreateCounterRequest{Name: name, Value: "10", Step: "2"})
	require.NoError(t, err)
	assert.Equal(t, "10", cr.Counter.Value)
	assert.Equal(t, "2", cr.Counter.Step)

	got, err := counterClient.Get(t.Context(), &v1alpha1.GetCounterRequest{Name: name})
	require.NoError(t, err)
	assert.Equal(t, "10", got.Counter.Value)

	add, err := counterClient.Add(t.Context(), &v1alpha1.AddCounterRequest{Name: name})
	require.NoError(t, err)
	assert.Equal(t, "12", add.Counter.Value, "empty amount adds one step")

	set, err := counterClient.Set(t.Context(), &v1alpha1.SetCounterRequest{Name: name, Value: "5"})
	require.NoError(t, err)
	assert.Equal(t, "5", set.Counter.Value)

	reset, err := counterClient.Reset(t.Context(), &v1alpha1.ResetCounterRequest{Name: name})
	require.NoError(t, err)
	assert.Equal(t, "0", reset.Counter.Value)

	list, err := counterClient.List(t.Context(), &v1alpha1.ListCountersRequest{Prefix: name[:3]})
	require.NoError(t, err)
	assert.NotEmpty(t, list.Counters, "the prefix filter finds the counter")

	// Update keeps the value, changes the step and the reset-on-start switch.
	up, err := counterClient.Update(t.Context(), &v1alpha1.UpdateCounterRequest{Name: name, Step: "5"})
	require.NoError(t, err)
	assert.Equal(t, "5", up.Counter.Step)

	del, err := counterClient.Delete(t.Context(), &v1alpha1.DeleteCounterRequest{Name: name})
	require.NoError(t, err)
	require.NotNil(t, del)

	_, err = counterClient.Get(t.Context(), &v1alpha1.GetCounterRequest{Name: name})
	assert.Error(t, err, "the counter is gone")

	// Settings: the sections are listed, a stored section is read back, and a
	// never-saved section returns its defaults.
	secs, err := settingsClient.ListSections(t.Context(), &v1alpha1.ListSectionsRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, secs.Sections)

	g, err := settingsClient.Get(t.Context(), &v1alpha1.GetSettingsRequest{Name: "chat"})
	require.NoError(t, err)
	assert.Contains(t, g.Document, `"type":"chat"`, "a never-saved section returns its defaults")

	u, err := settingsClient.Update(t.Context(), &v1alpha1.UpdateSettingsRequest{Name: "chat", Document: `{"type":"chat","schemaVersion":1,"historySize":7}`})
	require.NoError(t, err)
	assert.Contains(t, u.Document, "7")

	g2, err := settingsClient.Get(t.Context(), &v1alpha1.GetSettingsRequest{Name: "chat"})
	require.NoError(t, err)
	assert.Contains(t, g2.Document, "7", "the updated section is stored")

	_, err = settingsClient.Update(t.Context(), &v1alpha1.UpdateSettingsRequest{Name: "chat", Document: `{"type":"general","schemaVersion":1}`})
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
	base := "http://" + mustServerAddr(t, a)
	client := streamcrewv1alpha1connect.NewSystemServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))

	st, err := client.Status(t.Context(), &v1alpha1.StatusRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, st.Version, "the core reports its version")
	assert.NotEmpty(t, st.Profile, "the core reports its active profile")

	lp, err := client.ListProfiles(t.Context(), &v1alpha1.ListProfilesRequest{})
	require.NoError(t, err)
	assert.Equal(t, st.Profile, lp.Active, "the active profile matches Status")
	seen := false
	for _, p := range lp.Profiles {
		if p.Name == lp.Active {
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

	base := "http://" + mustServerAddr(t, a)
	client := streamcrewv1alpha1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))

	l, err := client.List(ctx, &v1alpha1.ListUsersRequest{})
	require.NoError(t, err)
	logins := map[string]bool{}
	for _, u := range l.Users {
		for _, i := range u.Identities {
			logins[i.Login] = true
		}
	}
	assert.True(t, logins["alice"] && logins["bob"], "both created users are listed")

	g, err := client.Get(ctx, &v1alpha1.GetUserRequest{PlatformLogin: "mock/alice"})
	require.NoError(t, err)
	assert.Equal(t, "alice", g.User.Identities[0].Login)
	aliceID := g.User.Id

	gi, err := client.Get(ctx, &v1alpha1.GetUserRequest{Id: aliceID})
	require.NoError(t, err)
	assert.Equal(t, aliceID, gi.User.Id)

	three := uint32(3)
	up, err := client.Update(ctx, &v1alpha1.UpdateUserRequest{Id: aliceID, Title: "Queen", Strikes: &three})
	require.NoError(t, err)
	assert.Equal(t, "Queen", up.User.Title)
	assert.Equal(t, uint32(3), up.User.Strikes)

	r, err := client.Rankings(ctx, &v1alpha1.RankingsRequest{})
	require.NoError(t, err)
	var msgs *v1alpha1.Ranking
	for _, k := range r.Rankings {
		if k.Stat == "messages" {
			msgs = k
		}
	}
	require.NotNil(t, msgs, "the messages ranking is present")
	require.NotEmpty(t, msgs.Users, "the messages ranking has users")
	assert.EqualValues(t, 5, msgs.Users[0].Value, "alice is on top with 5 messages")

	m, err := client.Merge(ctx, &v1alpha1.MergeUsersRequest{KeepPlatformLogin: "mock/alice", MergePlatformLogin: "mock/bob"})
	require.NoError(t, err)
	assert.Equal(t, aliceID, m.Id, "the keep user remains")

	gb, err := client.Get(ctx, &v1alpha1.GetUserRequest{PlatformLogin: "mock/bob"})
	require.NoError(t, err)
	assert.Equal(t, aliceID, gb.User.Id, "the merged login now resolves to the keep user")
}

// TestAPIServesTheChatService exercises the ChatService through the real
// ConnectRPC client (roadmap 6.2.1): history, send, delete and clear.
func TestAPIServesTheChatService(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	a, mp, coll := apiCore(t, cfg, settings.DefaultChat())
	ctx := t.Context()

	base := "http://" + mustServerAddr(t, a)
	client := streamcrewv1alpha1connect.NewChatServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))

	// A message with a known platform ID lands in the history.
	_, err := mp.SayWithID(ctx, "alice", "hi there", "msg-1")
	require.NoError(t, err)

	h, err := client.History(ctx, &v1alpha1.HistoryChatRequest{})
	require.NoError(t, err)
	var seen bool
	for _, m := range h.Messages {
		if m.MessageId == "msg-1" && m.Text == "hi there" {
			seen = true
		}
	}
	assert.True(t, seen, "the message is in the history")

	// Send goes through the chat service to the platform.
	_, err = client.Send(ctx, &v1alpha1.SendChatRequest{Text: "hello from api"})
	require.NoError(t, err)
	coll.waitFor(t, "the sent message on the platform", func(e event.Envelope) bool { return mockSent(e, "hello from api") })

	// Delete removes a message by its platform ID.
	_, err = client.Delete(ctx, &v1alpha1.DeleteChatRequest{MessageId: "msg-1", Platform: "mock"})
	require.NoError(t, err)
	coll.waitFor(t, "the delete on the platform", func(e event.Envelope) bool {
		if e.Type != mock.TypeOutput {
			return false
		}
		o, ok := event.Payload[mock.Output](e)
		return ok && o.Op == mock.OpDelete && o.MessageID == "msg-1"
	})

	// Clear empties the channel on the platform.
	_, err = client.Clear(ctx, &v1alpha1.ClearChatRequest{Platform: "mock"})
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

	base := "http://" + mustServerAddr(t, a)
	client := streamcrewv1alpha1connect.NewBackupServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))

	// Create a manual backup.
	c, err := client.Create(ctx, &v1alpha1.CreateBackupRequest{})
	require.NoError(t, err)
	created := c.Backup
	assert.NotEmpty(t, created.Id)
	assert.Equal(t, "manual", created.Kind)
	assert.NotZero(t, created.SchemaVersion)

	// List finds the created backup.
	l, err := client.List(ctx, &v1alpha1.ListBackupsRequest{})
	require.NoError(t, err)
	var found bool
	for _, b := range l.Backups {
		if b.Id == created.Id {
			found = true
		}
	}
	assert.True(t, found, "the created backup is listed")

	// Restore is refused while the app runs (the profile is open).
	_, err = client.Restore(ctx, &v1alpha1.RestoreBackupRequest{Id: created.Id})
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

	base := "http://" + mustServerAddr(t, a)
	client := streamcrewv1alpha1connect.NewAuthServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))

	// ListAccounts succeeds (the fresh profile has no logged-in account).
	l, err := client.ListAccounts(ctx, &v1alpha1.ListAccountsRequest{})
	require.NoError(t, err)
	assert.Empty(t, l.Accounts)

	// Starting a login of an unknown platform is refused.
	_, err = client.StartLogin(ctx, &v1alpha1.StartLoginRequest{Platform: "nope"})
	require.Error(t, err)

	// Canceling a login that is not in progress is a no-op.
	_, err = client.CancelLogin(ctx, &v1alpha1.CancelLoginRequest{Platform: "nope"})
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

	base := "http://" + mustServerAddr(t, a)
	client := streamcrewv1alpha1connect.NewCommandServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))

	// The fresh profile comes with the preset commands.
	l0, err := client.List(ctx, &v1alpha1.ListCommandsRequest{})
	require.NoError(t, err)
	count0 := len(l0.Commands)
	assert.NotEmpty(t, l0.Commands)

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
	cr, err := client.Create(ctx, &v1alpha1.CreateCommandRequest{Document: doc, Format: "yaml"})
	require.NoError(t, err)
	assert.Equal(t, "hug", cr.Command.Name)
	id := cr.Command.Id
	assert.NotEmpty(t, id)

	// The prefix filter finds the new command, and the list grew by one.
	l1, err := client.List(ctx, &v1alpha1.ListCommandsRequest{Prefix: "hu"})
	require.NoError(t, err)
	assert.Len(t, l1.Commands, 1)
	assert.Equal(t, "hug", l1.Commands[0].Name)
	all, err := client.List(ctx, &v1alpha1.ListCommandsRequest{})
	require.NoError(t, err)
	assert.Len(t, all.Commands, count0+1)

	// Get returns the command and its document.
	g, err := client.Get(ctx, &v1alpha1.GetCommandRequest{Id: id})
	require.NoError(t, err)
	assert.Equal(t, "hug", g.Summary.Name)
	assert.NotEmpty(t, g.Document)
	assert.Equal(t, "yaml", g.Format)

	// Export writes the commands to a document.
	ex, err := client.Export(ctx, &v1alpha1.ExportCommandsRequest{Format: "yaml"})
	require.NoError(t, err)
	assert.Contains(t, ex.Document, "hug")

	// TypeCatalog lists the action types.
	tc, err := client.TypeCatalog(ctx, &v1alpha1.TypeCatalogRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, tc.Actions)

	// The instance queue is not paused at first.
	li, err := client.ListInstances(ctx, &v1alpha1.ListInstancesRequest{})
	require.NoError(t, err)
	assert.False(t, li.Paused)

	// Pause and resume the queue.
	pq, err := client.PauseQueue(ctx, &v1alpha1.PauseQueueRequest{Pause: true})
	require.NoError(t, err)
	assert.True(t, pq.Paused)
	_, err = client.PauseQueue(ctx, &v1alpha1.PauseQueueRequest{Pause: false})
	require.NoError(t, err)

	// Delete removes the command; the list is back to the presets.
	_, err = client.Delete(ctx, &v1alpha1.DeleteCommandRequest{Id: id})
	require.NoError(t, err)
	l2, err := client.List(ctx, &v1alpha1.ListCommandsRequest{Prefix: "hu"})
	require.NoError(t, err)
	assert.Empty(t, l2.Commands)
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

	base := "http://" + mustServerAddr(t, a)
	client := streamcrewv1alpha1connect.NewStreamServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.Subscribe(subCtx, &v1alpha1.SubscribeRequest{})
	require.NoError(t, err)

	// A chat message arrives on the stream.
	_, err = mp.Say(ctx, "alice", "hello stream")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		event, err := stream.Receive()
		return err == nil && event.GetChat() != nil && event.GetChat().GetText() == "hello stream"
	}, 5*time.Second, 10*time.Millisecond, "the chat message on the stream")

	// A prompt is delivered through the stream.
	p := prompt.Prompt{ID: "test-prompt", Kind: prompt.KindConfirmation, Text: "a prompt"}
	require.NoError(t, a.Bus().Publish(ctx, event.New(event.Source{Kind: event.SourceSystem, Name: "prompt"}, prompt.Type, p)))
	require.Eventually(t, func() bool {
		event, err := stream.Receive()
		return err == nil && event.GetPrompt() != nil && event.GetPrompt().GetId() == "test-prompt"
	}, 5*time.Second, 10*time.Millisecond, "the prompt on the stream")

	// Answering a prompt that is not pending is refused.
	_, err = client.AnswerPrompt(ctx, &v1alpha1.AnswerPromptRequest{Id: "test-prompt"})
	require.Error(t, err)

	// A reconnecting client catches up with the since buffer.
	catchup, err := client.Subscribe(ctx, &v1alpha1.SubscribeRequest{Since: 10})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		event, err := catchup.Receive()
		return err == nil && event.GetChat() != nil && event.GetChat().GetText() == "hello stream"
	}, 5*time.Second, 10*time.Millisecond, "the catch-up of the chat message")
}

// TestAPIServesSetEnabled covers the SetEnabled RPC of the CommandService
// (roadmap 6.3): the active switch flips, and an unknown ID is an error.
func TestAPIServesSetEnabled(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	a, _, _ := apiCore(t, cfg, settings.DefaultChat())
	ctx := t.Context()

	base := "http://" + mustServerAddr(t, a)
	client := streamcrewv1alpha1connect.NewCommandServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))

	l0, err := client.List(ctx, &v1alpha1.ListCommandsRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, l0.Commands)
	id, enabled := l0.Commands[0].Id, l0.Commands[0].Enabled

	resp, err := client.SetEnabled(ctx, &v1alpha1.SetEnabledCommandRequest{Id: id, Enabled: !enabled})
	require.NoError(t, err)
	assert.Equal(t, !enabled, resp.Command.Enabled)

	_, err = client.SetEnabled(ctx, &v1alpha1.SetEnabledCommandRequest{Id: "no-such-id"})
	require.Error(t, err)
}

// TestAPIServesSimulate covers the Simulate RPC of the StreamService
// (roadmap 6.3): a chat message of a user and a platform-neutral event go
// through the mock platform like the mock console does.
func TestAPIServesSimulate(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	a, _, _ := apiCore(t, cfg, settings.DefaultChat())
	ctx := t.Context()

	base := "http://" + mustServerAddr(t, a)
	client := streamcrewv1alpha1connect.NewStreamServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))
	chat := streamcrewv1alpha1connect.NewChatServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))
	follows := a.Bus().Subscribe(ctx, event.WithPrefixes("channel."))

	// A chat message of a user appears in the chat history.
	resp, err := client.Simulate(ctx, &v1alpha1.SimulateEventRequest{
		EventType: "chat.message",
		User:      "alice",
		Text:      "hello from the API",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.EventId)
	require.Eventually(t, func() bool {
		h, err := chat.History(ctx, &v1alpha1.HistoryChatRequest{})
		return err == nil && len(h.Messages) > 0 && h.Messages[0].Login == "alice" && h.Messages[0].Text == "hello from the API"
	}, 5*time.Second, 10*time.Millisecond, "the simulated chat message in the history")

	// A platform-neutral event fires on the bus.
	_, err = client.Simulate(ctx, &v1alpha1.SimulateEventRequest{
		EventType: "channel.follow",
		User:      "dave",
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		select {
		case e := <-follows.C():
			return e.Type == "channel.follow"
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond, "the simulated follow on the bus")

	// A chat message without a user is refused.
	_, err = client.Simulate(ctx, &v1alpha1.SimulateEventRequest{
		EventType: "chat.message",
		Text:      "no user",
	})
	require.Error(t, err)

	// A type the mock platform does not simulate is refused.
	_, err = client.Simulate(ctx, &v1alpha1.SimulateEventRequest{
		EventType: "twitch.channel.follow",
	})
	require.Error(t, err)
}

// withAuth returns the client interceptor that sends the bearer token of
// value with every call.
func withAuth(value string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", "Bearer "+value)
			}
			return next(ctx, spec)
		}
	}
}

// TestAPITokensProtectTheAPI covers the token access control (roadmap
// 6.2.2): without tokens the API is open; with tokens a valid bearer token
// with the required scope is mandatory.
func TestAPITokensProtectTheAPI(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	a, _, _ := apiCore(t, cfg, settings.DefaultChat())
	ctx := t.Context()

	base := "http://" + mustServerAddr(t, a)

	// Without tokens the API is open.
	open := streamcrewv1alpha1connect.NewCounterServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))
	_, err := open.List(ctx, &v1alpha1.ListCountersRequest{})
	require.NoError(t, err)

	// With tokens the API is protected.
	s := apitoken.Open(cfg.DataDir)
	_, readValue, err := s.Create("read", []apitoken.Scope{apitoken.Read})
	require.NoError(t, err)
	_, adminValue, err := s.Create("admin", []apitoken.Scope{apitoken.Admin})
	require.NoError(t, err)
	_, overlayValue, err := s.Create("overlay", []apitoken.Scope{apitoken.Overlay})
	require.NoError(t, err)

	// Without or with an invalid token: unauthenticated.
	_, err = open.List(ctx, &v1alpha1.ListCountersRequest{})
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	bogus := streamcrewv1alpha1connect.NewCounterServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0)), withAuth("sc_bogus")))
	_, err = bogus.List(ctx, &v1alpha1.ListCountersRequest{})
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// A read token can read, but not create.
	read := streamcrewv1alpha1connect.NewCounterServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0)), withAuth(readValue)))
	_, err = read.List(ctx, &v1alpha1.ListCountersRequest{})
	require.NoError(t, err)
	_, err = read.Create(ctx, &v1alpha1.CreateCounterRequest{Name: "hugs"})
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// An admin token can create.
	admin := streamcrewv1alpha1connect.NewCounterServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0)), withAuth(adminValue)))
	_, err = admin.Create(ctx, &v1alpha1.CreateCounterRequest{Name: "hugs"})
	require.NoError(t, err)

	// An overlay token can read, but not control.
	overlay := streamcrewv1alpha1connect.NewChatServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0)), withAuth(overlayValue)))
	_, err = overlay.History(ctx, &v1alpha1.HistoryChatRequest{})
	require.NoError(t, err)
	_, err = overlay.Send(ctx, &v1alpha1.SendChatRequest{Text: "nope"})
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}
