// SPDX-License-Identifier: MIT

package commandfile

import (
	"slices"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/event"
)

// Types are the action and requirement types that documents may use, and
// the codec that decodes them.
type Types struct {
	// Actions are the action types as an action.Registry prepared them.
	Actions []action.Descriptor
	// Codec decodes actions of these types and the requirement types.
	Codec *command.Codec
}

// Named is an object of the profile: its ID and its name.
type Named struct {
	ID   id.ID
	Name string
}

// Existing are the commands, command groups and cooldown groups of the
// profile, which documents replace by name and refer to (B22 to B24, B33).
type Existing struct {
	Commands       []Named
	Groups         []Named
	CooldownGroups []Named
}

// Plan is what documents describe, in the order of an import (B34):
// cooldown groups, command groups, commands, each in the order of the
// documents.
type Plan struct {
	CooldownGroups []Planned[command.CooldownGroup]
	Groups         []Planned[command.Group]
	Commands       []Planned[command.Command]
}

// Planned is an object of a plan with the document it comes from.
type Planned[T any] struct {
	Doc   Document
	Value T
	// Replaces reports whether the profile has the object by name; it keeps
	// its ID (B33).
	Replaces bool
}

// space is where names are unique: the four kinds of commands share one
// (commands.md, B7).
type space string

// The spaces of names.
const (
	spaceCommand       space = "command"
	spaceGroup         space = "command group"
	spaceCooldownGroup space = "cooldown group"
)

// spaceOf returns the space of names of kind k.
func spaceOf(k Kind) space {
	switch k {
	case KindCommandGroup:
		return spaceGroup
	case KindCooldownGroup:
		return spaceCooldownGroup
	default:
		return spaceCommand
	}
}

// header is what every document has (B1 to B3).
type header struct {
	kind Kind
	name string
	// group is metadata.group of a command; nil without one.
	group *value
	spec  *value
	id    id.ID
	// replaces reports whether the profile has the object by name.
	replaces bool
}

// conversion converts the documents of one plan.
type conversion struct {
	types        Types
	descriptors  map[string]action.Descriptor
	requirements map[string]command.RequirementDescriptor
	reqOrder     []string
	// ids are the IDs by space and name key: of the documents, else of the
	// profile.
	ids map[space]map[string]id.ID
}

// Convert turns documents into the objects of a plan (B1 to B5, B10 to
// B14, B20 to B24). Names become IDs, of the documents first, then of the
// profile; an object the profile has by name keeps its ID, a new one gets
// a new ID (B33). Duplicate names are reported as Duplicates does (B60).
//
// Each document with a problem is left out of the plan, and the first
// problem of each is reported (B32). The Go code of the types checks the
// documents, as it checks stored ones (Code-ADR-0013, points 6 and 7); the
// checks that need the profile, e.g. conflicting triggers, come with
// saving.
func Convert(docs []Document, existing Existing, types Types) (Plan, []Problem) {
	c := &conversion{
		types:        types,
		descriptors:  make(map[string]action.Descriptor, len(types.Actions)),
		requirements: map[string]command.RequirementDescriptor{},
		ids:          map[space]map[string]id.ID{spaceCommand: {}, spaceGroup: {}, spaceCooldownGroup: {}},
	}
	for _, d := range types.Actions {
		c.descriptors[d.Type] = d
	}
	for _, d := range command.RequirementCatalog() {
		c.requirements[d.Type] = d
		c.reqOrder = append(c.reqOrder, d.Type)
	}
	known := map[space]map[string]id.ID{spaceCommand: {}, spaceGroup: {}, spaceCooldownGroup: {}}
	for sp, list := range map[space][]Named{spaceCommand: existing.Commands, spaceGroup: existing.Groups, spaceCooldownGroup: existing.CooldownGroups} {
		for _, n := range list {
			known[sp][command.NameKey(n.Name)] = n.ID
			c.ids[sp][command.NameKey(n.Name)] = n.ID
		}
	}

	var problems []Problem
	failed := map[int]bool{}
	headers := make([]header, len(docs))
	for i, d := range docs {
		r := &reader{doc: d}
		headers[i] = r.header()
		if r.err != nil {
			problems = append(problems, *r.err)
			failed[i] = true
			continue
		}
		h := &headers[i]
		sp, key := spaceOf(h.kind), command.NameKey(h.name)
		h.id, h.replaces = known[sp][key]
		if !h.replaces {
			h.id = id.New()
		}
		c.ids[sp][key] = h.id
	}
	for _, p := range Duplicates(docs) {
		problems = append(problems, p)
		for i, d := range docs {
			if d.File == p.File && sameName(d, p) {
				failed[i] = true
			}
		}
	}

	var plan Plan
	for i, d := range docs {
		if failed[i] {
			continue
		}
		r := &reader{doc: d}
		h := headers[i]
		switch h.kind {
		case KindCooldownGroup:
			g := c.cooldownGroup(r, h)
			if r.err == nil {
				plan.CooldownGroups = append(plan.CooldownGroups, Planned[command.CooldownGroup]{Doc: d, Value: g, Replaces: h.replaces})
			}
		case KindCommandGroup:
			g := c.group(r, h)
			if r.err == nil {
				plan.Groups = append(plan.Groups, Planned[command.Group]{Doc: d, Value: g, Replaces: h.replaces})
			}
		default:
			cmd := c.command(r, h)
			if r.err == nil {
				plan.Commands = append(plan.Commands, Planned[command.Command]{Doc: d, Value: cmd, Replaces: h.replaces})
			}
		}
		if r.err != nil {
			problems = append(problems, *r.err)
		}
	}
	return plan, problems
}

