// SPDX-License-Identifier: MIT

package settings_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

func newService(t *testing.T) (*settings.Service, *store.Store) {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	svc, err := settings.New(s)
	require.NoError(t, err)
	return svc, s
}

func TestDefaultsWhenNeverSaved(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	b, err := settings.Load(t.Context(), svc, settings.DefaultBackups())
	require.NoError(t, err)
	assert.Equal(t, settings.Backups{Enabled: true, At: "04:00", KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12}, b)

	tz, err := settings.Load(t.Context(), svc, settings.DefaultTime())
	require.NoError(t, err)
	loc, err := tz.Location()
	require.NoError(t, err)
	assert.Equal(t, time.Local, loc, "empty means the system time zone")

	l, err := settings.Load(t.Context(), svc, settings.DefaultLocale())
	require.NoError(t, err)
	assert.Equal(t, settings.Locale{Language: i18n.English, Locale: settings.LocaleSystem}, l, "ADR-0022, point 7; B41")

	c, err := settings.Load(t.Context(), svc, settings.DefaultCommands())
	require.NoError(t, err)
	assert.Equal(t, settings.Commands{
		LockMode:              settings.LockPerCommandType,
		ErrorCooldown:         settings.ErrorCooldownPerCommand,
		ErrorCooldownDuration: polydoc.Duration(10 * time.Second),
		ArgDelimiter:          "|",
		EntranceMediaGap:      polydoc.Duration(5 * time.Second),
		QueueSize:             1000,
		UserLookupAttempts:    3,
		UserLookupTimeout:     polydoc.Duration(2 * time.Second),
	}, c, "B90")

	e, err := settings.Load(t.Context(), svc, settings.DefaultEvents())
	require.NoError(t, err)
	assert.Equal(t, settings.Events{MassGiftThreshold: 2, StreamGracePeriod: polydoc.Duration(10 * time.Minute)}, e, "events.md B10")

	ch, err := settings.Load(t.Context(), svc, settings.DefaultChannels())
	require.NoError(t, err)
	assert.Equal(t, settings.Channels{}, ch, "no additional channel by default")

	g, err := settings.Load(t.Context(), svc, settings.DefaultGeneral())
	require.NoError(t, err)
	assert.Equal(t, settings.General{DefaultPlatform: platform.Default}, g, "chat-service.md B26")

	ch2, err := settings.Load(t.Context(), svc, settings.DefaultChat())
	require.NoError(t, err)
	assert.Equal(t, settings.Chat{HistorySize: 200}, ch2, "chat-service.md B15, B17, B30")

	u, err := settings.Load(t.Context(), svc, settings.DefaultUsers())
	require.NoError(t, err)
	assert.Equal(t, settings.Users{}, u, "no regular role by default (users-and-roles.md, B26)")
}

