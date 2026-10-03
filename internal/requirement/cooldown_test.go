// SPDX-License-Identifier: Apache-2.0

package requirement_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/requirement"
	"github.com/ripmav/streamcrew/internal/store"
)

var _ requirement.Cooldowns = (*store.Store)(nil)

// cooldownFor returns a cooldown requirement of the scope; the grouped
// scopes name group, the others last d.
func cooldownFor(scope command.CooldownScope, d time.Duration, group id.ID) command.CooldownRequirement {
	if scope.Grouped() {
		return command.CooldownRequirement{Scope: scope, Group: group}
	}
	return command.CooldownRequirement{Scope: scope, Duration: polydoc.Duration(d)}
}

// onCooldown returns the rejection of a running cooldown.
func onCooldown(key i18n.Key, left time.Duration, tell bool) engine.Decision {
	return engine.Rejected(engine.Rejection{
		Requirement: command.TypeCooldown,
		Reason:      i18n.Message{Key: key, Args: map[string]i18n.Value{"remaining": i18n.Duration(left)}},
		Tell:        tell,
	})
}

// verdict applies the requirements of c for p and returns the verdict.
func (f *fixture) verdict(t *testing.T, c command.Command, p engine.Params) engine.Verdict {
	t.Helper()
	d, err := apply(t.Context(), f.service, c, p)
	require.NoError(t, err)
	return d.Verdict
}

// TestCooldownScopes covers B20, B21, B24 and B101: each scope blocks what
// it says, as long as its requirement or its cooldown group says, and the
// message names the remaining time, for the scopes per user to the user.
func TestCooldownScopes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sounds := command.CooldownGroup{ID: id.New(), Name: "Sounds", Duration: 2 * time.Minute}
		f := newFixture(t, language{lang: i18n.English})
		f.cooldowns.groups = []command.CooldownGroup{sounds}
		ada, bob := person("ada", platform.Twitch), person("bob", platform.Twitch)

		standard := cmd(cooldownFor(command.CooldownStandard, 90*time.Second, id.ID{}))
		assert.Equal(t, engine.VerdictMet, f.verdict(t, standard, chat(ada)))
		time.Sleep(29*time.Second + 500*time.Millisecond)
		got, err := apply(t.Context(), f.service, standard, chat(bob))
		require.NoError(t, err)
		assert.Equal(t, onCooldown(i18n.KeyRequirementCooldownAll, 61*time.Second, true), got, "for everyone, 60.5 s rounded up")
		got, err = apply(t.Context(), f.service, standard, engine.Params{Platform: platform.Twitch, User: bob})
		require.NoError(t, err)
		assert.Equal(t, onCooldown(i18n.KeyRequirementCooldownAll, 61*time.Second, false), got, "not told without a chat message")
		time.Sleep(time.Minute + 500*time.Millisecond)
		assert.Equal(t, engine.VerdictMet, f.verdict(t, standard, chat(bob)), "ended")

		perUser := cmd(cooldownFor(command.CooldownPerUser, time.Minute, id.ID{}))
		assert.Equal(t, engine.VerdictMet, f.verdict(t, perUser, chat(ada)))
		got, err = apply(t.Context(), f.service, perUser, chat(ada))
		require.NoError(t, err)
		assert.Equal(t, onCooldown(i18n.KeyRequirementCooldownUser, time.Minute, true), got, "for the user")
		assert.Equal(t, engine.VerdictMet, f.verdict(t, perUser, chat(bob)), "others are free")

		boom := cmd(cooldownFor(command.CooldownGrouped, 0, sounds.ID))
		bang := cmd(cooldownFor(command.CooldownGrouped, 0, sounds.ID))
		assert.Equal(t, engine.VerdictMet, f.verdict(t, boom, chat(ada)))
		got, err = apply(t.Context(), f.service, bang, chat(bob))
		require.NoError(t, err)
		assert.Equal(t, onCooldown(i18n.KeyRequirementCooldownAll, 2*time.Minute, true), got, "the group shares it, as long as the group says")

		mine := cmd(cooldownFor(command.CooldownPerUserGrouped, 0, sounds.ID))
		yours := cmd(cooldownFor(command.CooldownPerUserGrouped, 0, sounds.ID))
		assert.Equal(t, engine.VerdictMet, f.verdict(t, mine, chat(ada)), "per user group is a cooldown of its own")
		got, err = apply(t.Context(), f.service, yours, chat(ada))
		require.NoError(t, err)
		assert.Equal(t, onCooldown(i18n.KeyRequirementCooldownUser, 2*time.Minute, true), got)
		assert.Equal(t, engine.VerdictMet, f.verdict(t, yours, chat(bob)))

		mods := cmd(command.RoleRequirement{Role: role.Moderator}, cooldownFor(command.CooldownGrouped, 0, sounds.ID))
		got, err = apply(t.Context(), f.service, mods, chat(ada))
		require.NoError(t, err)
		assert.Equal(t, rejection(role.Moderator, true), got, "B101: the role comes first")
	})
}