// sameName reports whether the problem p of Duplicates is about the name
// of d.
func sameName(d Document, p Problem) bool {
	meta, ok := d.root.lookup("metadata")
	if !ok {
		return false
	}
	m, ok := meta.value.lookup("name")
	return ok && m.value.pos.line == p.Line && m.value.pos.column == p.Column
}

// header reads apiVersion, kind and metadata, and that spec is an object
// (B1 to B3).
func (r *reader) header() header {
	root := r.doc.root
	r.members(root, "", "apiVersion", "kind", "metadata", "spec")
	var h header
	if v := r.require(root, "", "apiVersion"); v != nil && v.text != APIVersion {
		r.fail(v.pos, "apiVersion", "must be "+oneOfValues([]string{APIVersion}))
	}
	if v := r.require(root, "", "kind"); v != nil {
		names := make([]string, len(Kinds()))
		for i, k := range Kinds() {
			names[i] = string(k)
		}
		if v.kind != kindString || !slices.Contains(names, v.text) {
			r.fail(v.pos, "kind", "must be "+oneOfValues(names))
		} else {
			h.kind = Kind(v.text)
		}
	}
	meta := r.require(root, "", "metadata")
	h.spec = r.require(root, "", "spec")
	if r.err != nil {
		return h
	}
	_, isCommand := h.kind.CommandKind()
	if isCommand {
		r.members(meta, "metadata", "name", "group")
	} else {
		r.members(meta, "metadata", "name")
	}
	h.name = r.text(meta, "metadata", "name", true, "")
	if m, ok := meta.lookup("group"); ok && r.err == nil {
		if m.value.kind != kindString {
			r.fail(m.value.pos, "metadata.group", "must be text, not "+describe(m.value))
		}
		h.group = m.value
	}
	if r.err == nil {
		// The names of commands, groups and cooldown groups follow one rule
		// (commands.md, B1, B30, B33); Group.Validate checks no more.
		if err := (command.Group{Name: h.name}).Validate(); err != nil {
			nameVal, _ := meta.lookup("name")
			r.fail(nameVal.value.pos, "metadata.name", trimInvalid(err))
		}
	}
	return h
}

// cooldownGroup converts a cooldown group (B21).
func (c *conversion) cooldownGroup(r *reader, h header) command.CooldownGroup {
	r.members(h.spec, "spec", "duration")
	g := command.CooldownGroup{ID: h.id, Name: h.name, Duration: r.duration(h.spec, "spec", "duration", true)}
	if r.err == nil {
		if err := g.Validate(); err != nil {
			m, _ := h.spec.lookup("duration")
			r.fail(m.value.pos, "spec.duration", trimInvalid(err))
		}
	}
	return g
}

// group converts a command group (B20).
func (c *conversion) group(r *reader, h header) command.Group {
	r.members(h.spec, "spec", "timerInterval")
	g := command.Group{ID: h.id, Name: h.name, TimerInterval: r.duration(h.spec, "spec", "timerInterval", false)}
	if r.err == nil {
		if err := g.Validate(); err != nil {
			m, _ := h.spec.lookup("timerInterval")
			r.fail(m.value.pos, "spec.timerInterval", trimInvalid(err))
		}
	}
	return g
}

// The members of the spec of every command (B10).
func commandMembers() []string {
	return []string{"enabled", "unlocked", "errorPolicy", "requirements", "actions"}
}