func TestSaveAndLoad(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, s := newService(t)

	want := settings.Backups{Enabled: false, At: "23:30", KeepDaily: 3, KeepWeekly: 2, KeepMonthly: 1}
	require.NoError(t, settings.Save(ctx, svc, want))
	got, err := settings.Load(ctx, svc, settings.DefaultBackups())
	require.NoError(t, err)
	assert.Equal(t, want, got)

	doc, _, err := s.Settings(ctx, "backups")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"backups","schemaVersion":1,"enabled":false,"at":"23:30","keepDaily":3,"keepWeekly":2,"keepMonthly":1}`, string(doc))

	require.NoError(t, settings.Save(ctx, svc, settings.Locale{Language: i18n.German, Locale: settings.LocaleSystem}))
	locale, err := settings.Load(ctx, svc, settings.DefaultLocale())
	require.NoError(t, err)
	assert.Equal(t, i18n.German, locale.Language)
	assert.Equal(t, settings.LocaleSystem, locale.Locale)
	doc, _, err = s.Settings(ctx, "locale")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"locale","schemaVersion":2,"language":"de","locale":"system"}`, string(doc))

	require.NoError(t, settings.Save(ctx, svc, settings.Time{TimeZone: "Europe/Berlin"}))
	tz, err := settings.Load(ctx, svc, settings.DefaultTime())
	require.NoError(t, err)
	loc, err := tz.Location()
	require.NoError(t, err)
	assert.Equal(t, "Europe/Berlin", loc.String())

	cmds := settings.Commands{
		LockMode:              settings.LockVisualAudio,
		ErrorCooldown:         settings.ErrorCooldownGlobal,
		ErrorCooldownDuration: polydoc.Duration(time.Minute),
		ArgDelimiter:          ";",
		EntranceMediaGap:      polydoc.Duration(1500 * time.Millisecond),
		QueueSize:             50,
		UserLookupAttempts:    5,
		UserLookupTimeout:     polydoc.Duration(500 * time.Millisecond),
	}
	require.NoError(t, settings.Save(ctx, svc, cmds))
	gotCmds, err := settings.Load(ctx, svc, settings.DefaultCommands())
	require.NoError(t, err)
	assert.Equal(t, cmds, gotCmds)
	doc, _, err = s.Settings(ctx, "commands")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"commands","schemaVersion":4,"lockMode":"visual_audio","errorCooldown":"global","errorCooldownDuration":"1m0s","argDelimiter":";","entranceMediaGap":"1.5s","queueSize":50,"userLookupAttempts":5,"userLookupTimeout":"500ms"}`, string(doc))

	events := settings.Events{MassGiftThreshold: 1000, StreamGracePeriod: 0}
	require.NoError(t, settings.Save(ctx, svc, events), "B10: 0 is without grace period")
	gotEvents, err := settings.Load(ctx, svc, settings.DefaultEvents())
	require.NoError(t, err)
	assert.Equal(t, events, gotEvents)
	doc, _, err = s.Settings(ctx, "events")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"events","schemaVersion":1,"massGiftThreshold":1000,"streamGracePeriod":"0s"}`, string(doc))

	chans := settings.Channels{Additional: []string{"friend", "Other", strings.Repeat("a", 25)}}
	require.NoError(t, settings.Save(ctx, svc, chans))
	gotChans, err := settings.Load(ctx, svc, settings.DefaultChannels())
	require.NoError(t, err)
	assert.Equal(t, chans, gotChans)
	assert.True(t, gotChans.Contains("FRIEND"), "the logins compare case-insensitively")
	assert.False(t, gotChans.Contains("home"))
	doc, _, err = s.Settings(ctx, "channels")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"channels","schemaVersion":2,"additional":["friend","Other","aaaaaaaaaaaaaaaaaaaaaaaaa"]}`, string(doc))

	gen := settings.General{DefaultPlatform: platform.Mock}
	require.NoError(t, settings.Save(ctx, svc, gen))
	gotGen, err := settings.Load(ctx, svc, settings.DefaultGeneral())
	require.NoError(t, err)
	assert.Equal(t, gen, gotGen)
	doc, _, err = s.Settings(ctx, "general")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"general","schemaVersion":1,"defaultPlatform":"mock"}`, string(doc))

	chat := settings.Chat{HistorySize: 1000, Protocol: true, DeleteTriggerMessage: true}
	require.NoError(t, settings.Save(ctx, svc, chat))
	gotChat, err := settings.Load(ctx, svc, settings.DefaultChat())
	require.NoError(t, err)
	assert.Equal(t, chat, gotChat)
	doc, _, err = s.Settings(ctx, "chat")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"chat","schemaVersion":1,"historySize":1000,"protocol":true,"deleteTriggerMessage":true}`, string(doc))

	users := settings.Users{RegularHours: 3, TitleRules: []user.TitleRule{{Name: "Fan", MinRole: role.Follower, MinMonths: 3}}}
	require.NoError(t, settings.Save(ctx, svc, users))
	gotUsers, err := settings.Load(ctx, svc, settings.DefaultUsers())
	require.NoError(t, err)
	assert.Equal(t, users, gotUsers)
	doc, _, err = s.Settings(ctx, "users")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"users","schemaVersion":1,"regularHours":3,"titleRules":[{"name":"Fan","minRole":"follower","minMonths":3}]}`, string(doc))
}