// TestCooldownKeepsItsEnd covers B23 and B102: a new duration does not
// change a running cooldown, neither of a command nor of a cooldown group;
// it applies to the next one.
func TestCooldownKeepsItsEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sounds := command.CooldownGroup{ID: id.New(), Name: "Sounds", Duration: time.Minute}
		f := newFixture(t, language{lang: i18n.English})
		f.cooldowns.groups = []command.CooldownGroup{sounds}
		ada := chat(person("ada", platform.Twitch))

		c := cmd(cooldownFor(command.CooldownStandard, time.Minute, id.ID{}))
		grouped := cmd(cooldownFor(command.CooldownGrouped, 0, sounds.ID))
		assert.Equal(t, engine.VerdictMet, f.verdict(t, c, ada))
		assert.Equal(t, engine.VerdictMet, f.verdict(t, grouped, ada))
		time.Sleep(10 * time.Second)
		c.Requirements = []command.Requirement{cooldownFor(command.CooldownStandard, 10*time.Second, id.ID{})}
		f.cooldowns.mu.Lock()
		f.cooldowns.groups[0].Duration = 10 * time.Second
		f.cooldowns.mu.Unlock()
		for _, x := range []command.Command{c, grouped} {
			got, err := apply(t.Context(), f.service, x, ada)
			require.NoError(t, err)
			assert.Equal(t, onCooldown(i18n.KeyRequirementCooldownAll, 50*time.Second, true), got)
		}
		time.Sleep(50 * time.Second)
		assert.Equal(t, engine.VerdictMet, f.verdict(t, c, ada))
		assert.Equal(t, engine.VerdictMet, f.verdict(t, grouped, ada))
		time.Sleep(9 * time.Second)
		assert.Equal(t, engine.VerdictRejected, f.verdict(t, c, ada), "the new duration for the next one")
		assert.Equal(t, engine.VerdictRejected, f.verdict(t, grouped, ada))
		time.Sleep(time.Second)
		assert.Equal(t, engine.VerdictMet, f.verdict(t, c, ada))
	})
}

// TestCooldownStreamer covers B4 and B22: a run without a user counts
// against the cooldown per user of the streamer on its platform, without a
// platform on Twitch, and so does the streamer's own chat message; linked
// accounts share one cooldown.
func TestCooldownStreamer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, language{lang: i18n.English})
		c := cmd(cooldownFor(command.CooldownPerUser, time.Minute, id.ID{}))
		timer := engine.Params{}
		onKick := engine.Params{Platform: platform.Kick}

		assert.Equal(t, engine.VerdictMet, f.verdict(t, c, timer))
		assert.Equal(t, engine.VerdictRejected, f.verdict(t, c, engine.Params{Platform: platform.Twitch}), "the streamer on Twitch")
		streamerChat := chat(&user.User{ID: f.streamer[platform.Twitch], Identities: []user.Identity{{Platform: platform.Twitch, Login: "me", Roles: role.NewSet(role.Streamer)}}})
		assert.Equal(t, engine.VerdictRejected, f.verdict(t, c, streamerChat), "the streamer's own message")
		assert.Equal(t, engine.VerdictMet, f.verdict(t, c, onKick), "the streamer on Kick is another user")
		assert.Contains(t, f.cooldowns.running(), command.CooldownKey{Command: c.ID, User: f.streamer[platform.Kick]})

		linked := person("ada", platform.Twitch)
		linked.Identities = append(linked.Identities, user.Identity{Platform: platform.Kick, PlatformUserID: "k-ada", Login: "ada"})
		assert.Equal(t, engine.VerdictMet, f.verdict(t, c, chat(linked)))
		assert.Equal(t, engine.VerdictRejected, f.verdict(t, c, engine.Params{Platform: platform.Kick, User: linked, Message: "!hug"}),
			"one user on both platforms")

		forEveryone := cmd(cooldownFor(command.CooldownStandard, time.Minute, id.ID{}))
		delete(f.streamer, platform.Twitch)
		assert.Equal(t, engine.VerdictMet, f.verdict(t, forEveryone, timer), "only the scopes per user need the streamer")
		_, err := apply(t.Context(), f.service, cmd(cooldownFor(command.CooldownPerUser, time.Minute, id.ID{})), timer)
		require.ErrorContains(t, err, "no streamer on twitch")
	})
}