// command converts a command (B10 to B14, B23, B24).
func (c *conversion) command(r *reader, h header) command.Command {
	kind, _ := h.kind.CommandKind()
	spec := h.spec
	cmd := command.Command{ID: h.id, Name: h.name, Kind: kind}
	switch kind {
	case command.KindChat:
		r.members(spec, "spec", append([]string{"triggers", "triggerMode"}, commandMembers()...)...)
		cmd.Triggers = r.texts(spec, "spec", "triggers", true)
		cmd.TriggerMode = command.TriggerMode(r.text(spec, "spec", "triggerMode", false, string(DefaultTriggerMode)))
		valid(r, spec, "spec", "triggerMode", cmd.TriggerMode.Valid(), command.TriggerExclamation, command.TriggerLiteral, command.TriggerWildcard)
	case command.KindEvent:
		r.members(spec, "spec", append([]string{"event"}, commandMembers()...)...)
		cmd.Event = event.Type(r.text(spec, "spec", "event", true, ""))
		if _, ok := eventtype.Lookup(cmd.Event); !ok && r.err == nil {
			m, _ := spec.lookup("event")
			r.fail(m.value.pos, "spec.event", "unknown event type")
		}
	case command.KindTimer, command.KindActionGroup:
		r.members(spec, "spec", commandMembers()...)
	}
	cmd.Enabled = r.boolean(spec, "spec", "enabled", DefaultEnabled)
	cmd.Unlocked = r.boolean(spec, "spec", "unlocked", DefaultUnlocked)
	cmd.ErrorPolicy = command.ErrorPolicy(r.text(spec, "spec", "errorPolicy", false, string(DefaultErrorPolicy)))
	valid(r, spec, "spec", "errorPolicy", cmd.ErrorPolicy.Valid(), command.ErrorContinue, command.ErrorAbort)
	if h.group != nil && r.err == nil {
		groupID, ok := c.ids[spaceGroup][command.NameKey(h.group.text)]
		if !ok {
			r.fail(h.group.pos, "metadata.group", "no command group is named "+quote(h.group.text))
		}
		cmd.GroupID = groupID
	}
	cmd.Requirements = c.requirementList(r, spec)
	cmd.Actions = []command.Action{}
	if m, ok := spec.lookup("actions"); ok && r.err == nil {
		_, cmd.Actions = c.actions(r, m.value, "spec.actions")
	}
	if r.err == nil {
		if err := cmd.Validate(); err != nil {
			r.fail(spec.pos, "spec", trimInvalid(err))
		}
	}
	return cmd
}

// requirementList converts the map of requirements, in the order of the
// catalog (B12, B14).
func (c *conversion) requirementList(r *reader, spec *value) []command.Requirement {
	list := []command.Requirement{}
	m, ok := spec.lookup("requirements")
	if !ok || r.err != nil {
		return list
	}
	reqs := m.value
	if reqs.kind != kindObject {
		r.fail(reqs.pos, "spec.requirements", "must be an object, not "+describe(reqs))
		return list
	}
	for _, mm := range reqs.members {
		if _, known := c.requirements[mm.key]; !known {
			r.fail(mm.keyPos, join("spec.requirements", mm.key), "unknown requirement type")
			return list
		}
	}
	for _, typ := range c.reqOrder {
		mm, ok := reqs.lookup(typ)
		if !ok {
			continue
		}
		path := join("spec.requirements", typ)
		if mm.value.kind != kindObject {
			r.fail(mm.value.pos, path, "must be an object, not "+describe(mm.value))
			return list
		}
		if req := c.requirement(r, mm.value, path, c.requirements[typ]); req != nil {
			list = append(list, req)
		}
		if r.err != nil {
			return list
		}
	}
	return list
}

// requirement converts one requirement of type d with the members v.
func (c *conversion) requirement(r *reader, v *value, path string, d command.RequirementDescriptor) command.Requirement {
	for _, key := range []string{keyType, keySchemaVersion} {
		if m, ok := v.lookup(key); ok {
			r.fail(m.keyPos, join(path, key), "unknown member")
			return nil
		}
	}
	resolved := c.resolve(r, v, d.Schema, path)
	if r.err != nil {
		return nil
	}
	doc := &value{kind: kindObject, members: append([]member{{key: keyType, value: &value{kind: kindString, text: d.Type}}}, resolved.members...)}
	data, err := doc.JSON()
	if err != nil {
		r.fail(v.pos, path, err.Error())
		return nil
	}
	req, err := c.types.Codec.Requirement(data)
	if err != nil {
		r.decodeFailed(v, path, err)
		return nil
	}
	if err := req.Validate(); err != nil {
		r.fail(v.pos, path, cleanMessage(err))
		return nil
	}
	return req
}

