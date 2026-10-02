// SPDX-License-Identifier: MIT

package action_test

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/template"
)

// probe is an action type with a template, an amount and a result name.
type probe struct {
	action.Common `json:",embed"`
	Message       action.Template   `json:"message"`
	Seconds       action.Amount     `json:"seconds,omitzero"`
	Result        action.ResultName `json:"result"`
}

// secondsRange is the range of probe.Seconds.
func secondsRange() action.Range { return action.Range{Min: 0, Max: 3600} }

func (probe) DocType() string { return "probe" }

func (p probe) Validate() error {
	if err := p.Seconds.Validate(secondsRange()); err != nil {
		return fmt.Errorf("seconds: %w", err)
	}
	return p.Result.Validate()
}

func probeType() action.Descriptor {
	return action.Descriptor{
		Type:         "probe",
		Version:      1,
		Category:     action.CategoryFlow,
		Capabilities: []capability.Capability{capability.NetOutbound},
		Schema: schema.Document(
			schema.Property{Name: "message", Schema: schema.Template()},
			schema.Property{Name: "seconds", Schema: secondsRange().Schema(), Required: true},
			schema.Property{Name: "result", Schema: schema.ResultName()},
		),
	}.WithNew(func() probe { return probe{Common: action.On(), Message: "hi", Result: "out"} })
}

// box is an action type with child actions.
type box struct {
	action.Common `json:",embed"`
	Actions       []command.Action `json:"actions"`
}

func (box) DocType() string              { return "box" }
func (box) Validate() error              { return nil }
func (b box) Children() []command.Action { return b.Actions }

func boxType() action.Descriptor {
	return action.Descriptor{
		Type:     "box",
		Version:  1,
		Category: action.CategoryFlow,
		Schema:   schema.Document(schema.Property{Name: "actions", Schema: schema.Actions()}),
	}.WithNew(func() box { return box{Common: action.On(), Actions: []command.Action{}} })
}

// switcher is an action type with kinds; only kind "a" has the amount n.
type switcher struct {
	action.Common `json:",embed"`
	Kind          string        `json:"kind"`
	N             action.Amount `json:"n,omitzero"`
}

func (switcher) DocType() string { return "switcher" }

func (s switcher) Validate() error {
	switch s.Kind {
	case "a":
		return s.N.Validate(action.Range{Min: 1, Max: 10, Integer: true})
	case "b":
		if !s.N.IsZero() {
			return errors.New("kind b has no n")
		}
		return nil
	default:
		return fmt.Errorf("unknown kind %q", s.Kind)
	}
}

func switcherType() action.Descriptor {
	return action.Descriptor{
		Type:     "switcher",
		Version:  1,
		Category: action.CategoryModeration,
		Schema: schema.Kinds(nil,
			schema.Variant{Kind: "a", Props: []schema.Property{
				{Name: "n", Schema: action.Range{Min: 1, Max: 10, Integer: true}.Schema(), Required: true},
			}},
			schema.Variant{Kind: "b"},
		),
	}.WithNew(func() switcher { return switcher{Common: action.On(), Kind: "b"} })
}

// kinded is an action type with kinds and per-kind defaults: only kind
// "loud" has the options in Loud, and a new one is loud with Volume 7.
type kinded struct {
	action.Common `json:",embed"`
	Kind          string       `json:"kind"`
	Loud          *loudOptions `json:",embed"`
}

// loudOptions are the members of the kind "loud".
type loudOptions struct {
	Volume int    `json:"volume"`
	Text   string `json:"text"`
}

func (kinded) DocType() string { return "kinded" }

func (k kinded) Validate() error {
	if (k.Kind == "loud") != (k.Loud != nil) {
		return fmt.Errorf("%w: only kind loud has its options", action.ErrInvalid)
	}
	return nil
}