func TestValidation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := newService(t)
	for _, s := range []settings.Section{
		settings.Backups{At: "25:00"},
		settings.Backups{At: "4 Uhr"},
		settings.Backups{At: "04:00", KeepDaily: -1},
		settings.Time{TimeZone: "Mars/Olympus"},
		settings.Locale{Language: "fr"},
		settings.Locale{},
		settings.Locale{Language: i18n.English, Locale: "fr-FR"},
		settings.Locale{Language: i18n.English, Locale: ""},
		settings.Locale{Language: i18n.English, Locale: "de"},
		withCommands(func(c *settings.Commands) { c.LockMode = "per_user" }),
		withCommands(func(c *settings.Commands) { c.LockMode = "" }),
		withCommands(func(c *settings.Commands) { c.ErrorCooldown = "sometimes" }),
		withCommands(func(c *settings.Commands) { c.ErrorCooldownDuration = -1 }),
		withCommands(func(c *settings.Commands) { c.ArgDelimiter = "" }),
		withCommands(func(c *settings.Commands) { c.ArgDelimiter = "\n" }),
		withCommands(func(c *settings.Commands) { c.EntranceMediaGap = 0 }),
		withCommands(func(c *settings.Commands) { c.EntranceMediaGap = polydoc.Duration(999 * time.Millisecond) }),
		withCommands(func(c *settings.Commands) { c.EntranceMediaGap = polydoc.Duration(time.Minute + time.Millisecond) }),
		withCommands(func(c *settings.Commands) { c.QueueSize = 0 }),
		withCommands(func(c *settings.Commands) { c.QueueSize = 10_001 }),
		withCommands(func(c *settings.Commands) { c.UserLookupAttempts = 0 }),
		withCommands(func(c *settings.Commands) { c.UserLookupAttempts = 11 }),
		withCommands(func(c *settings.Commands) { c.UserLookupTimeout = polydoc.Duration(99 * time.Millisecond) }),
		withCommands(func(c *settings.Commands) { c.UserLookupTimeout = polydoc.Duration(30*time.Second + time.Millisecond) }),
		settings.Events{MassGiftThreshold: 1, StreamGracePeriod: 0},
		settings.Events{MassGiftThreshold: 1001, StreamGracePeriod: 0},
		settings.Events{MassGiftThreshold: 2, StreamGracePeriod: -1},
		settings.Events{MassGiftThreshold: 2, StreamGracePeriod: polydoc.Duration(time.Hour + time.Millisecond)},
		settings.ChannelPoints{Rewards: []settings.ChannelPointReward{{RewardID: "r1"}}},
		settings.ChannelPoints{Rewards: []settings.ChannelPointReward{{Command: "c"}}},
		settings.ChannelPoints{Rewards: []settings.ChannelPointReward{{RewardID: "r1", Command: "c"}, {RewardID: "r1", Command: "d"}}},
		settings.Channels{Additional: []string{"a", ""}},
		settings.Channels{Additional: []string{"a", "a"}},
		settings.Channels{Additional: []string{"a", "A"}},
		settings.Channels{Additional: []string{"has space"}},
		settings.Channels{Additional: []string{strings.Repeat("a", 26)}},
		settings.General{DefaultPlatform: ""},
		settings.General{DefaultPlatform: "Twitch"},
		settings.General{DefaultPlatform: platform.Name(strings.Repeat("a", 33))},
		settings.Chat{HistorySize: 0},
		settings.Chat{HistorySize: 10_001},
		settings.Users{RegularHours: -1},
		settings.Users{TitleRules: []user.TitleRule{{Name: "", MinRole: role.Follower, MinMonths: 0}}},
		settings.Users{TitleRules: []user.TitleRule{{Name: "Fan", MinRole: "vip", MinMonths: 0}}},
		settings.Users{TitleRules: []user.TitleRule{{Name: "Fan", MinRole: role.Follower, MinMonths: -1}}},
		settings.Timers{Interval: polydoc.Duration(-time.Second)},
		settings.Timers{MinChatMessages: -1},
		settings.Timers{Order: "sometimes"},
		settings.Moderation{MinFollowDays: -1},
		settings.Moderation{MinWatchMinutes: -1},
		settings.Moderation{MinRole: "vip"},
		settings.Moderation{Filters: []settings.Filter{{Kind: "unknown"}}},
		settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterWord}}},
		settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterUppercase, Threshold: -1}}},
		settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterUppercase, Threshold: 101, Percent: true}}},
		settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterWord, Word: "x", Exempt: []role.Role{"vip"}}}},
		settings.Moderation{StrikeLevels: []settings.StrikeLevel{{Level: 0}}},
		settings.Moderation{StrikeLevels: []settings.StrikeLevel{{Level: 1}, {Level: 1}}},
	} {
		assert.Error(t, settings.Save(ctx, svc, s), "%+v", s)
	}
}

