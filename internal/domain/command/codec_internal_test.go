// SPDX-License-Identifier: MIT

package command

import (
	"encoding/json/jsontext"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// golden compares got with testdata/<name>.golden. With
// STREAMCREW_UPDATE_GOLDEN=1 it writes the file first (Code-ADR-0006).
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Clean(filepath.Join("testdata", name+".golden"))
	if os.Getenv("STREAMCREW_UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, got, 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "create it with STREAMCREW_UPDATE_GOLDEN=1")
	assert.Equal(t, string(want), string(got))
}

// requirementExamples has one example per requirement type and version.
func requirementExamples() map[string]Requirement {
	ref := id.MustParse("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d")
	return map[string]Requirement{
		"role.v1":      RoleRequirement{Role: role.Follower},
		"cooldown.v1":  CooldownRequirement{Scope: CooldownPerUserGroup, Duration: polydoc.Duration(90 * time.Second)},
		"currency.v1":  CurrencyRequirement{Currency: ref, Mode: CurrencyRange, Amount: 10, Maximum: 100},
		"rank.v1":      RankRequirement{Rank: ref, Match: RankAtLeast},
		"inventory.v1": InventoryRequirement{Item: ref, Amount: 2},
		"arguments.v1": ArgumentsRequirement{Arguments: []Argument{
			{Name: "target", Type: ArgumentUser, Required: true, Identifier: "target"},
			{Name: "amount", Type: ArgumentNumber},
		}},
		"threshold.v1": ThresholdRequirement{Users: 3, Within: polydoc.Duration(time.Minute), RunForEachUser: true},
		"settings.v1":  SettingsRequirement{DeleteTriggerMessage: true, ShowInChatMenu: true},
	}
}

// TestRequirementGoldenFiles covers B40 to B47: every requirement type is
// stored in a fixed format and read back unchanged (Code-ADR-0010).
func TestRequirementGoldenFiles(t *testing.T) {
	t.Parallel()
	c, err := NewCodec()
	require.NoError(t, err)

	examples := requirementExamples()
	var covered []string
	for name, r := range examples {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, r.Validate())
			doc, err := c.requirements.Encode(r)
			require.NoError(t, err)
			pretty := jsontext.Value(doc)
			require.NoError(t, pretty.Indent(jsontext.WithIndent("  ")))
			golden(t, filepath.Join("requirement", name), append(pretty, '\n'))

			stored, err := os.ReadFile(filepath.Clean(filepath.Join("testdata", "requirement", name+".golden")))
			require.NoError(t, err)
			back, err := c.requirements.Decode(stored)
			require.NoError(t, err)
			assert.Equal(t, r, back)
		})
		covered = append(covered, r.DocType())
	}
	slices.Sort(covered)
	assert.Equal(t, c.RequirementTypes(), covered, "one example per requirement type")
}

// TestUnknownPartsSurvive covers B4: requirements and actions of unknown
// types are read and written back unchanged.
func TestUnknownPartsSurvive(t *testing.T) {
	t.Parallel()
	c, err := NewCodec()
	require.NoError(t, err)
	rec := Record{
		Name: "future", Kind: KindActionGroup, ErrorPolicy: ErrorContinue,
		Requirements: jsontext.Value(`[{"type":"role","schemaVersion":1,"role":"vip"},{"type":"streak","schemaVersion":3,"days":7}]`),
		Actions:      jsontext.Value(`[{"type":"chat.send","schemaVersion":1,"message":"hi"},{"type":"obs.scene","schemaVersion":9,"scene":"Main"}]`),
	}
	cmd, err := c.Command(rec)
	require.NoError(t, err)
	require.Len(t, cmd.Requirements, 2)
	assert.Equal(t, RoleRequirement{Role: role.VIP}, cmd.Requirements[0])
	assert.IsType(t, UnknownRequirement{}, cmd.Requirements[1])
	require.Len(t, cmd.Actions, 2)
	assert.IsType(t, UnknownAction{}, cmd.Actions[0], "no action types before phase 3")
	require.NoError(t, cmd.Validate())

	back, err := c.Record(cmd)
	require.NoError(t, err)
	assert.JSONEq(t, string(rec.Requirements), string(back.Requirements))
	assert.JSONEq(t, string(rec.Actions), string(back.Actions))
}

func TestCodecEdgeCases(t *testing.T) {
	t.Parallel()
	c, err := NewCodec()
	require.NoError(t, err)

	cmd, err := c.Command(Record{Name: "empty"})
	require.NoError(t, err)
	assert.Empty(t, cmd.Requirements)
	assert.Empty(t, cmd.Actions)
	rec, err := c.Record(cmd)
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(rec.Requirements))
	assert.JSONEq(t, `[]`, string(rec.Actions))

	_, err = c.Command(Record{Requirements: jsontext.Value(`{"type":"role"}`)})
	require.Error(t, err, "not an array")
	_, err = c.Command(Record{Actions: jsontext.Value(`[{"schemaVersion":1}]`)})
	require.Error(t, err, "document without type")
	_, err = c.Record(Command{Actions: []Action{nil}})
	require.ErrorIs(t, err, ErrInvalid)

	_, err = NewCodec(polydoc.Entry[Action]{Type: TypeRole})
	require.Error(t, err, "invalid action entry")
}