// TestCooldownFaulty covers B7 and B103: a grouped cooldown without a
// cooldown group or with one that does not exist rejects the command
// before anything else, without telling the user.
func TestCooldownFaulty(t *testing.T) {
	t.Parallel()
	sounds := command.CooldownGroup{ID: id.New(), Name: "Sounds", Duration: time.Minute}
	for name, tc := range map[string]struct {
		group id.ID
		why   string
	}{
		"no cooldown group":      {id.ID{}, "the cooldown names no cooldown group"},
		"a deleted cooldown one": {id.New(), "the cooldown group does not exist"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, language{lang: i18n.English})
			f.cooldowns.groups = []command.CooldownGroup{sounds}
			c := cmd(command.RoleRequirement{Role: role.Moderator}, command.CooldownRequirement{Scope: command.CooldownPerUserGrouped, Group: tc.group})
			got, err := apply(t.Context(), f.service, c, chat(person("ada", platform.Twitch)))
			require.NoError(t, err)
			assert.Equal(t, engine.Rejected(engine.Rejection{Requirement: command.TypeCooldown, Reason: i18n.Message{Key: i18n.KeyRequirementFaulty}}), got)
			assert.Contains(t, f.logs.String(), "command not run: faulty requirement")
			assert.Contains(t, f.logs.String(), tc.why)
			assert.Empty(t, f.cooldowns.running())
		})
	}
}

// TestCooldownBeforeUnsupported: a running cooldown rejects before the
// requirements the service cannot decide yet.
func TestCooldownBeforeUnsupported(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	c := cmd(cooldownFor(command.CooldownStandard, time.Minute, id.ID{}))
	require.NoError(t, f.service.StartCooldown(t.Context(), c, engine.Params{}))
	c.Requirements = append(c.Requirements, command.ThresholdRequirement{Users: 2, Within: 1})
	assert.Equal(t, engine.VerdictRejected, f.verdict(t, c, chat(person("ada", platform.Twitch))))
}

// TestCooldownOnce covers B3 and B100: the same user triggers a command
// with a cooldown per user many times at once; exactly one run passes.
// Decisions, starts by the action and taking back happen one at a time.
func TestCooldownOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	slow := &slowCooldowns{cooldowns: f.cooldowns}
	svc := f.withCooldowns(t, slow)
	c := cmd(cooldownFor(command.CooldownPerUser, time.Minute, id.ID{}))
	ada := chat(person("ada", platform.Twitch))
	earlier, err := apply(t.Context(), svc, c, chat(person("carl", platform.Twitch)))
	require.NoError(t, err)
	require.NotNil(t, earlier.Revert)

	const runs = 16
	verdicts := make(chan engine.Verdict, runs)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range runs {
		wg.Go(func() {
			<-start
			d, err := apply(t.Context(), svc, c, ada)
			assert.NoError(t, err)
			verdicts <- d.Verdict
		})
		wg.Go(func() {
			<-start
			assert.NoError(t, svc.StartCooldown(t.Context(), c, chat(person("bob", platform.Twitch))))
		})
		wg.Go(func() {
			<-start
			assert.NoError(t, earlier.Revert(t.Context()))
		})
	}
	close(start)
	wg.Wait()
	close(verdicts)
	met := 0
	for v := range verdicts {
		if v == engine.VerdictMet {
			met++
		}
	}
	assert.Equal(t, 1, met)
	assert.False(t, slow.overlapped(), "one decision at a time")
}

// slowCooldowns is a cooldown store that takes a moment for each read and
// write, so that decisions that are not made one after another would
// overlap.
type slowCooldowns struct {
	*cooldowns
	mu      sync.Mutex
	busy    int
	overlap bool
}

// slowly notes that an operation runs while another does, and takes a
// moment.
func (s *slowCooldowns) slowly() func() {
	s.mu.Lock()
	s.busy++
	s.overlap = s.overlap || s.busy > 1
	s.mu.Unlock()
	time.Sleep(time.Millisecond)
	return func() {
		s.mu.Lock()
		s.busy--
		s.mu.Unlock()
	}
}

