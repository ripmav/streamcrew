// SPDX-License-Identifier: MIT

package commandfile_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/commandfile"
)

// TestDuplicates covers B22 and B60: names that differ only in case are an
// error at each document, across the kinds of commands, but not between
// a command and a group, and not for documents without a known kind or a
// name.
func TestDuplicates(t *testing.T) {
	t.Parallel()
	a, problems := commandfile.Read("a.yaml", []byte(
		"apiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: Hug}\nspec: {triggers: [hug]}\n"+
			"---\napiVersion: streamcrew/v1alpha1\nkind: CommandGroup\nmetadata: {name: hug}\nspec: {}\n"))
	require.Empty(t, problems)
	b, problems := commandfile.Read("b.yaml", []byte(
		"apiVersion: streamcrew/v1alpha1\nkind: ActionGroup\nmetadata: {name: hug}\nspec: {}\n"+
			"---\nkind: TimerCommand\n---\nkind: Other\nmetadata: {name: hug}\n---\nkind: CooldownGroup\nmetadata: {name: Hugs}\n"))
	require.Empty(t, problems)
	got := []string{}
	for _, p := range commandfile.Duplicates(append(a, b...)) {
		got = append(got, p.String())
	}
	assert.Equal(t, []string{
		"a.yaml:3:18: metadata.name: the name is used again, regardless of case, at b.yaml:3:18",
		"b.yaml:3:18: metadata.name: the name is used again, regardless of case, at a.yaml:3:18",
	}, got)

	c, problems := commandfile.Read("c.yaml", []byte("kind: CooldownGroup\nmetadata: {name: HUGS}\n---\nkind: CooldownGroup\nmetadata: {name: hugs}\n---\nkind: CooldownGroup\nmetadata: {name: Hugs}\n"))
	require.Empty(t, problems)
	got = []string{}
	for _, p := range commandfile.Duplicates(c) {
		got = append(got, p.String())
	}
	assert.Equal(t, []string{
		"c.yaml:2:18: metadata.name: the name is used again, regardless of case, at c.yaml:5:18",
		"c.yaml:5:18: metadata.name: the name is used again, regardless of case, at c.yaml:8:18",
		"c.yaml:8:18: metadata.name: the name is used again, regardless of case, at c.yaml:2:18",
	}, got, "each names the next, the last the first")
}