func kindedType() action.Descriptor {
	return action.Descriptor{
		Type:     "kinded",
		Version:  1,
		Category: action.CategoryChat,
		Schema: schema.Kinds(nil,
			schema.Variant{Kind: "loud", Props: []schema.Property{
				{Name: "volume", Schema: &schema.Schema{Type: "integer"}},
				{Name: "text", Schema: schema.Template(), Required: true},
			}},
			schema.Variant{Kind: "quiet"},
		),
	}.WithKinds("loud", func(kind string) (kinded, bool) {
		switch kind {
		case "loud":
			return kinded{Common: action.On(), Kind: kind, Loud: &loudOptions{Volume: 7}}, true
		case "quiet":
			return kinded{Common: action.On(), Kind: kind}, true
		default:
			return kinded{}, false
		}
	})
}

// renamed is at version 2: version 1 called "message" "text".
type renamed struct {
	action.Common `json:",embed"`
	Message       action.Template `json:"message"`
}

func (renamed) DocType() string { return "renamed" }
func (renamed) Validate() error { return nil }

func renamedType() action.Descriptor {
	return action.Descriptor{
		Type:     "renamed",
		Version:  2,
		Category: action.CategoryChat,
		Schema:   schema.Document(schema.Property{Name: "message", Schema: schema.Template()}),
		Migrations: []polydoc.Migration{func(doc map[string]jsontext.Value) error {
			doc["message"] = doc["text"]
			delete(doc, "text")
			return nil
		}},
	}.WithNew(func() renamed { return renamed{Common: action.On()} })
}

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

func TestConformance(t *testing.T) {
	t.Parallel()
	t.Run("probe", func(t *testing.T) {
		t.Parallel()
		actiontest.Suite{Descriptor: probeType(), Update: update(), Examples: []actiontest.Example{
			{Name: "fixed", Doc: `{"type":"probe","enabled":true,"message":"Hi $username","seconds":1.5,"result":"x1"}`, Valid: true},
			{Name: "expression", Doc: `{"type":"probe","schemaVersion":1,"seconds":"$arg1text * 2"}`, Valid: true},
			{Name: "defaults only", Doc: `{"type":"probe","seconds":0}`, Valid: true},
			{Name: "seconds missing", Doc: `{"type":"probe","message":"x"}`},
			{Name: "seconds out of range", Doc: `{"type":"probe","seconds":3601}`},
			{Name: "seconds empty", Doc: `{"type":"probe","seconds":""}`},
			{Name: "seconds wrong type", Doc: `{"type":"probe","seconds":true}`},
			{Name: "result name", Doc: `{"type":"probe","seconds":1,"result":"Bad Name"}`},
			{Name: "unknown member", Doc: `{"type":"probe","seconds":1,"mesage":"typo"}`},
			{Name: "switch not a bool", Doc: `{"type":"probe","seconds":1,"enabled":"yes"}`},
		}}.Run(t)
	})
	t.Run("box", func(t *testing.T) {
		t.Parallel()
		actiontest.Suite{Descriptor: boxType(), Update: update(), Examples: []actiontest.Example{
			{Name: "nested", Doc: `{"type":"box","actions":[{"type":"box","enabled":false,"actions":[]},{"type":"obs_scene","scene":"BRB"}]}`, Valid: true},
			{Name: "empty", Doc: `{"type":"box","actions":[]}`, Valid: true},
			{Name: "child without type", Doc: `{"type":"box","actions":[{"scene":"BRB"}]}`},
			{Name: "child not an object", Doc: `{"type":"box","actions":[7]}`},
		}}.Run(t)
	})
	t.Run("switcher", func(t *testing.T) {
		t.Parallel()
		actiontest.Suite{Descriptor: switcherType(), Update: update(), Examples: []actiontest.Example{
			{Name: "a", Doc: `{"type":"switcher","kind":"a","n":3}`, Valid: true},
			{Name: "b", Doc: `{"type":"switcher","kind":"b"}`, Valid: true},
			{Name: "a without n", Doc: `{"type":"switcher","kind":"a"}`},
			{Name: "a with fraction", Doc: `{"type":"switcher","kind":"a","n":2.5}`},
			{Name: "b with n", Doc: `{"type":"switcher","kind":"b","n":3}`},
			{Name: "unknown kind", Doc: `{"type":"switcher","kind":"c"}`},
			{Name: "kind missing", Doc: `{"type":"switcher"}`},
		}}.Run(t)
	})
	t.Run("kinded", func(t *testing.T) {
		t.Parallel()
		actiontest.Suite{Descriptor: kindedType(), Update: update(), Examples: []actiontest.Example{
			{Name: "loud", Doc: `{"type":"kinded","kind":"loud","volume":3,"text":"hi"}`, Valid: true},
			{Name: "loud with defaults", Doc: `{"type":"kinded","kind":"loud","text":"hi"}`, Valid: true},
			{Name: "quiet", Doc: `{"type":"kinded","kind":"quiet"}`, Valid: true},
			{Name: "quiet with a member of loud", Doc: `{"type":"kinded","kind":"quiet","volume":3}`},
			{Name: "loud without text", Doc: `{"type":"kinded","kind":"loud"}`},
			{Name: "unknown kind", Doc: `{"type":"kinded","kind":"silent"}`},
			{Name: "kind missing", Doc: `{"type":"kinded","text":"hi"}`},
			{Name: "unknown member", Doc: `{"type":"kinded","kind":"quiet","pitch":"high"}`},
		}}.Run(t)
	})
	t.Run("renamed", func(t *testing.T) {
		t.Parallel()
		actiontest.Suite{Descriptor: renamedType(), Update: update(), Examples: []actiontest.Example{
			{Name: "current", Doc: `{"type":"renamed","message":"hello"}`, Valid: true},
			{Name: "member of version 1", Doc: `{"type":"renamed","text":"hello"}`},
		}}.Run(t)
	})
}

