// SPDX-License-Identifier: Apache-2.0

package commandfile

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
)

// Source is what an export reads from the profile: the commands as stored,
// the command groups and the cooldown groups.
type Source struct {
	Commands       []command.Record
	Groups         []command.Group
	CooldownGroups []command.CooldownGroup
}

// Exported is a document of an export (B35, B36).
type Exported struct {
	Kind Kind
	Name string
	root *value
}

// Export turns objects of the profile into documents in the form of files
// (B35, B36): the commands named, regardless of case, with the command
// groups and cooldown groups they refer to; without names all commands,
// groups and cooldown groups. The documents come in the order cooldown
// groups, command groups, commands, each by name, with all members in the
// order of the schema, also those with a default; IDs become names (B23).
//
// A command that refers to an object that no longer exists (B64) or has
// an action or requirement of an unknown type (B65) cannot be written; the
// error names each such command and what it refers to, and the export
// writes nothing.
func Export(src Source, names []string, types Types) ([]Exported, error) {
	x := newExporter(src, types)
	recs, err := x.selected(names)
	if err != nil {
		return nil, err
	}
	var errs []error
	var commands []Exported
	usedGroups, usedCooldowns := map[id.ID]bool{}, map[id.ID]bool{}
	for _, rec := range recs {
		doc, err := x.command(rec, usedGroups, usedCooldowns)
		if err != nil {
			errs = append(errs, fmt.Errorf("command %q: %w", rec.Name, err))
			continue
		}
		commands = append(commands, doc)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	all := len(names) == 0
	var docs []Exported
	for _, g := range sortedByName(src.CooldownGroups, func(g command.CooldownGroup) string { return g.Name }) {
		if all || usedCooldowns[g.ID] {
			docs = append(docs, cooldownGroupDoc(g))
		}
	}
	for _, g := range sortedByName(src.Groups, func(g command.Group) string { return g.Name }) {
		if all || usedGroups[g.ID] {
			docs = append(docs, groupDoc(g))
		}
	}
	slices.SortStableFunc(commands, func(a, b Exported) int { return compareNames(a.Name, b.Name) })
	return append(docs, commands...), nil
}

// exporter turns stored objects into documents.
type exporter struct {
	types        Types
	descriptors  map[string]action.Descriptor
	requirements map[string]command.RequirementDescriptor
	reqOrder     []string
	names        map[space]map[id.ID]string
	src          Source
}

func newExporter(src Source, types Types) *exporter {
	x := &exporter{
		types:        types,
		src:          src,
		descriptors:  make(map[string]action.Descriptor, len(types.Actions)),
		requirements: map[string]command.RequirementDescriptor{},
		names:        map[space]map[id.ID]string{spaceCommand: {}, spaceGroup: {}, spaceCooldownGroup: {}},
	}
	for _, d := range types.Actions {
		x.descriptors[d.Type] = d
	}
	for _, d := range command.RequirementCatalog() {
		x.requirements[d.Type] = d
		x.reqOrder = append(x.reqOrder, d.Type)
	}
	for _, r := range src.Commands {
		x.names[spaceCommand][r.ID] = r.Name
	}
	for _, g := range src.Groups {
		x.names[spaceGroup][g.ID] = g.Name
	}
	for _, g := range src.CooldownGroups {
		x.names[spaceCooldownGroup][g.ID] = g.Name
	}
	return x
}

// selected returns the commands names names, all without names.
func (x *exporter) selected(names []string) ([]command.Record, error) {
	if len(names) == 0 {
		return x.src.Commands, nil
	}
	byKey := make(map[string]command.Record, len(x.src.Commands))
	for _, r := range x.src.Commands {
		byKey[command.NameKey(r.Name)] = r
	}
	var out []command.Record
	var errs []error
	seen := map[id.ID]bool{}
	for _, n := range names {
		r, ok := byKey[command.NameKey(n)]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("no command is named %q", n))
		case !seen[r.ID]:
			seen[r.ID] = true
			out = append(out, r)
		}
	}
	return out, errors.Join(errs...)
}