// The members of documents that files leave out (B12, B13).
const (
	keyType          = "type"
	keySchemaVersion = "schemaVersion"
)

// actions converts a list of actions at path and returns it with its names
// replaced by IDs (B13, B14, B23). Child actions are converted before
// their parents, so that a problem is reported at the action that has it.
func (c *conversion) actions(r *reader, list *value, path string) (*value, []command.Action) {
	if list.kind != kindArray {
		r.fail(list.pos, path, "must be a list, not "+describe(list))
		return nil, nil
	}
	out := &value{kind: kindArray, pos: list.pos, items: make([]*value, 0, len(list.items))}
	acts := make([]command.Action, 0, len(list.items))
	for i, item := range list.items {
		resolved, a := c.action(r, item, path+"["+itoa(i)+"]")
		if r.err != nil {
			return nil, nil
		}
		out.items = append(out.items, resolved)
		acts = append(acts, a)
	}
	return out, acts
}

// action converts one action at path.
func (c *conversion) action(r *reader, v *value, path string) (*value, command.Action) {
	if v.kind != kindObject {
		r.fail(v.pos, path, "must be an object, not "+describe(v))
		return nil, nil
	}
	typ := r.text(v, path, keyType, true, "")
	if r.err != nil {
		return nil, nil
	}
	d, ok := c.descriptors[typ]
	if !ok {
		m, _ := v.lookup(keyType)
		r.fail(m.value.pos, join(path, keyType), "unknown action type")
		return nil, nil
	}
	if m, ok := v.lookup(keySchemaVersion); ok {
		r.fail(m.keyPos, join(path, keySchemaVersion), "unknown member")
		return nil, nil
	}
	resolved := c.resolve(r, v, d.Schema, path)
	if r.err != nil {
		return nil, nil
	}
	data, err := resolved.JSON()
	if err != nil {
		r.fail(v.pos, path, err.Error())
		return nil, nil
	}
	a, err := c.types.Codec.Action(data)
	if err != nil {
		r.decodeFailed(v, path, err)
		return nil, nil
	}
	if err := a.Validate(); err != nil {
		r.fail(v.pos, path, cleanMessage(err))
		return nil, nil
	}
	return resolved, a
}

// resolve returns a copy of the object v at path with the names of its
// references replaced by IDs (B23, B24). The schema of the stored form, s,
// shows where references are: fields with the pattern of IDs, named by
// their UI hint. Lists of child actions are converted as actions.
// References stand only among the members of v; TestReferencesAtTop finds
// a type that has one deeper.
func (c *conversion) resolve(r *reader, v *value, s *schema.Schema, path string) *value {
	out := &value{kind: v.kind, pos: v.pos, members: make([]member, 0, len(v.members))}
	for _, m := range v.members {
		at := join(path, m.key)
		p, ok := s.Properties.Lookup(m.key)
		item := m.value
		switch {
		case !ok:
		case p.Schema.UI == schema.UIActions:
			item, _ = c.actions(r, m.value, at)
		case p.Schema.Pattern == schema.PatternID:
			item = c.reference(r, m.value, at, p.Schema.UI)
		}
		if r.err != nil {
			return out
		}
		out.members = append(out.members, member{key: m.key, keyPos: m.keyPos, value: item})
	}
	return out
}

// reference returns the ID that the name v refers to as a value; ui says
// what it refers to.
func (c *conversion) reference(r *reader, v *value, path string, ui schema.UI) *value {
	if v.kind != kindString {
		r.fail(v.pos, path, "must be a name, not "+describe(v))
		return v
	}
	var sp space
	what := map[schema.UI]string{schema.UICurrency: "currency", schema.UIRank: "rank", schema.UIItem: "item"}[ui]
	switch ui {
	case schema.UICommand:
		sp = spaceCommand
	case schema.UIGroup:
		sp = spaceGroup
	case schema.UICooldownGroup:
		sp = spaceCooldownGroup
	default:
		if what == "" {
			what = string(ui)
		}
		r.fail(v.pos, path, "no "+what+" is named "+quote(v.text))
		return v
	}
	found, ok := c.ids[sp][command.NameKey(v.text)]
	if !ok {
		r.fail(v.pos, path, "no "+string(sp)+" is named "+quote(v.text))
		return v
	}
	return &value{kind: kindString, pos: v.pos, text: found.String()}
}
