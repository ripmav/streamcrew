// SPDX-License-Identifier: MIT

package requirement_test

import (
	"testing"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/template"
	"github.com/stretchr/testify/require"
)

// cheer returns the parameters of a run triggered by a cheer carrying n bits.
func cheer(n int64) engine.Params {
	p := engine.Params{Platform: platform.Twitch, User: person("ada", platform.Twitch), MessageID: "m1"}
	if n > 0 {
		p.Values = map[string]template.Value{template.EventCheerBits: template.IntValue(n)}
	}
	return p
}

// bitsRejection returns the rejection of the bits requirement.
func bitsRejection(amount int64, tell bool) engine.Decision {
	return engine.Rejected(engine.Rejection{
		Requirement: command.TypeBits,
		Reason:      i18n.Message{Key: i18n.KeyRequirementBits, Args: map[string]i18n.Value{"amount": i18n.Int(amount)}},
		Tell:        tell,
	})
}

// TestBits covers the bits requirement (roadmap 4.4): the run must carry at
// least the required bits; a run without bits is rejected.
func TestBits(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	r := command.BitsRequirement{Amount: 100}

	for name, tc := range map[string]struct {
		cmd  command.Command
		p    engine.Params
		want engine.Decision
	}{
		"no requirement":            {cmd(), cheer(0), engine.Met(cheer(0))},
		"meets":                     {cmd(r), cheer(100), engine.Met(cheer(100))},
		"above":                     {cmd(r), cheer(150), engine.Met(cheer(150))},
		"below":                     {cmd(r), cheer(99), bitsRejection(100, false)},
		"no bits":                   {cmd(r), cheer(0), bitsRejection(100, false)},
		"bits and role, role first": {cmd(command.RoleRequirement{Role: role.Moderator}, r), cheer(150), rejection(role.Moderator, false)},
	} {
		got, err := apply(t.Context(), f.service, tc.cmd, tc.p)
		require.NoError(t, err, name)
		require.Equal(t, tc.want.Verdict, got.Verdict, name)
		if tc.want.Verdict == engine.VerdictRejected {
			require.Equal(t, tc.want.Rejection, got.Rejection, name)
		}
	}
}