// command returns the document of a stored command and notes the groups
// and cooldown groups it refers to.
func (x *exporter) command(rec command.Record, usedGroups, usedCooldowns map[id.ID]bool) (Exported, error) {
	cmd, err := x.types.Codec.Command(rec)
	if err != nil {
		return Exported{}, err
	}
	for _, r := range cmd.Requirements {
		if u, ok := r.(command.UnknownRequirement); ok {
			return Exported{}, fmt.Errorf("requirement of the unknown type %q", u.Type)
		}
		if c, ok := r.(command.CooldownRequirement); ok && c.Scope.Grouped() {
			usedCooldowns[c.Group] = true
		}
	}
	if err := unknownActions(cmd.Actions, nil); err != nil {
		return Exported{}, err
	}
	stored, err := x.types.Codec.Record(cmd)
	if err != nil {
		return Exported{}, err
	}
	kind := kindOf(cmd.Kind)
	meta := []member{text("name", cmd.Name)}
	if !cmd.GroupID.IsZero() {
		name, ok := x.names[spaceGroup][cmd.GroupID]
		if !ok {
			return Exported{}, fmt.Errorf("the group %s does not exist", cmd.GroupID)
		}
		usedGroups[cmd.GroupID] = true
		meta = append(meta, text("group", name))
	}
	var spec []member
	switch cmd.Kind {
	case command.KindChat:
		triggers := &value{kind: kindArray, items: []*value{}}
		for _, t := range cmd.Triggers {
			triggers.items = append(triggers.items, &value{kind: kindString, text: t})
		}
		spec = append(spec, member{key: "triggers", value: triggers}, text("triggerMode", string(cmd.TriggerMode)))
	case command.KindEvent:
		spec = append(spec, text("event", string(cmd.Event)))
	case command.KindTimer, command.KindActionGroup:
	}
	spec = append(spec, boolean("enabled", cmd.Enabled), boolean("unlocked", cmd.Unlocked), text("errorPolicy", string(cmd.ErrorPolicy)))
	reqs, err := x.requirementMap(stored)
	if err != nil {
		return Exported{}, err
	}
	acts, err := x.actionList(stored)
	if err != nil {
		return Exported{}, err
	}
	spec = append(spec, member{key: "requirements", value: reqs}, member{key: "actions", value: acts})
	return document(kind, cmd.Name, meta, spec), nil
}

// unknownActions returns an error for the first action of an unknown type
// in list or below, at path (B65).
func unknownActions(list []command.Action, path []int) error {
	for i, a := range list {
		at := append(slices.Clone(path), i+1)
		if u, ok := a.(command.UnknownAction); ok {
			return fmt.Errorf("action %s of the unknown type %q", positionOf(at), u.Type)
		}
		if p, ok := a.(command.Parent); ok {
			if err := unknownActions(p.Children(), at); err != nil {
				return err
			}
		}
	}
	return nil
}

// positionOf writes the path of an action as in actions.md, B9, e.g. "3.2".
func positionOf(path []int) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ".")
}

// requirementMap returns the requirements of a stored command as the map
// of files, in the order of the catalog (B12).
func (x *exporter) requirementMap(stored command.Record) (*value, error) {
	list, err := parseJSON(stored.Requirements)
	if err != nil {
		return nil, err
	}
	byType := map[string]*value{}
	for _, item := range list.items {
		typ, _ := item.lookup(keyType)
		byType[typ.value.text] = item
	}
	out := &value{kind: kindObject, members: []member{}}
	for _, typ := range x.reqOrder {
		item, ok := byType[typ]
		if !ok {
			continue
		}
		v, err := x.fileFormAt(item, x.requirements[typ].Schema, false, nil)
		if err != nil {
			return nil, fmt.Errorf("requirement %q: %w", typ, err)
		}
		out.members = append(out.members, member{key: typ, value: v})
	}
	return out, nil
}

// actionList returns the actions of a stored command in the form of files.
func (x *exporter) actionList(stored command.Record) (*value, error) {
	list, err := parseJSON(stored.Actions)
	if err != nil {
		return nil, err
	}
	return x.actions(list, nil)
}

// actions returns a list of stored actions at path in the form of files.
func (x *exporter) actions(list *value, path []int) (*value, error) {
	out := &value{kind: kindArray, items: make([]*value, 0, len(list.items))}
	for i, item := range list.items {
		at := append(slices.Clone(path), i+1)
		typ, _ := item.lookup(keyType)
		d := x.descriptors[typ.value.text]
		v, err := x.fileFormAt(item, d.Schema, true, at)
		if err != nil {
			return nil, err
		}
		out.items = append(out.items, v)
	}
	return out, nil
}