// TestSchemaOfRegistry: the registry binds the member "type" and takes the
// defaults from New (Code-ADR-0013, point 3).
func TestSchemaOfRegistry(t *testing.T) {
	t.Parallel()
	reg, err := action.NewRegistry(capability.Set{}, probeType())
	require.NoError(t, err)
	d, ok := reg.Descriptor("probe")
	require.True(t, ok)
	data, err := d.Schema.JSON()
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"type": {"type": "string", "const": "probe"},
			"schemaVersion": {"type": "integer", "minimum": 1},
			"enabled": {"type": "boolean", "default": true, "x-ui": "switch"},
			"message": {"type": "string", "default": "hi", "x-ui": "template"},
			"seconds": {"oneOf": [
				{"type": "number", "minimum": 0, "maximum": 3600},
				{"type": "string", "minLength": 1, "x-ui": "expression"}
			], "x-ui": "amount"},
			"result": {"type": "string", "pattern": "^[a-z0-9]+$", "default": "out", "x-ui": "result_name"}
		},
		"required": ["type", "seconds"],
		"additionalProperties": false
	}`, string(data))
	assert.Contains(t, string(data), `"properties":{"type":`, "members in the order editors show them")

	plain := probeType()
	assert.Empty(t, plain.Schema.Properties[0].Schema.Const, "the registry works on a copy")
}

// TestRegistryRejects: the registry checks every descriptor
// (Code-ADR-0013, point 3).
func TestRegistryRejects(t *testing.T) {
	t.Parallel()
	with := func(change func(*action.Descriptor)) action.Descriptor {
		d := probeType()
		change(&d)
		return d
	}
	for name, d := range map[string]action.Descriptor{
		"type with a dot":     with(func(d *action.Descriptor) { d.Type = "chat.send" }),
		"type in capitals":    with(func(d *action.Descriptor) { d.Type = "Probe" }),
		"type with __":        with(func(d *action.Descriptor) { d.Type = "a__b" }),
		"type too long":       with(func(d *action.Descriptor) { d.Type = strings.Repeat("a", 41) }),
		"unknown category":    with(func(d *action.Descriptor) { d.Category = "misc" }),
		"unknown capability":  with(func(d *action.Descriptor) { d.Capabilities = []capability.Capability{"host:root"} }),
		"no schema":           with(func(d *action.Descriptor) { d.Schema = nil }),
		"no constructor":      with(func(d *action.Descriptor) { d.New = nil }),
		"no decode":           with(func(d *action.Descriptor) { d.Decode = nil }),
		"version zero":        with(func(d *action.Descriptor) { d.Version = 0 }),
		"New of another type": with(func(d *action.Descriptor) { d.New = boxType().New }),
		"member not in schema": with(func(d *action.Descriptor) {
			d.Schema = schema.Document(schema.Property{Name: "seconds", Schema: secondsRange().Schema(), Required: true})
		}),
		"unknown UI hint": with(func(d *action.Descriptor) {
			d.Schema.Properties = append(d.Schema.Properties, schema.Property{Name: "x", Schema: schema.Text("slider")})
		}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := action.NewRegistry(capability.Set{}, d)
			assert.Error(t, err)
		})
	}

	_, err := action.NewRegistry(capability.Set{}, probeType(), probeType())
	require.ErrorIs(t, err, action.ErrInvalid, "a type twice")
}

// TestRegistry covers the lists of the registry and the port of the engine.
func TestRegistry(t *testing.T) {
	t.Parallel()
	visual := switcherType()
	visual.VisualAudio = true
	reg, err := action.NewRegistry(capability.Set{}, probeType(), boxType(), visual)
	require.NoError(t, err)

	var types []string
	for _, d := range reg.Descriptors() {
		types = append(types, d.Type)
	}
	assert.Equal(t, []string{"box", "probe", "switcher"}, types)
	require.Len(t, reg.Entries(), 3)
	assert.Equal(t, "box", reg.Entries()[0].Type)

	assert.True(t, reg.VisualAudio("switcher"))
	assert.False(t, reg.VisualAudio("probe"))
	assert.Equal(t, []capability.Capability{capability.NetOutbound}, reg.Missing("probe"))
	assert.Equal(t, []capability.Capability{}, reg.Missing("box"))

	granted, err := capability.NewSet(capability.NetOutbound)
	require.NoError(t, err)
	reg, err = action.NewRegistry(granted, probeType())
	require.NoError(t, err)
	assert.Equal(t, []capability.Capability{}, reg.Missing("probe"))

	codec, err := command.NewCodec(reg.Entries()...)
	require.NoError(t, err, "the codec of the commands takes the entries")
	_ = codec
}

// TestDecodeStartsFromNew covers Code-ADR-0013, point 4: members missing in
// a handwritten document keep the defaults of a new action.
func TestDecodeStartsFromNew(t *testing.T) {
	t.Parallel()
	d := probeType()
	a, err := d.Decode([]byte(`{"seconds":"5"}`), json.DefaultOptionsV2())
	require.NoError(t, err)
	assert.Equal(t, probe{Common: action.On(), Message: "hi", Seconds: action.Expression("5"), Result: "out"}, a)

	_, err = d.Decode([]byte(`{"message":"x"}`), json.DefaultOptionsV2())
	require.ErrorIs(t, err, action.ErrInvalid, "a required member is missing")
}

// TestDecodeKinds covers Code-ADR-0013, point 4, for types with kinds:
// decoding starts from the defaults of the kind in the document.
func TestDecodeKinds(t *testing.T) {
	t.Parallel()
	d := kindedType()
	assert.Equal(t, kinded{Common: action.On(), Kind: "loud", Loud: &loudOptions{Volume: 7}}, d.New(), "a new one is of the first kind")

	a, err := d.Decode([]byte(`{"kind":"quiet"}`), json.DefaultOptionsV2())
	require.NoError(t, err)
	assert.Equal(t, kinded{Common: action.On(), Kind: "quiet"}, a, "no default of the kind loud slips in")

	a, err = d.Decode([]byte(`{"kind":"loud","text":"hi"}`), json.DefaultOptionsV2())
	require.NoError(t, err)
	assert.Equal(t, kinded{Common: action.On(), Kind: "loud", Loud: &loudOptions{Volume: 7, Text: "hi"}}, a)

	_, err = d.Decode([]byte(`{"kind":"loud"}`), json.DefaultOptionsV2())
	require.ErrorContains(t, err, `member "text" is missing`, "the kind requires it")
	_, err = d.Decode([]byte(`{"text":"hi"}`), json.DefaultOptionsV2())
	require.ErrorContains(t, err, `member "kind" is missing`)
	_, err = d.Decode([]byte(`{"kind":"silent"}`), json.DefaultOptionsV2())
	require.ErrorContains(t, err, `unknown kind "silent"`)
	_, err = d.Decode([]byte(`{"kind":7}`), json.DefaultOptionsV2())
	require.Error(t, err)
}

func TestAmountJSON(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]action.Amount{
		`1.50`:         action.Fixed(1.5),
		`-3`:           action.Fixed(-3),
		`"$arg1text"`:  action.Expression("$arg1text"),
		`"2 * $count"`: action.Expression("2 * $count"),
	} {
		var a action.Amount
		require.NoError(t, json.Unmarshal([]byte(in), &a), in)
		if _, fixed := want.Fixed(); fixed {
			got, _ := a.Fixed()
			wantV, _ := want.Fixed()
			assert.InDelta(t, wantV, got, 0, in)
			out, err := json.Marshal(a)
			require.NoError(t, err)
			assert.Equal(t, in, string(out), "a number is written as it was read")
		} else {
			assert.Equal(t, want, a, in)
		}
	}
	for _, in := range []string{`""`, `true`, `null`, `[1]`, `{}`} {
		var a action.Amount
		assert.Error(t, json.Unmarshal([]byte(in), &a), in)
	}
	_, err := json.Marshal(action.Amount{})
	require.ErrorIs(t, err, action.ErrInvalid, "no amount is not written")
	assert.True(t, action.Amount{}.IsZero())
}

// TestAmount covers actions.md B4: ranges, whole numbers and expressions.
func TestAmount(t *testing.T) {
	t.Parallel()
	whole := action.Range{Min: 0, Max: 1000, Integer: true}
	require.NoError(t, action.Fixed(1000).Validate(whole))
	require.ErrorIs(t, action.Fixed(1001).Validate(whole), action.ErrInvalid)
	require.ErrorIs(t, action.Fixed(1.5).Validate(whole), action.ErrInvalid, "fractions are not rounded")
	require.NoError(t, action.Expression("$n * 2").Validate(whole))
	require.ErrorIs(t, action.Expression("2 *").Validate(whole), action.ErrInvalid, "the expression must compile")
	require.ErrorIs(t, action.Amount{}.Validate(whole), action.ErrInvalid)

	engine := template.New(nil)
	scope := &template.Scope{ArgDelimiter: "|", Location: time.UTC}
	scope.SetValue("n", template.IntValue(21))
	scope.SetValue("word", template.TextValue("abc"))
	ctx := t.Context()

	v, err := action.Expression("$n * 2").Eval(ctx, engine, scope, whole)
	require.NoError(t, err)
	assert.InDelta(t, 42.0, v, 0)
	_, err = action.Expression("$n * 100").Eval(ctx, engine, scope, whole)
	require.ErrorIs(t, err, action.ErrInvalid, "2100 is out of range")
	_, err = action.Expression("$n / 2").Eval(ctx, engine, scope, whole)
	require.ErrorIs(t, err, action.ErrInvalid, "10.5 is not whole")
	_, err = action.Expression(`"$word"`).Eval(ctx, engine, scope, whole)
	require.ErrorIs(t, err, action.ErrInvalid, "text is not a number")
	v, err = action.Fixed(7).Eval(ctx, engine, scope, whole)
	require.NoError(t, err)
	assert.InDelta(t, 7.0, v, 0)
}

// TestAmountWithTexts covers actions.md B3: an action renders the
// identifiers of an amount together with its other templates and gets the
// same value as Eval.
func TestAmountWithTexts(t *testing.T) {
	t.Parallel()
	whole := action.Range{Min: 0, Max: 1000, Integer: true}
	engine := template.New(nil)
	scope := &template.Scope{ArgDelimiter: "|", Location: time.UTC}
	scope.SetValue("n", template.IntValue(21))
	scope.SetValue("m", template.IntValue(3))
	scope.SetValue("word", template.TextValue("abc"))

	a := action.Expression("$n * 2 + $m")
	ts, err := a.Templates()
	require.NoError(t, err)
	rendered, err := engine.RenderEach(t.Context(), append([]template.Template{template.Parse("other $word")}, ts...), scope)
	require.NoError(t, err)
	texts := make([]string, len(ts))
	for i, r := range rendered[1:] {
		texts[i] = r.Text
	}
	v, err := a.EvalWithTexts(texts, whole)
	require.NoError(t, err)
	assert.InDelta(t, 45.0, v, 0)
	_, err = a.EvalWithTexts(texts, action.Range{Min: 0, Max: 10, Integer: true})
	require.ErrorIs(t, err, action.ErrInvalid, "45 is out of range")
	_, err = a.EvalWithTexts(texts[:1], whole)
	require.Error(t, err, "a text is missing")

	ts, err = action.Fixed(7).Templates()
	require.NoError(t, err)
	assert.Empty(t, ts, "a fixed number has no templates")
	v, err = action.Fixed(7).EvalWithTexts(nil, whole)
	require.NoError(t, err)
	assert.InDelta(t, 7.0, v, 0)
	_, err = action.Fixed(1.5).EvalWithTexts(nil, whole)
	require.ErrorIs(t, err, action.ErrInvalid)

	_, err = action.Expression(`"$word"`).EvalWithTexts([]string{"abc"}, whole)
	require.ErrorIs(t, err, action.ErrInvalid, "text is not a number")
	_, err = action.Expression("2 *").Templates()
	require.ErrorIs(t, err, action.ErrInvalid)
	_, err = action.Amount{}.Templates()
	require.ErrorIs(t, err, action.ErrInvalid)
	_, err = action.Amount{}.EvalWithTexts(nil, whole)
	require.ErrorIs(t, err, action.ErrInvalid)
}

// TestCompileRejects: the conformance test catches schemas that the
// building blocks cannot rule out.
func TestCompileRejects(t *testing.T) {
	t.Parallel()
	good := schema.Document(schema.Property{Name: "message", Schema: schema.Template()})
	_, err := actiontest.Compile("good", good)
	require.NoError(t, err)

	for name, bad := range map[string]*schema.Schema{
		"unknown UI hint, deep inside": schema.Document(schema.Property{Name: "x", Schema: &schema.Schema{
			OneOf: []*schema.Schema{{Type: "string", UI: "slider"}},
		}}),
		"unknown JSON type": schema.Document(schema.Property{Name: "x", Schema: &schema.Schema{Type: "text"}}),
		"negative length":   schema.Document(schema.Property{Name: "x", Schema: &schema.Schema{Type: "string", MinLength: new(-1)}}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := actiontest.Compile("bad", bad)
			assert.Error(t, err)
		})
	}
}

func TestResultName(t *testing.T) {
	t.Parallel()
	for _, n := range []action.ResultName{"result", "line2", "0"} {
		require.NoError(t, n.Validate(), n)
	}
	for _, n := range []action.ResultName{"", "Result", "my_result", "$result", "with space"} {
		require.ErrorIs(t, n.Validate(), action.ErrInvalid, n)
	}
}

func TestCategories(t *testing.T) {
	t.Parallel()
	for _, c := range action.Categories() {
		assert.True(t, c.Valid(), c)
	}
	assert.False(t, action.Category("misc").Valid())
}