func TestCommandsModes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := newService(t)
	for _, m := range []settings.LockMode{
		settings.LockPerCommandType, settings.LockPerActionType, settings.LockVisualAudio,
		settings.LockSingular, settings.LockNone,
	} {
		assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.LockMode = m })), m)
	}
	for _, m := range []settings.ErrorCooldown{
		settings.ErrorCooldownPerCommand, settings.ErrorCooldownGlobal, settings.ErrorCooldownOff, settings.ErrorCooldownSilent,
	} {
		assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.ErrorCooldown = m })), m)
	}
	assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.ErrorCooldownDuration = 0 })),
		"0 holds back no message")
	for _, gap := range []time.Duration{settings.MinEntranceMediaGap, settings.MaxEntranceMediaGap} {
		assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.EntranceMediaGap = polydoc.Duration(gap) })),
			"B43: %s", gap)
	}
	for _, size := range []int{settings.MinQueueSize, settings.MaxQueueSize} {
		assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.QueueSize = size })), "B15: %d", size)
	}
	for _, n := range []int{settings.MinUserLookupAttempts, settings.MaxUserLookupAttempts} {
		assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.UserLookupAttempts = n })), "B17: %d", n)
	}
	for _, d := range []time.Duration{settings.MinUserLookupTimeout, settings.MaxUserLookupTimeout} {
		assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.UserLookupTimeout = polydoc.Duration(d) })), "B17: %s", d)
	}
}

// TestCommandsVersion1: stored version 1 documents are migrated; they get
// the default gap of B43 and the default queue size of B15.
func TestCommandsVersion1(t *testing.T) {
	t.Parallel()
	v1 := `{"type":"commands","schemaVersion":1,"lockMode":"singular","errorCooldown":"off","errorCooldownDuration":"0s","argDelimiter":";"}`
	svc, err := settings.New(fakeRepo{doc: []byte(v1)})
	require.NoError(t, err)
	got, err := settings.Load(t.Context(), svc, settings.DefaultCommands())
	require.NoError(t, err)
	assert.Equal(t, settings.Commands{
		LockMode:              settings.LockSingular,
		ErrorCooldown:         settings.ErrorCooldownOff,
		ErrorCooldownDuration: 0,
		ArgDelimiter:          ";",
		EntranceMediaGap:      polydoc.Duration(settings.DefaultEntranceMediaGap),
		QueueSize:             settings.DefaultQueueSize,
		UserLookupAttempts:    settings.DefaultUserLookupAttempts,
		UserLookupTimeout:     polydoc.Duration(settings.DefaultUserLookupTimeout),
	}, got)

	withGap := `{"type":"commands","schemaVersion":1,"lockMode":"singular","errorCooldown":"off","errorCooldownDuration":"0s","argDelimiter":";","entranceMediaGap":"2s"}`
	svc, err = settings.New(fakeRepo{doc: []byte(withGap)})
	require.NoError(t, err)
	_, err = settings.Load(t.Context(), svc, settings.DefaultCommands())
	require.Error(t, err, "version 1 never had the gap")
}