func (s *slowCooldowns) CooldownEnd(ctx context.Context, key command.CooldownKey) (time.Time, bool, error) {
	defer s.slowly()()
	return s.cooldowns.CooldownEnd(ctx, key)
}

func (s *slowCooldowns) PutCooldown(ctx context.Context, key command.CooldownKey, ends, now time.Time) error {
	defer s.slowly()()
	return s.cooldowns.PutCooldown(ctx, key, ends, now)
}

func (s *slowCooldowns) DeleteCooldown(ctx context.Context, key command.CooldownKey, ends time.Time) error {
	defer s.slowly()()
	return s.cooldowns.DeleteCooldown(ctx, key, ends)
}

func (s *slowCooldowns) overlapped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overlap
}

// withCooldowns returns a service like the one of f with the cooldown
// store c.
func (f *fixture) withCooldowns(t *testing.T, c requirement.Cooldowns) *requirement.Service {
	t.Helper()
	catalog, err := i18n.Load()
	require.NoError(t, err)
	set, err := connector.NewSet(f.twitch, f.kick)
	require.NoError(t, err)
	svc, err := requirement.New(requirement.Ports{
		Catalog: catalog, Language: language{lang: i18n.English}, Platforms: set,
		Cooldowns: c, Streamer: f.streamer, Users: f.users, Logger: slog.New(slog.NewTextHandler(f.logs, nil)),
	})
	require.NoError(t, err)
	return svc
}

// TestStartCooldown covers B25 and actions.md B37: the action starts the
// cooldown as if the command had been queued; the scopes per user need the
// user of the run, also where a check would count the streamer.
func TestStartCooldown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sounds := command.CooldownGroup{ID: id.New(), Name: "Sounds", Duration: 2 * time.Minute}
		f := newFixture(t, language{lang: i18n.English})
		f.cooldowns.groups = []command.CooldownGroup{sounds}
		ada := person("ada", platform.Twitch)
		now := time.Now()

		require.NoError(t, f.service.StartCooldown(t.Context(), cmd(), engine.Params{}), "nothing to start")
		assert.Empty(t, f.cooldowns.running())

		standard := cmd(cooldownFor(command.CooldownStandard, time.Minute, id.ID{}))
		require.NoError(t, f.service.StartCooldown(t.Context(), standard, engine.Params{}))
		perUser := cmd(cooldownFor(command.CooldownPerUser, time.Minute, id.ID{}))
		require.NoError(t, f.service.StartCooldown(t.Context(), perUser, chat(ada)))
		grouped := cmd(cooldownFor(command.CooldownPerUserGrouped, 0, sounds.ID))
		require.NoError(t, f.service.StartCooldown(t.Context(), grouped, chat(ada)))
		assert.Equal(t, map[command.CooldownKey]time.Duration{
			{Command: standard.ID}:              time.Minute,
			{Command: perUser.ID, User: ada.ID}: time.Minute,
			{Group: sounds.ID, User: ada.ID}:    2 * time.Minute,
		}, f.cooldowns.left(now))
		assert.Equal(t, engine.VerdictRejected, f.verdict(t, standard, chat(ada)))

		require.Error(t, f.service.StartCooldown(t.Context(), perUser, engine.Params{}), "per user without a user")
		require.Error(t, f.service.StartCooldown(t.Context(), cmd(cooldownFor(command.CooldownGrouped, 0, id.New())), engine.Params{}),
			"a cooldown group that does not exist")
		require.Error(t, f.service.StartCooldown(t.Context(), cmd(command.CooldownRequirement{Scope: command.CooldownGrouped}), engine.Params{}),
			"no cooldown group")
		f.cooldowns.putErr = errors.New("disk full")
		require.ErrorContains(t, f.service.StartCooldown(t.Context(), standard, engine.Params{}), "disk full")
	})
}

// TestCooldownRevert covers command-engine.md B15: a decision takes back the
// cooldown it started, but not a later start.
func TestCooldownRevert(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, language{lang: i18n.English})
		c := cmd(cooldownFor(command.CooldownStandard, time.Minute, id.ID{}))
		ada := chat(person("ada", platform.Twitch))

		met, err := apply(t.Context(), f.service, c, ada)
		require.NoError(t, err)
		require.NotNil(t, met.Revert)
		require.NoError(t, met.Revert(t.Context()))
		assert.Empty(t, f.cooldowns.running())

		met, err = apply(t.Context(), f.service, c, ada)
		require.NoError(t, err)
		time.Sleep(time.Second)
		require.NoError(t, f.service.StartCooldown(t.Context(), c, ada))
		require.NoError(t, met.Revert(t.Context()))
		assert.Equal(t, map[command.CooldownKey]time.Duration{{Command: c.ID}: time.Minute}, f.cooldowns.left(time.Now()),
			"the later start stays")

		free, err := apply(t.Context(), f.service, cmd(), ada)
		require.NoError(t, err)
		assert.Nil(t, free.Revert, "nothing to take back")
	})
}

