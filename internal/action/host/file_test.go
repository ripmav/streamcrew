// SPDX-License-Identifier: Apache-2.0

package host_test

import (
	"context"
	json "encoding/json/v2"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/host"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// files is a running engine with the host types and the released root
// "data" in a temporary directory.
type files struct {
	*fixture
	dir string
}

// newFiles returns a fixture for the file action whose core has host:fs.
// Create it inside synctest.Test.
func newFiles(t *testing.T, dir string, granted ...capability.Capability) *files {
	t.Helper()
	if granted == nil {
		granted = []capability.Capability{capability.HostFS}
	}
	f := &fixture{t: t, opener: &opener{}, logs: &logs{}, lines: &lines{}}
	f.reg, f.templates = registryWith(t, host.Ports{
		Opener: f.opener, Roots: roots{"data": dir, "gone": filepath.Join(dir, "does-not-exist")},
		Logger: slog.New(slog.NewTextHandler(f.logs, nil)),
	}, granted...)
	f.harness = actiontest.NewHarness(t, f.reg)
	return &files{fixture: f, dir: dir}
}

// file returns a file action of the document doc, without "type".
func (f *files) file(doc string) host.File {
	f.t.Helper()
	d, ok := f.reg.Descriptor(host.TypeFile)
	require.True(f.t, ok)
	a, err := d.Decode([]byte(doc), json.DefaultOptionsV2())
	require.NoError(f.t, err, doc)
	require.NoError(f.t, a.Validate(), doc)
	fa, ok := a.(host.File)
	require.True(f.t, ok)
	return fa
}

// op returns a file action of kind k on path in the root data, with the
// members of extra, e.g. `"text":"x"`.
func (f *files) op(k host.FileKind, path, extra string) host.File {
	f.t.Helper()
	doc := `{"kind":"` + string(k) + `","root":"data","path":` + quote(path)
	if extra != "" {
		doc += "," + extra
	}
	return f.file(doc + "}")
}

// show returns an action that renders text and records it.
func (f *files) show(text string) command.Action {
	return probe{fn: func(ctx context.Context, run *engine.Run) error {
		out, err := f.templates.Render(ctx, template.Parse(text), run.Scope(), template.Text)
		f.lines.add(out)
		return err
	}}
}

// put writes a file in the root.
func (f *files) put(path, content string) {
	f.t.Helper()
	full := filepath.Join(f.dir, filepath.FromSlash(path))
	require.NoError(f.t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(f.t, os.WriteFile(full, []byte(content), 0o600))
}

// content returns the content of a file in the root.
func (f *files) content(path string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(path)))
	require.NoError(f.t, err)
	return string(data)
}

