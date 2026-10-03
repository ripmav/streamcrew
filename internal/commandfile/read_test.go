// SPDX-License-Identifier: Apache-2.0

package commandfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/commandfile"
)

// readOne reads a file with one document and returns it as JSON.
func readOne(t *testing.T, name, content string) string {
	t.Helper()
	docs, problems := commandfile.Read(name, []byte(content))
	require.Empty(t, problems)
	require.Len(t, docs, 1)
	out, err := docs[0].JSON()
	require.NoError(t, err)
	return string(out)
}

// readProblems reads a file and returns its problems as text.
func readProblems(t *testing.T, name, content string) []string {
	t.Helper()
	docs, problems := commandfile.Read(name, []byte(content))
	if len(problems) > 0 {
		assert.Empty(t, docs, "a file with a problem gives no documents")
	}
	out := make([]string, len(problems))
	for i, p := range problems {
		out[i] = p.String()
	}
	return out
}

// TestReadValues covers B5 and B67: YAML by version 1.2, numbers exact
// from their text, keys and values in their order.
func TestReadValues(t *testing.T) {
	t.Parallel()
	assert.JSONEq(t, `{"triggers":["no","on","yes","off","Hug"],"enabled":true,"unlocked":false,"group":null}`,
		readOne(t, "a.yaml", "triggers: [no, on, yes, off, Hug]\nenabled: true\nunlocked: false\ngroup: ~\n"))
	assert.Equal(t, `{"a":16,"b":1000,"c":1.5,"d":-0.25,"e":100000,"f":"2026-10-03","g":"5","h":0.1}`,
		readOne(t, "a.yaml", "a: 0x10\nb: 1_000\nc: 1.50\nd: -.25\ne: 1e5\nf: 2026-10-03\ng: '5'\nh: 0.1\n"), "canonical decimals")
	assert.Equal(t, `{"x":1,"list":[{"y":2},{"y":2}]}`,
		readOne(t, "a.yaml", "x: 1\nlist:\n  - &item {y: 2}\n  - *item\n"), "aliases are expanded")
	assert.Equal(t, `{"1":"a","true":"b"}`, readOne(t, "a.yaml", "1: a\ntrue: b\n"), "keys are their text")
	assert.Equal(t, `{"a":"x/y","b":[true,null],"c":"é"}`, readOne(t, "a.json", `{"a": "x\/y", "b": [true, null], "c": "\u00e9"}`))
	assert.Equal(t, `{"a":"x/y"}`, readOne(t, "A.JSON", `{"a": "x\/y"}`), "the extension regardless of case")
}

// TestReadDocuments covers B37: several YAML documents, a list of
// documents, and empty documents that do not count.
func TestReadDocuments(t *testing.T) {
	t.Parallel()
	docs, problems := commandfile.Read("a.yaml", []byte("---\nkind: ChatCommand\nmetadata: {name: Hug}\n---\n---\n- kind: CooldownGroup\n- kind: CommandGroup\n  metadata: {name: 7}\n"))
	require.Empty(t, problems)
	require.Len(t, docs, 3)
	k, ok := docs[0].Kind()
	assert.True(t, ok)
	assert.Equal(t, commandfile.KindChatCommand, k)
	name, ok := docs[0].Name()
	assert.True(t, ok)
	assert.Equal(t, "Hug", name)
	_, ok = docs[1].Name()
	assert.False(t, ok, "no metadata")
	_, ok = docs[2].Name()
	assert.False(t, ok, "a name that is not text")
	assert.Equal(t, "a.yaml", docs[2].File)

	docs, problems = commandfile.Read("a.json", []byte(`[{"kind":"ChatCommand"},{"kind":7}]`))
	require.Empty(t, problems)
	require.Len(t, docs, 2)
	_, ok = docs[1].Kind()
	assert.False(t, ok, "a kind that is not text")

	half := `{"a":[` + strings.Repeat("0,", 1<<19) + `0]}`
	docs, problems = commandfile.Read("big.json", []byte("["+half+","+half+"]"))
	assert.Empty(t, problems, "the limit holds for each document")
	assert.Len(t, docs, 2)

	docs, problems = commandfile.Read("empty.yaml", []byte("# nothing\n"))
	assert.Empty(t, docs)
	assert.Empty(t, problems)
}