// TestCommandsVersion2: stored version 2 documents are migrated; they get
// the default queue size of B15.
func TestCommandsVersion2(t *testing.T) {
	t.Parallel()
	v2 := `{"type":"commands","schemaVersion":2,"lockMode":"singular","errorCooldown":"off","errorCooldownDuration":"0s","argDelimiter":";","entranceMediaGap":"2s"}`
	svc, err := settings.New(fakeRepo{doc: []byte(v2)})
	require.NoError(t, err)
	got, err := settings.Load(t.Context(), svc, settings.DefaultCommands())
	require.NoError(t, err)
	assert.Equal(t, settings.Commands{
		LockMode:              settings.LockSingular,
		ErrorCooldown:         settings.ErrorCooldownOff,
		ErrorCooldownDuration: 0,
		ArgDelimiter:          ";",
		EntranceMediaGap:      polydoc.Duration(2 * time.Second),
		QueueSize:             settings.DefaultQueueSize,
		UserLookupAttempts:    settings.DefaultUserLookupAttempts,
		UserLookupTimeout:     polydoc.Duration(settings.DefaultUserLookupTimeout),
	}, got)

	withSize := `{"type":"commands","schemaVersion":2,"lockMode":"singular","errorCooldown":"off","errorCooldownDuration":"0s","argDelimiter":";","entranceMediaGap":"2s","queueSize":5}`
	svc, err = settings.New(fakeRepo{doc: []byte(withSize)})
	require.NoError(t, err)
	_, err = settings.Load(t.Context(), svc, settings.DefaultCommands())
	require.Error(t, err, "version 2 never had the queue size")
}

// TestCommandsVersion3: stored version 3 documents are migrated; they get
// the defaults of the user lookups of B17.
func TestCommandsVersion3(t *testing.T) {
	t.Parallel()
	v3 := `{"type":"commands","schemaVersion":3,"lockMode":"singular","errorCooldown":"off","errorCooldownDuration":"0s","argDelimiter":";","entranceMediaGap":"2s","queueSize":7}`
	svc, err := settings.New(fakeRepo{doc: []byte(v3)})
	require.NoError(t, err)
	got, err := settings.Load(t.Context(), svc, settings.DefaultCommands())
	require.NoError(t, err)
	assert.Equal(t, 7, got.QueueSize)
	assert.Equal(t, settings.DefaultUserLookupAttempts, got.UserLookupAttempts)
	assert.Equal(t, polydoc.Duration(settings.DefaultUserLookupTimeout), got.UserLookupTimeout)

	for _, field := range []string{`"userLookupAttempts":2`, `"userLookupTimeout":"1s"`} {
		doc := strings.Replace(v3, `"queueSize":7`, `"queueSize":7,`+field, 1)
		svc, err := settings.New(fakeRepo{doc: []byte(doc)})
		require.NoError(t, err)
		_, err = settings.Load(t.Context(), svc, settings.DefaultCommands())
		require.Error(t, err, "version 3 never had %s", field)
	}
}

// withCommands returns the default commands section changed by edit.
func withCommands(edit func(c *settings.Commands)) settings.Commands {
	c := settings.DefaultCommands()
	edit(&c)
	return c
}

type fakeRepo struct{ doc []byte }

func (f fakeRepo) Settings(context.Context, string) ([]byte, bool, error) { return f.doc, true, nil }
func (fakeRepo) PutSettings(context.Context, string, []byte) error        { return nil }

func TestNewerStoredVersionFallsBackToDefaults(t *testing.T) {
	t.Parallel()
	svc, err := settings.New(fakeRepo{doc: []byte(`{"type":"backups","schemaVersion":9,"future":true}`)})
	require.NoError(t, err)
	b, err := settings.Load(t.Context(), svc, settings.DefaultBackups())
	require.Error(t, err)
	assert.Equal(t, settings.DefaultBackups(), b)
}

// TestUsersWithoutTitleRules: a users document stored before the title
// rules (without the key) decodes with the empty list (B5, plan 5.2.3).
func TestUsersWithoutTitleRules(t *testing.T) {
	t.Parallel()
	v1 := `{"type":"users","schemaVersion":1,"regularHours":2}`
	svc, err := settings.New(fakeRepo{doc: []byte(v1)})
	require.NoError(t, err)
	got, err := settings.Load(t.Context(), svc, settings.DefaultUsers())
	require.NoError(t, err)
	assert.Equal(t, settings.Users{RegularHours: 2}, got)
	assert.Empty(t, got.TitleRules)
}

func TestUnloadableTimeZoneFallsBackToUTC(t *testing.T) {
	t.Parallel()
	loc, err := settings.Time{TimeZone: "Mars/Olympus"}.Location()
	require.Error(t, err)
	assert.Equal(t, time.UTC, loc)
}