func TestFileConformance(t *testing.T) {
	t.Parallel()
	reg, _ := registry(t, &opener{}, &logs{})
	d, ok := reg.Descriptor(host.TypeFile)
	require.True(t, ok)
	assert.Equal(t, []capability.Capability{capability.HostFS}, d.Capabilities)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "write", Doc: `{"type":"file","kind":"write","root":"obs","path":"now playing/$arg1text.txt","text":"Now: $arg2text"}`, Valid: true},
		{Name: "write with the default text", Doc: `{"type":"file","kind":"write","root":"obs","path":"clear.txt"}`, Valid: true},
		{Name: "append", Doc: `{"type":"file","kind":"append","root":"logs","path":"chat.log","text":"$username"}`, Valid: true},
		{Name: "count lines", Doc: `{"type":"file","kind":"count_lines","root":"obs","path":"quotes.txt","result":"count"}`, Valid: true},
		{Name: "read line", Doc: `{"type":"file","kind":"read_line","root":"obs","path":"quotes.txt","line":"$arg1text","result":"quote"}`, Valid: true},
		{Name: "each line", Doc: `{"type":"file","kind":"each_line","root":"obs","path":"names.txt","result":"name","actions":[]}`, Valid: true},
		{Name: "insert line", Doc: `{"type":"file","enabled":false,"kind":"insert_line","root":"obs","path":"q.txt","line":1,"text":"first"}`, Valid: true},
		{Name: "remove matching line", Doc: `{"type":"file","kind":"remove_matching_line","root":"obs","path":"q.txt","text":"$arg1text","result":"removed"}`, Valid: true},
		{Name: "remove random line", Doc: `{"type":"file","kind":"remove_random_line","root":"obs","path":"q.txt","result":"pick"}`, Valid: true},
		{Name: "kind missing", Doc: `{"type":"file","root":"obs","path":"x"}`},
		{Name: "root missing", Doc: `{"type":"file","kind":"read","path":"x","result":"r"}`},
		{Name: "root in uppercase", Doc: `{"type":"file","kind":"write","root":"OBS","path":"x"}`},
		{Name: "path missing", Doc: `{"type":"file","kind":"write","root":"obs"}`},
		{Name: "empty path", Doc: `{"type":"file","kind":"write","root":"obs","path":""}`},
		{Name: "line missing", Doc: `{"type":"file","kind":"read_line","root":"obs","path":"x","result":"r"}`},
		{Name: "line 0", Doc: `{"type":"file","kind":"read_line","root":"obs","path":"x","line":0,"result":"r"}`},
		{Name: "result missing", Doc: `{"type":"file","kind":"read","root":"obs","path":"x"}`},
		{Name: "invalid result name", Doc: `{"type":"file","kind":"read","root":"obs","path":"x","result":"My Result"}`},
		{Name: "read with a text", Doc: `{"type":"file","kind":"read","root":"obs","path":"x","result":"r","text":"y"}`},
		{Name: "write with a result", Doc: `{"type":"file","kind":"write","root":"obs","path":"x","result":"r"}`},
		{Name: "write with a line", Doc: `{"type":"file","kind":"write","root":"obs","path":"x","line":1}`},
		{Name: "read with child actions", Doc: `{"type":"file","kind":"read","root":"obs","path":"x","result":"r","actions":[]}`},
	}}.Run(t)

	// Paths without identifiers that leave the root are rejected when saving
	// (B102, B217); the schema cannot see that.
	for _, path := range []string{"../geheim.txt", "a/../../b", "/etc/passwd"} {
		a, err := d.Decode([]byte(`{"kind":"write","root":"obs","path":`+quote(path)+`}`), json.DefaultOptionsV2())
		require.NoError(t, err)
		require.ErrorContains(t, a.Validate(), "leaves the root", path)
	}
}

// TestFileWriteAndRead covers actions.md B104 and B105.
func TestFileWriteAndRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		f := newFiles(t, dir)
		in := f.start([]string{"song"},
			f.op(host.FileWrite, "sub/dir/$arg1text.txt", `"text":"Now: $arg1text"`),
			f.op(host.FileAppend, "sub/dir/song.txt", `"text":"line 2"`),
			f.op(host.FileAppend, "log.txt", `"text":"first"`),
			f.op(host.FileCountLines, "sub/dir/song.txt", `"result":"count"`),
			f.op(host.FileRead, "sub/dir/song.txt", `"result":"all"`),
			f.show("$count|$all"),
			f.op(host.FileWrite, "empty.txt", ""),
		)
		assert.Empty(t, in.Errors)
		assert.Equal(t, "Now: song\nline 2\n", f.content("sub/dir/song.txt"), "append starts a new line")
		assert.Equal(t, "first\n", f.content("log.txt"), "append creates the file")
		assert.Equal(t, []string{"2|Now: song\nline 2\n"}, f.lines.get())
		assert.Empty(t, f.content("empty.txt"))
	})
}

