// SPDX-License-Identifier: Apache-2.0

package commandfile

import (
	"fmt"
	"slices"

	"github.com/ripmav/streamcrew/internal/domain/command"
)

// Duplicates returns the problems of documents whose names differ only in
// case from the name of another document of the same kind (B22, B60); the
// four kinds of commands share their names (commands.md, B7). Each such
// document has a problem. Documents without a kind or a name are left to
// the check.
func Duplicates(docs []Document) []Problem {
	type entry struct {
		doc Document
		pos position
	}
	byKey := map[string][]entry{}
	var keys []string
	for _, d := range docs {
		k, ok := d.Kind()
		name, named := d.Name()
		if !ok || !named || !slices.Contains(Kinds(), k) {
			continue
		}
		space := string(k)
		if _, isCommand := k.CommandKind(); isCommand {
			space = "command"
		}
		key := space + "\x00" + command.NameKey(name)
		if _, seen := byKey[key]; !seen {
			keys = append(keys, key)
		}
		meta, _ := d.root.lookup("metadata")
		m, _ := meta.value.lookup("name")
		byKey[key] = append(byKey[key], entry{d, m.value.pos})
	}
	var problems []Problem
	for _, key := range keys {
		entries := byKey[key]
		if len(entries) < 2 {
			continue
		}
		for i, e := range entries {
			other := entries[(i+1)%len(entries)]
			problems = append(problems, e.doc.problem(e.pos, "metadata.name",
				fmt.Sprintf("the name is used again, regardless of case, at %s:%d:%d", other.doc.File, other.pos.line, other.pos.column)))
		}
	}
	return problems
}