// TestChannelsVersion1: stored version 1 documents are migrated; the chat
// adapter is dropped, it is the start configuration now (--twitch-irc,
// plan 2026-10-22).
func TestChannelsVersion1(t *testing.T) {
	t.Parallel()
	v1 := `{"type":"channels","schemaVersion":1,"additional":["friend"],"chatAdapter":"irc"}`
	svc, err := settings.New(fakeRepo{doc: []byte(v1)})
	require.NoError(t, err)
	got, err := settings.Load(t.Context(), svc, settings.DefaultChannels())
	require.NoError(t, err)
	assert.Equal(t, settings.Channels{Additional: []string{"friend"}}, got)
}

// TestLocaleVersion1: stored version 1 documents are migrated; they get the
// default format locale "system" (B41).
func TestLocaleVersion1(t *testing.T) {
	t.Parallel()
	for doc, want := range map[string]settings.Locale{
		`{"type":"locale","schemaVersion":1,"language":"de"}`: {Language: i18n.German, Locale: settings.LocaleSystem},
		`{"type":"locale","schemaVersion":1,"language":"en"}`: {Language: i18n.English, Locale: settings.LocaleSystem},
	} {
		svc, err := settings.New(fakeRepo{doc: []byte(doc)})
		require.NoError(t, err)
		got, err := settings.Load(t.Context(), svc, settings.DefaultLocale())
		require.NoError(t, err, doc)
		assert.Equal(t, want, got, doc)
	}
	svc, err := settings.New(fakeRepo{doc: []byte(`{"type":"locale","schemaVersion":1,"language":"de","locale":"de-DE"}`)})
	require.NoError(t, err)
	_, err = settings.Load(t.Context(), svc, settings.DefaultLocale())
	require.Error(t, err, "version 1 never had the format locale")
}

// TestLocaleModes: the locales the core knows and system are storable,
// regardless of case (B41).
func TestLocaleModes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := newService(t)
	for _, name := range []string{settings.LocaleSystem, "en-US", "en-GB", "de-DE", "de-AT", "de-CH", "de-de"} {
		assert.NoError(t, settings.Save(ctx, svc, settings.Locale{Language: i18n.English, Locale: name}), name)
	}
}

// TestTimeZoneSystem: the system time zone has its own name instead of an
// empty one (Code-ADR-0017), in version 2 of the section.
func TestTimeZoneSystem(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, s := newService(t)

	assert.Equal(t, settings.Time{TimeZone: settings.TimeZoneSystem}, settings.DefaultTime())
	require.NoError(t, settings.Save(ctx, svc, settings.DefaultTime()))
	doc, _, err := s.Settings(ctx, "time")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"time","schemaVersion":2,"timeZone":"system"}`, string(doc))
	loc, err := settings.DefaultTime().Location()
	require.NoError(t, err)
	assert.Equal(t, time.Local, loc)

	require.Error(t, settings.Save(ctx, svc, settings.Time{}), "an empty name is not the system zone")
	loc, err = settings.Time{}.Location()
	require.Error(t, err)
	assert.Equal(t, time.UTC, loc)
}

// TestTimeVersion1: stored version 1 documents are migrated; their empty
// name becomes "system".
func TestTimeVersion1(t *testing.T) {
	t.Parallel()
	for doc, want := range map[string]string{
		`{"type":"time","schemaVersion":1,"timeZone":""}`:              settings.TimeZoneSystem,
		`{"type":"time","schemaVersion":1,"timeZone":"Europe/Berlin"}`: "Europe/Berlin",
	} {
		svc, err := settings.New(fakeRepo{doc: []byte(doc)})
		require.NoError(t, err)
		got, err := settings.Load(t.Context(), svc, settings.DefaultTime())
		require.NoError(t, err, doc)
		assert.Equal(t, want, got.TimeZone, doc)
	}
	svc, err := settings.New(fakeRepo{doc: []byte(`{"type":"time","schemaVersion":1}`)})
	require.NoError(t, err)
	_, err = settings.Load(t.Context(), svc, settings.DefaultTime())
	require.Error(t, err, "version 1 always had the field")
}