// TestFileLines covers actions.md B103 to B106: line numbers from 1, lines
// end with "\n" or "\r\n", an empty line at the end is no line, changes
// write "\n"; invalid UTF-8 reads as U+FFFD.
func TestFileLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		f := newFiles(t, dir)
		f.put("q.txt", "one\r\ntwo\r\nthree\r\n")
		f.put("bad.txt", "a\xffb")
		in := f.start(nil,
			f.op(host.FileCountLines, "q.txt", `"result":"total"`),
			f.op(host.FileReadLine, "q.txt", `"line":2,"result":"second"`),
			f.op(host.FileReadRandomLine, "q.txt", `"result":"pick"`),
			f.op(host.FileRead, "bad.txt", `"result":"broken"`),
			f.show("$total|$second|$pick|$broken"),
			f.op(host.FileInsertLine, "q.txt", `"line":1,"text":"zero"`),
			f.op(host.FileInsertLine, "q.txt", `"line":5,"text":"four"`),
			f.op(host.FileRemoveLine, "q.txt", `"line":2,"result":"removed"`),
			f.op(host.FileRemoveMatchingLine, "q.txt", `"text":"three","result":"matched"`),
			f.op(host.FileRemoveMatchingLine, "q.txt", `"text":"nothing","result":"unmatched"`),
			f.op(host.FileInsertRandomLine, "q.txt", `"text":"last"`),
			f.op(host.FileRemoveRandomLine, "q.txt", `"result":"dropped"`),
			f.show("$removed|$matched|$unmatched|$dropped"),
		)
		assert.Empty(t, in.Errors)
		got := f.lines.get()
		assert.Equal(t, "3|two|three|a�b", got[0], "the random line is the last one in the tests")
		assert.Equal(t, "one|three|$unmatched|last", got[1], "no match changes nothing and sets nothing")
		assert.Equal(t, "zero\ntwo\nfour\n", f.content("q.txt"))
	})
}

// TestFileFails covers actions.md B102, B104, B108, B217: paths outside the
// root, unknown and missing roots, missing files, lines outside the file,
// random lines of an empty file and files over 1 MiB let the action fail.
func TestFileFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o600))
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link")))
	}
	synctest.Test(t, func(t *testing.T) {
		f := newFiles(t, dir)
		f.put("two.txt", "a\nb\n")
		f.put("empty.txt", "")
		f.put("big.txt", strings.Repeat("x", host.MaxFileSize+1))
		params := engine.Params{Values: map[string]template.Value{
			"up": template.TextValue("../secret.txt"), "abs": template.TextValue(filepath.Join(outside, "secret.txt")),
		}}
		cases := []struct {
			action command.Action
			want   string
		}{
			{f.op(host.FileRead, "$up", `"result":"r"`), `path: invalid action: "../secret.txt" leaves the root`},
			{f.op(host.FileRead, "$abs", `"result":"r"`), "leaves the root"},
			{f.file(`{"kind":"read","root":"other","path":"x","result":"r"}`), `root: invalid action: no root "other" is released`},
			{f.file(`{"kind":"read","root":"gone","path":"x","result":"r"}`), "root: "},
			{f.op(host.FileRead, "missing.txt", `"result":"r"`), "path: the file does not exist: missing.txt"},
			{f.op(host.FileInsertLine, "missing.txt", `"line":1,"text":"x"`), "the file does not exist"},
			{f.op(host.FileReadLine, "two.txt", `"line":3,"result":"r"`), "line: invalid action: line 3 of a file with 2 lines"},
			{f.op(host.FileInsertLine, "two.txt", `"line":4,"text":"x"`), "line: invalid action: line 4 of a file with 2 lines"},
			{f.op(host.FileReadRandomLine, "empty.txt", `"result":"r"`), "path: the file has no lines"},
			{f.op(host.FileRead, "big.txt", `"result":"r"`), "more than 1048576"},
			{f.op(host.FileAppend, "big.txt", `"text":"x"`), "more than 1048576"},
		}
		if runtime.GOOS != "windows" {
			cases = append(cases, struct {
				action command.Action
				want   string
			}{f.op(host.FileRead, "link/secret.txt", `"result":"r"`), "path: "})
		}
		actions := make([]command.Action, len(cases))
		for i, c := range cases {
			actions[i] = c.action
		}
		in := f.harness.Start(f.harness.Command("x", actions...), params)
		require.Len(t, in.Errors, len(cases))
		for i, c := range cases {
			assert.Contains(t, in.Errors[i].Message, c.want, i)
		}
		assert.Equal(t, "a\nb\n", f.content("two.txt"), "failed changes change nothing")
	})
	assert.NoFileExists(t, filepath.Join(dir, "secret.txt"))
}