// TestReadProblems covers B32, B66 and the limits: syntax errors of YAML
// with the line only, of JSON with line and column, duplicate keys at the
// second one, and values JSON cannot hold.
func TestReadProblems(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file, content, want string
	}{
		{"yaml syntax", "a.yaml", "a: [1,\nb: 2\n", "a.yaml:2: YAML: did not find expected ',' or ']'"},
		{"json syntax", "a.json", "{\n  \"a\": }\n", "a.json:2:8: JSON: missing value after object name"},
		{"json comment", "a.json", "{\"a\": 1} // no\n", "a.json:1:10: JSON: invalid character '/' at start of value"},
		{"two json values", "a.json", "{} {}", "a.json:1:4: more than one JSON value"},
		{"json end", "a.json", "{\"a\": [1,", "a.json:1:10: JSON: unexpected EOF"},
		{"empty json", "a.json", "", "a.json:1:1: JSON: unexpected end of file"},
		{"json large number", "a.json", "{\"a\": 1e40}", `a.json:1:7: a: number 1e40:`},
		{"duplicate key", "a.yaml", "spec:\n  enabled: true\n  enabled: false\n", "a.yaml:3:3: spec.enabled: duplicate key"},
		{"duplicate json name", "a.json", "{\"kind\": \"A\",\n \"kind\": \"B\"}", "a.json:2:2: kind: duplicate key"},
		{"merge key", "a.yaml", "base: &b {x: 1}\nspec:\n  <<: *b\n", "a.yaml:3:3: spec: merge keys (<<) are not supported"},
		{"key not text", "a.yaml", "? [a]\n: 1\n", "a.yaml:1:3: a key must be text"},
		{"octal", "a.yaml", "spec:\n  seconds: 0o17\n", `a.yaml:2:12: spec.seconds: number "0o17": not a decimal number`},
		{"infinity", "a.yaml", "x: .inf\n", `a.yaml:1:4: x: number ".inf": not a decimal number`},
		{"too large", "a.yaml", "x: 1e40\n", `a.yaml:1:4: x: number "1e40":`},
		{"yaml truth value", "a.yaml", "x: True\n", `a.yaml:1:4: x: truth value "True": only true and false`},
		{"binary", "a.yaml", "x: !!binary aGk=\n", "a.yaml:1:4: x: unsupported YAML tag !!binary"},
		{"not an object", "a.yaml", "hug\n", "a.yaml:1:1: a document must be an object, not text"},
		{"number document", "a.json", "[{}, 7]", "a.json:1:6: a document must be an object, not a number"},
		{"list of lists", "a.json", "[[]]", "a.json:1:2: a document must be an object, not a list"},
		{"json text", "a.json", "\"hug\"", "a.json:1:1: a document must be an object, not text"},
		{"json too deep", "a.json", "{\"a\":" + strings.Repeat("[", 300) + strings.Repeat("]", 300) + "}", "nested more than 256 levels deep"},
		{"not utf-8", "a.yaml", "a: 1\nb: \xff\n", "a.yaml:2:4: not UTF-8"},
		{"too deep", "a.yaml", strings.Repeat("[", 300) + strings.Repeat("]", 300), "nested more than 256 levels deep"},
		{"too many values", "a.yaml", "a: &a [1, 1, 1, 1, 1, 1, 1, 1, 1, 1]\nb: &b [*a, *a, *a, *a, *a, *a, *a, *a, *a, *a]\n" +
			"c: &c [*b, *b, *b, *b, *b, *b, *b, *b, *b, *b]\nd: &d [*c, *c, *c, *c, *c, *c, *c, *c, *c, *c]\n" +
			"e: &e [*d, *d, *d, *d, *d, *d, *d, *d, *d, *d]\nf: &f [*e, *e, *e, *e, *e, *e, *e, *e, *e, *e]\n", "more than 1048576 values"},
	}
	tests = append(tests, struct{ name, file, content, want string }{
		"too many json values", "a.json", `{"a":[` + strings.Repeat("0,", 1<<20) + `0]}`, "more than 1048576 values",
	})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := readProblems(t, tc.file, tc.content)
			require.Len(t, got, 1, "%v", got)
			assert.Contains(t, got[0], tc.want)
		})
	}
}