// fileFormAt returns the stored document v in the form of files: without
// "schemaVersion" (B13), without "type" unless keepType, IDs as names
// (B23), the members in the order of the schema s, also in objects and
// lists of objects below (B36). The Go types encode their members in this
// order today; the export does not rely on it. path is the place of an
// action, so that errors name it; nil for a requirement.
func (x *exporter) fileFormAt(v *value, s *schema.Schema, keepType bool, path []int) (*value, error) {
	out := &value{kind: kindObject, members: make([]member, 0, len(v.members))}
	done := map[string]bool{keySchemaVersion: true}
	if !keepType {
		done[keyType] = true
	}
	add := func(key string, item *value) error {
		done[key] = true
		p, _ := s.Properties.Lookup(key)
		switch {
		case p.Schema != nil && p.Schema.UI == schema.UIActions:
			children, err := x.actions(item, path)
			if err != nil {
				return err
			}
			item = children
		case p.Schema != nil && p.Schema.Pattern == schema.PatternID:
			name, err := x.name(item.text, p.Schema.UI)
			if err != nil {
				if len(path) > 0 {
					return fmt.Errorf("action %s: %s: %w", positionOf(path), key, err)
				}
				return fmt.Errorf("%s: %w", key, err)
			}
			item = &value{kind: kindString, text: name}
		case p.Schema != nil && item.kind == kindObject && len(p.Schema.Properties) > 0:
			nested, err := x.fileFormAt(item, p.Schema, true, path)
			if err != nil {
				return err
			}
			item = nested
		case p.Schema != nil && item.kind == kindArray && p.Schema.Items != nil && len(p.Schema.Items.Properties) > 0:
			list := &value{kind: kindArray, items: make([]*value, len(item.items))}
			for i, entry := range item.items {
				nested, err := x.fileFormAt(entry, p.Schema.Items, true, path)
				if err != nil {
					return err
				}
				list.items[i] = nested
			}
			item = list
		}
		out.members = append(out.members, member{key: key, value: item})
		return nil
	}
	for _, p := range s.Properties {
		if m, ok := v.lookup(p.Name); ok && !done[p.Name] {
			if err := add(p.Name, m.value); err != nil {
				return nil, err
			}
		}
	}
	for _, m := range v.members {
		if !done[m.key] {
			if err := add(m.key, m.value); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// name returns the name of the object with the ID text that a field shown
// as ui refers to (B23, B64).
func (x *exporter) name(text string, ui schema.UI) (string, error) {
	var sp space
	switch ui {
	case schema.UICommand:
		sp = spaceCommand
	case schema.UIGroup:
		sp = spaceGroup
	case schema.UICooldownGroup:
		sp = spaceCooldownGroup
	default:
		return "", fmt.Errorf("refers to the %s %s, which cannot be written as a name yet", ui, text)
	}
	ref, err := id.Parse(text)
	if err != nil {
		return "", err
	}
	name, ok := x.names[sp][ref]
	if !ok {
		return "", fmt.Errorf("refers to the %s %s, which does not exist", sp, ref)
	}
	return name, nil
}

// cooldownGroupDoc returns the document of a cooldown group (B21).
func cooldownGroupDoc(g command.CooldownGroup) Exported {
	return document(KindCooldownGroup, g.Name, []member{text("name", g.Name)}, []member{text("duration", g.Duration.String())})
}

// groupDoc returns the document of a command group (B20); without an own
// timer interval it has no member for it.
func groupDoc(g command.Group) Exported {
	var spec []member
	if g.TimerInterval > 0 {
		spec = append(spec, text("timerInterval", g.TimerInterval.String()))
	}
	return document(KindCommandGroup, g.Name, []member{text("name", g.Name)}, spec)
}

// document returns a document with apiVersion, kind, metadata and spec in
// this order (B1, B36).
func document(k Kind, name string, meta, spec []member) Exported {
	if spec == nil {
		spec = []member{}
	}
	return Exported{Kind: k, Name: name, root: &value{kind: kindObject, members: []member{
		text("apiVersion", APIVersion),
		text("kind", string(k)),
		{key: "metadata", value: &value{kind: kindObject, members: meta}},
		{key: "spec", value: &value{kind: kindObject, members: spec}},
	}}}
}

// kindOf returns the kind of documents of commands of kind k.
func kindOf(k command.Kind) Kind {
	for _, kind := range Kinds() {
		if ck, ok := kind.CommandKind(); ok && ck == k {
			return kind
		}
	}
	return ""
}

// text returns a member with text.
func text(key, s string) member {
	return member{key: key, value: &value{kind: kindString, text: s}}
}

// boolean returns a member with a truth value.
func boolean(key string, b bool) member {
	return member{key: key, value: &value{kind: kindBool, boolean: b}}
}

// sortedByName returns list sorted by name, regardless of case first.
func sortedByName[T any](list []T, name func(T) string) []T {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b T) int { return compareNames(name(a), name(b)) })
	return out
}

// compareNames orders names regardless of case, then by their spelling.
func compareNames(a, b string) int {
	return cmp.Or(cmp.Compare(command.NameKey(a), command.NameKey(b)), cmp.Compare(a, b))
}

// parseJSON reads a list of stored documents into a value; the codec
// always writes a list, also an empty one.
func parseJSON(data []byte) (*value, error) {
	docs, problems := readJSON("stored", data)
	if len(problems) > 0 {
		return nil, errors.New(problems[0].Message)
	}
	out := &value{kind: kindArray, items: make([]*value, len(docs))}
	for i, d := range docs {
		out.items[i] = d.root
	}
	return out, nil
}

// FileNames returns the names of the files of docs for an export with
// --dir (B38): kind and name in lowercase, every character but letters and
// digits as "-", and the version of the format, e.g.
// "chat-command-hug.v1alpha1.yaml"; names that would be equal get a
// number, e.g. "chat-command-hug-2.v1alpha1.yaml". ext is ".yaml" or
// ".json".
func FileNames(docs []Exported, ext string) []string {
	out := make([]string, len(docs))
	used := map[string]int{}
	for i, d := range docs {
		stem := fileStem(d)
		used[stem]++
		if n := used[stem]; n > 1 {
			stem += "-" + strconv.Itoa(n)
		}
		out[i] = stem + "." + strings.TrimPrefix(APIVersion, "streamcrew/") + ext
	}
	return out
}

// fileStem returns kind and name of d as in a file name.
func fileStem(d Exported) string {
	var b strings.Builder
	for i, r := range string(d.Kind) {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('-')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	b.WriteByte('-')
	for _, r := range strings.ToLower(d.Name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
