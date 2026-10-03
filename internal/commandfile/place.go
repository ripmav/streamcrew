// SPDX-License-Identifier: Apache-2.0

package commandfile

import (
	"slices"
	"strconv"
	"strings"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
)

// find returns the value at path in v, e.g. "spec.triggers[1]", and the
// path it found: the longest beginning of path that exists.
func (v *value) find(path string) (*value, string) {
	cur, found := v, ""
	for seg := range strings.SplitSeq(path, ".") {
		key, rest, _ := strings.Cut(seg, "[")
		m, ok := cur.lookup(key)
		if cur.kind != kindObject || !ok {
			return cur, found
		}
		cur, found = m.value, join(found, key)
		for rest != "" {
			idx, after, _ := strings.Cut(rest, "]")
			i, err := strconv.Atoi(idx)
			if err != nil || cur.kind != kindArray || i < 0 || i >= len(cur.items) {
				return cur, found
			}
			cur, found = cur.items[i], found+"["+idx+"]"
			rest = strings.TrimPrefix(after, "[")
		}
	}
	return cur, found
}

// problemAt returns a problem at path in d, or at the nearest place above
// it that exists.
func (d Document) problemAt(severity Severity, path, msg string) Problem {
	v, found := d.root.find(path)
	p := d.problem(v.pos, found, msg)
	p.Severity = severity
	return p
}

// actionPath returns the path in d of the action at path, e.g. [3, 2] for
// the second child of the third action (actions.md, B9): child actions
// count through the lists of child actions of their parent in the order of
// its schema, as command.Parent.Children does.
func actionPath(d Document, path []int, descriptors map[string]action.Descriptor) string {
	path = slices.Clone(path)
	list, at := d.root.find("spec.actions")
	if at != "spec.actions" {
		return at
	}
	for depth := 0; depth < len(path); depth++ {
		i := path[depth] - 1
		if list.kind != kindArray || i < 0 || i >= len(list.items) {
			return at
		}
		item := list.items[i]
		at += "[" + strconv.Itoa(i) + "]"
		if depth == len(path)-1 {
			return at
		}
		typ, _ := item.lookup(keyType)
		desc := descriptors[typ.value.text]
		next, found := path[depth+1], false
		for _, p := range desc.Schema.Properties {
			if p.Schema.UI != schema.UIActions {
				continue
			}
			m, ok := item.lookup(p.Name)
			if !ok {
				continue
			}
			if next <= len(m.value.items) {
				list, at, found = m.value, join(at, p.Name), true
				path[depth+1] = next
				break
			}
			next -= len(m.value.items)
		}
		if !found {
			return at
		}
	}
	return at
}