// TestFiles covers B37: directories with all files of the extensions,
// also in subdirectories, in the order of their paths.
func TestFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"b.yaml", "a.json", filepath.Join("sub", "c.yml"), "sub.yaml", "D.YAML", "notes.txt", filepath.Join("sub", "x.md")} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}
	single := filepath.Join(dir, "b.yaml")
	files, err := commandfile.Files([]string{single, dir})
	require.NoError(t, err)
	assert.Equal(t, []string{
		single,
		filepath.Join(dir, "D.YAML"),
		filepath.Join(dir, "a.json"),
		filepath.Join(dir, "sub.yaml"),
		filepath.Join(dir, "sub", "c.yml"),
	}, files, "the named file first, then the directory in path order, each file once")

	_, err = commandfile.Files([]string{filepath.Join(dir, "notes.txt")})
	require.ErrorContains(t, err, "extension")
	_, err = commandfile.Files([]string{filepath.Join(dir, "missing.yaml")})
	require.Error(t, err)
}

// TestProblemString covers B32: the place as far as it is known, and
// warnings marked.
func TestProblemString(t *testing.T) {
	t.Parallel()
	p := commandfile.Problem{Severity: commandfile.SeverityError, File: "hug.yaml", Line: 12, Column: 9, Path: "spec.actions[1].message", Message: "must not be empty"}
	assert.Equal(t, "hug.yaml:12:9: spec.actions[1].message: must not be empty", p.String())
	p.Column, p.Path = 0, ""
	assert.Equal(t, "hug.yaml:12: must not be empty", p.String())
	p.Line, p.Severity = 0, commandfile.SeverityWarning
	assert.Equal(t, "hug.yaml: warning: must not be empty", p.String())
	assert.False(t, commandfile.HasErrors([]commandfile.Problem{p}))
	p.Severity = commandfile.SeverityError
	assert.True(t, commandfile.HasErrors([]commandfile.Problem{p}))
	assert.False(t, commandfile.HasErrors(nil))
}

// FuzzRead checks that any file reads without a panic and that the
// documents of a file without problems are valid JSON that reads back the
// same.
func FuzzRead(f *testing.F) {
	f.Add("a.yaml", "apiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: hug}\nspec: {triggers: [no], enabled: true}\n")
	f.Add("a.yaml", "a: &a [1, *a]\n---\n- {x: 0x1F}\n")
	f.Add("a.json", `[{"kind":"CooldownGroup","spec":{"duration":"30s"}},{"a":"\u00e9\/","b":[1.5e3,null,true]}]`)
	f.Add("a.json", `{"a":1,"a":2}`)
	f.Fuzz(func(t *testing.T, name, content string) {
		if !strings.HasSuffix(name, ".json") {
			name = "f.yaml"
		}
		docs, problems := commandfile.Read(name, []byte(content))
		if len(problems) > 0 {
			return
		}
		for _, d := range docs {
			out, err := d.JSON()
			require.NoError(t, err)
			require.True(t, out.IsValid(), "%s", out)
			again, problems := commandfile.Read("again.json", out)
			require.Empty(t, problems)
			require.Len(t, again, 1)
			out2, err := again[0].JSON()
			require.NoError(t, err)
			require.Equal(t, string(out), string(out2))
			d.Kind()
			d.Name()
		}
		commandfile.Duplicates(docs)
	})
}