// TestCooldownErrors: a cooldown that cannot be read or started is an
// error, and nothing runs.
func TestCooldownErrors(t *testing.T) {
	t.Parallel()
	sounds := command.CooldownGroup{ID: id.New(), Name: "Sounds", Duration: time.Minute}
	ada := chat(person("ada", platform.Twitch))
	for name, tc := range map[string]struct {
		cmd  command.Command
		fail func(*cooldowns)
	}{
		"cooldown groups": {cmd(cooldownFor(command.CooldownGrouped, 0, sounds.ID)), func(c *cooldowns) { c.fail(errors.New("store gone")) }},
		"end":             {cmd(cooldownFor(command.CooldownStandard, time.Minute, id.ID{})), func(c *cooldowns) { c.fail(errors.New("store gone")) }},
		"start":           {cmd(cooldownFor(command.CooldownStandard, time.Minute, id.ID{})), func(c *cooldowns) { c.putErr = errors.New("store gone") }},
	} {
		f := newFixture(t, language{lang: i18n.English})
		f.cooldowns.groups = []command.CooldownGroup{sounds}
		tc.fail(f.cooldowns)
		_, err := apply(t.Context(), f.service, tc.cmd, ada)
		require.ErrorContains(t, err, "store gone", name)
	}

	t.Run("group deleted meanwhile", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, language{lang: i18n.English})
		deleting := &deletingGroups{cooldowns: f.cooldowns}
		f.cooldowns.groups = []command.CooldownGroup{sounds}
		_, err := apply(t.Context(), f.withCooldowns(t, deleting), cmd(cooldownFor(command.CooldownGrouped, 0, sounds.ID)), ada)
		require.ErrorContains(t, err, "does not exist")
		assert.Empty(t, f.cooldowns.running())
	})
}

// deletingGroups deletes every cooldown group after the first listing.
type deletingGroups struct {
	*cooldowns
}

func (d *deletingGroups) CooldownGroups(ctx context.Context) ([]command.CooldownGroup, error) {
	groups, err := d.cooldowns.CooldownGroups(ctx)
	d.mu.Lock()
	d.groups = nil
	d.mu.Unlock()
	return groups, err
}

// TestCooldownOutlastsRestart covers B23 with the store: a running cooldown
// is still there after the profile was closed and opened again.
func TestCooldownOutlastsRestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "profiles", "default.db")
	s, err := store.Open(ctx, path)
	require.NoError(t, err)
	codec, err := command.NewCodec()
	require.NoError(t, err)
	commands, err := command.NewService(s, codec, command.Checks{Counters: s, Names: noNames{}, Types: noTypes{}, Roots: noRoots{}})
	require.NoError(t, err)
	daily := cmd(cooldownFor(command.CooldownPerUser, time.Hour, id.ID{}))
	daily.Triggers, daily.ErrorPolicy = []string{"daily"}, command.ErrorContinue
	saved, err := commands.Save(ctx, daily)
	require.NoError(t, err)
	ada, _, err := s.UpsertIdentity(ctx, user.Identity{Platform: platform.Twitch, PlatformUserID: "1001", Login: "ada", DisplayName: "ada"})
	require.NoError(t, err)

	f := newFixture(t, language{lang: i18n.English})
	assert.Equal(t, engine.VerdictMet, decide(t, f.withCooldowns(t, s), saved.Command, chat(&ada)))
	require.NoError(t, s.Close())

	reopened, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	assert.Equal(t, engine.VerdictRejected, decide(t, f.withCooldowns(t, reopened), saved.Command, chat(&ada)))
}

// decide applies the requirements of c for p with svc and returns the
// verdict.
func decide(t *testing.T, svc *requirement.Service, c command.Command, p engine.Params) engine.Verdict {
	t.Helper()
	d, err := apply(t.Context(), svc, c, p)
	require.NoError(t, err)
	return d.Verdict
}

// noNames reserves no names.
type noNames struct{}

func (noNames) Reserved(string) (string, bool) { return "", false }

// noRoots knows no file roots.
type noRoots struct{}

func (noRoots) HasRoot(string) bool { return false }