// TestFileTooLarge covers actions.md B108: a change that would make a file
// larger than 1 MiB is not written.
func TestFileTooLarge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		f := newFiles(t, dir)
		f.put("q.txt", "keep\n")
		big := template.TextValue(strings.Repeat("y", host.MaxFileSize))
		in := f.harness.Start(f.harness.Command("x",
			f.op(host.FileAppend, "q.txt", `"text":"$big"`),
			f.op(host.FileWrite, "new.txt", `"text":"$big$big"`),
		), engine.Params{Values: map[string]template.Value{"big": big}})
		require.Len(t, in.Errors, 2)
		assert.Equal(t, "keep\n", f.content("q.txt"))
		assert.NoFileExists(t, filepath.Join(dir, "new.txt"))
	})
}

// TestFileEachLine covers actions.md B107: the child actions run for each
// line, with the line as result value; more than 1 000 lines fail before
// the first pass.
func TestFileEachLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		f := newFiles(t, dir)
		f.put("names.txt", "alice\nbob\n\ncarol\n")
		f.put("many.txt", strings.Repeat("x\n", host.MaxEachLines+1))
		each := f.op(host.FileEachLine, "names.txt", `"result":"name"`)
		each.Actions = []command.Action{f.show("hi $name")}
		many := f.op(host.FileEachLine, "many.txt", `"result":"name"`)
		many.Actions = []command.Action{f.show("never")}
		in := f.start(nil, each, many)
		require.Len(t, in.Errors, 1)
		assert.Equal(t, "path: invalid action: 1001 lines, at most 1000", in.Errors[0].Message)
		assert.Equal(t, []string{"hi alice", "hi bob", "hi ", "hi carol"}, f.lines.get())
	})
}

// TestFileAtomic covers actions.md B109: a change leaves no temporary file
// and keeps the permissions of the file.
func TestFileAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		f := newFiles(t, dir)
		f.put("q.txt", "a\n") // 0o600; a new file would get 0o644
		in := f.start(nil, f.op(host.FileAppend, "q.txt", `"text":"b"`), f.op(host.FileWrite, "q.txt", `"text":"c"`))
		assert.Empty(t, in.Errors)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1, "no temporary file is left")
		assert.Equal(t, "c", f.content("q.txt"))
		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(dir, "q.txt"))
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the permissions stay")
		}
	})
}

// TestFileCapability covers actions.md B7: without host:fs the action does
// not run.
func TestFileCapability(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		f := newFiles(t, dir, capability.HostProcess)
		in := f.start(nil, f.op(host.FileWrite, "x.txt", `"text":"x"`))
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "host:fs")
	})
	assert.NoFileExists(t, filepath.Join(dir, "x.txt"))
}

// TestFileSaving covers actions.md B5 and Code-ADR-0013, point 7: saving
// sees the root and the name of the result value.
func TestFileSaving(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		f := newFiles(t, dir)
		read := f.op(host.FileRead, "x", `"result":"content"`)
		assert.Equal(t, []command.Reference{{Kind: command.RefFileRoot, Name: "data"}}, read.References())
		assert.Equal(t, []string{"content"}, read.ResultNames())
		assert.Empty(t, f.op(host.FileWrite, "x", "").ResultNames())
		var _ command.ResultSetter = read
		var _ command.Referrer = read
	})
}
