// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeFile is the type ID of the file action (Code-ADR-0013, point 1).
const TypeFile = "file"

// MaxFileSize is the largest file the action reads or changes, 1 MiB
// (actions.md B108); a change that would make a file larger fails too.
const MaxFileSize = 1 << 20

// MaxEachLines is the most lines each_line runs its child actions for
// (B107; command-engine.md B74).
const MaxEachLines = 1000

// lineRange is the range of a line number (B100): from 1, at most as many
// as a file of MaxFileSize can have lines.
func lineRange() action.Range {
	return action.Range{Min: 1, Max: MaxFileSize + 1, Integer: true}
}

// rootPattern is the form of the names of released roots.
var rootPattern = regexp.MustCompile(schema.PatternFileRoot)

// ErrNoFile is the error of a kind that needs a file that does not exist
// (B108).
var ErrNoFile = errors.New("the file does not exist")

// FileKind is what a file action does (actions.md B101).
type FileKind string

// The kinds of the file action.
const (
	// FileWrite creates the file or replaces its content (B104). New file
	// actions do this.
	FileWrite FileKind = "write"
	// FileAppend adds the text as a new line, creating the file if needed.
	FileAppend FileKind = "append"
	// FileCountLines sets the number of lines (B105).
	FileCountLines FileKind = "count_lines"
	// FileRead sets the whole content.
	FileRead FileKind = "read"
	// FileReadLine sets line N.
	FileReadLine FileKind = "read_line"
	// FileReadRandomLine sets a random line.
	FileReadRandomLine FileKind = "read_random_line"
	// FileEachLine runs the child actions for each line (B107).
	FileEachLine FileKind = "each_line"
	// FileRemoveLine removes line N and sets it (B106).
	FileRemoveLine FileKind = "remove_line"
	// FileRemoveRandomLine removes a random line and sets it.
	FileRemoveRandomLine FileKind = "remove_random_line"
	// FileRemoveMatchingLine removes the first line equal to the text and
	// sets it.
	FileRemoveMatchingLine FileKind = "remove_matching_line"
	// FileInsertLine inserts the text before line N (B104).
	FileInsertLine FileKind = "insert_line"
	// FileInsertRandomLine inserts the text at a random place.
	FileInsertRandomLine FileKind = "insert_random_line"
)

// FileKinds returns the kinds, in the order editors show them.
func FileKinds() []FileKind {
	return []FileKind{
		FileWrite, FileAppend, FileCountLines, FileRead, FileReadLine, FileReadRandomLine, FileEachLine,
		FileRemoveLine, FileRemoveRandomLine, FileRemoveMatchingLine, FileInsertLine, FileInsertRandomLine,
	}
}

// Valid reports whether k is a known kind.
func (k FileKind) Valid() bool {
	return slices.Contains(FileKinds(), k)
}

// hasText reports whether actions of kind k have a text.
func (k FileKind) hasText() bool {
	return slices.Contains([]FileKind{
		FileWrite, FileAppend, FileRemoveMatchingLine, FileInsertLine, FileInsertRandomLine,
	}, k)
}

// hasLine reports whether actions of kind k have a line number.
func (k FileKind) hasLine() bool {
	return k == FileReadLine || k == FileRemoveLine || k == FileInsertLine
}

// hasResult reports whether actions of kind k set a result value.
func (k FileKind) hasResult() bool {
	return slices.Contains([]FileKind{
		FileCountLines, FileRead, FileReadLine, FileReadRandomLine, FileEachLine,
		FileRemoveLine, FileRemoveRandomLine, FileRemoveMatchingLine,
	}, k)
}

// fileSchema returns the schema of the file action: the kind decides which
// of text, line, result and child actions it has.
func fileSchema() *schema.Schema {
	text := schema.Property{Name: "text", Schema: schema.Template()}
	line := schema.Property{Name: "line", Schema: lineRange().Schema(), Required: true}
	result := schema.Property{Name: "result", Schema: schema.ResultName(), Required: true}
	actions := schema.Property{Name: "actions", Schema: schema.Actions()}
	variants := make([]schema.Variant, 0, len(FileKinds()))
	for _, k := range FileKinds() {
		v := schema.Variant{Kind: string(k)}
		if k.hasText() {
			v.Props = append(v.Props, text)
		}
		if k.hasLine() {
			v.Props = append(v.Props, line)
		}
		if k.hasResult() {
			v.Props = append(v.Props, result)
		}
		if k == FileEachLine {
			v.Props = append(v.Props, actions)
		}
		variants = append(variants, v)
	}
	return schema.Kinds([]schema.Property{
		{Name: "root", Schema: schema.FileRoot(), Required: true},
		{Name: "path", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
	}, variants...)
}

// File is the file action (actions.md B100 to B109). Which members it has
// depends on its kind; members of other kinds are nil.
type File struct {
	action.Common `json:",embed"`
	Kind          FileKind `json:"kind"`
	// Root is the name of a root the configuration releases (B100,
	// Code-ADR-0019); a new action has none.
	Root string `json:"root,omitzero"`
	// Path is the path in the root, as a template; "/" separates
	// directories on every system. A new action has none.
	Path action.Template `json:"path,omitzero"`
	// Text is the text to write, append, insert or find, as a template; a
	// new action has an empty text.
	Text *action.Template `json:"text,omitzero"`
	// Line is the line number, from 1 (B100); a new action has none.
	Line *action.Amount `json:"line,omitzero"`
	// Result is the name of the result value (B5, B105 to B107); a new
	// action has none.
	Result *action.ResultName `json:"result,omitzero"`
	// Actions are the child actions of each_line.
	Actions []command.Action `json:"actions,omitzero"`
	ports   *ports
}

// newFile returns a new file action of kind k; ok is false for an unknown
// kind.
func newFile(p *ports, k FileKind) (File, bool) {
	f := File{Common: action.On(), Kind: k, ports: p}
	if k.hasText() {
		f.Text = new(action.Template)
	}
	if k == FileEachLine {
		f.Actions = []command.Action{}
	}
	return f, k.Valid()
}

// DocType implements command.Action.
func (File) DocType() string { return TypeFile }

// Validate implements command.Action: the members fit the kind, the name of
// the root has its form, and a path without identifiers stays in the root
// (B102).
func (f File) Validate() error {
	k := f.Kind
	switch {
	case !k.Valid():
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, k))
	case !rootPattern.MatchString(f.Root):
		return field("root", fmt.Errorf("%w: %q is not 1 to 32 lowercase letters, digits and \"_\"", action.ErrInvalid, f.Root))
	case f.Path == "":
		return field("path", fmt.Errorf("%w: empty path", action.ErrInvalid))
	case !strings.Contains(string(f.Path), "$") && !local(string(f.Path)):
		return field("path", fmt.Errorf("%w: %q leaves the root", action.ErrInvalid, f.Path))
	case k.hasText() != (f.Text != nil):
		return field("text", fmt.Errorf("%w: %s has no text", action.ErrInvalid, k))
	case k.hasLine() != (f.Line != nil):
		return field("line", fmt.Errorf("%w: %s has no line number", action.ErrInvalid, k))
	case k.hasResult() != (f.Result != nil):
		return field("result", fmt.Errorf("%w: %s sets no result value", action.ErrInvalid, k))
	case (k == FileEachLine) != (f.Actions != nil):
		return field("actions", fmt.Errorf("%w: only each_line has child actions", action.ErrInvalid))
	case f.Line != nil:
		if err := f.Line.Validate(lineRange()); err != nil {
			return field("line", err)
		}
	}
	if f.Result != nil {
		return field("result", f.Result.Validate())
	}
	return nil
}

// References implements command.Referrer: saving warns about a root the
// configuration does not release (B7; Code-ADR-0013, point 7).
func (f File) References() []command.Reference {
	return []command.Reference{{Kind: command.RefFileRoot, Name: f.Root}}
}

// ResultNames implements command.ResultSetter (B5).
func (f File) ResultNames() []string {
	if f.Result == nil {
		return []string{}
	}
	return []string{string(*f.Result)}
}

// Children implements engine.Container: the child actions of each_line;
// none for the other kinds.
func (f File) Children() []command.Action { return f.Actions }

// local reports whether path stays in its root: not absolute, no ".."
// beyond it (B102).
func local(path string) bool {
	return filepath.IsLocal(systemPath(path))
}

// systemPath returns path with the separator of the system: "/" and "\"
// both separate directories on every system, so that paths from Windows
// hold on Linux and macOS too (B102).
func systemPath(path string) string {
	return filepath.FromSlash(strings.ReplaceAll(path, `\`, "/"))
}

// Perform implements engine.Performer. Path, text and line number come
// from one render (B3). Accesses to the same file run one after the other
// (B109); each_line runs its child actions after it has read the file.
func (f File) Perform(ctx context.Context, run *engine.Run) error {
	in, err := f.render(ctx, run.Scope())
	if err != nil {
		return err
	}
	dir, ok := f.ports.Roots.Root(f.Root)
	if !ok {
		return field("root", fmt.Errorf("%w: no root %q is released", action.ErrInvalid, f.Root))
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return field("root", err)
	}
	defer root.Close()

	if f.Kind == FileEachLine {
		var lines []string
		err := f.ports.locked(dir, in.path, func() (err error) {
			lines, err = f.readLines(root, in.path)
			return err
		})
		if err != nil {
			return field("path", err)
		}
		// The file is no longer locked: the child actions may use it.
		return f.eachLine(ctx, run, lines)
	}
	return f.ports.locked(dir, in.path, func() error { return f.change(run, root, in) })
}

// locked runs fn while the file at path in the root dir is locked, so that
// accesses to the same file run one after the other (B109).
func (p *ports) locked(dir, path string, fn func() error) error {
	unlock := p.files.lock(filepath.Join(dir, path))
	defer unlock()
	return fn()
}

// inputs are the rendered members of a file action.
type inputs struct {
	path string
	text string
	line int
}

// render renders path, text and line in one render (B3) and checks that
// the path stays in the root (B102).
func (f File) render(ctx context.Context, s *template.Scope) (inputs, error) {
	ts := []template.Template{f.Path.Parse()}
	if f.Text != nil {
		ts = append(ts, f.Text.Parse())
	}
	var line []template.Template
	if f.Line != nil {
		var err error
		if line, err = f.Line.Templates(); err != nil {
			return inputs{}, field("line", err)
		}
	}
	rendered, err := f.ports.Templates.RenderEach(ctx, append(ts, line...), s)
	if err != nil {
		return inputs{}, err
	}
	texts := make([]string, len(rendered))
	for i, r := range rendered {
		texts[i] = r.Text
	}
	in := inputs{path: texts[0]}
	texts = texts[1:]
	if !local(in.path) {
		return inputs{}, field("path", fmt.Errorf("%w: %q leaves the root", action.ErrInvalid, in.path))
	}
	in.path = filepath.Clean(systemPath(in.path))
	if f.Text != nil {
		in.text, texts = texts[0], texts[1:]
	}
	if f.Line != nil {
		n, err := f.Line.EvalWithTexts(texts, lineRange())
		if err != nil {
			return inputs{}, field("line", err)
		}
		in.line = int(n)
	}
	return in, nil
}

// change does what the kinds other than each_line do (B104 to B106).
func (f File) change(run *engine.Run, root *os.Root, in inputs) error {
	k := f.Kind
	switch k {
	case FileWrite:
		return field("path", replace(root, in.path, in.text))
	case FileAppend:
		old, err := read(root, in.path)
		if err != nil && !errors.Is(err, ErrNoFile) {
			return field("path", err)
		}
		if old != "" && !strings.HasSuffix(old, "\n") {
			old += "\n"
		}
		return field("path", replace(root, in.path, old+in.text+"\n"))
	case FileEachLine:
		return field("kind", fmt.Errorf("%w: each_line changes nothing", action.ErrInvalid))
	case FileCountLines, FileRead, FileReadLine, FileReadRandomLine, FileRemoveLine, FileRemoveRandomLine,
		FileRemoveMatchingLine, FileInsertLine, FileInsertRandomLine:
	default:
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, k))
	}

	content, err := read(root, in.path)
	if err != nil {
		return field("path", err)
	}
	lines := splitLines(content)
	set := func(v template.Value) { run.Scope().SetValue(string(*f.Result), v) }
	switch k {
	case FileCountLines:
		set(template.IntValue(int64(len(lines))))
	case FileRead:
		set(template.TextValue(content))
	case FileReadLine:
		i, err := index(in.line, len(lines))
		if err != nil {
			return field("line", err)
		}
		set(template.TextValue(lines[i]))
	case FileReadRandomLine:
		if len(lines) == 0 {
			return field("path", errors.New("the file has no lines"))
		}
		set(template.TextValue(lines[f.ports.IntN(len(lines))]))
	case FileRemoveLine, FileRemoveRandomLine, FileRemoveMatchingLine:
		return f.remove(root, in, lines, set)
	case FileInsertLine, FileInsertRandomLine:
		at := 0
		if k == FileInsertLine {
			if in.line > len(lines)+1 {
				return field("line", fmt.Errorf("%w: line %d of a file with %d lines", action.ErrInvalid, in.line, len(lines)))
			}
			at = in.line - 1
		} else {
			at = f.ports.IntN(len(lines) + 1)
		}
		lines = slices.Insert(lines, at, in.text)
		return field("path", replace(root, in.path, joinLines(lines)))
	case FileWrite, FileAppend, FileEachLine: // handled above
	}
	return nil
}

// remove removes a line and sets it as the result value (B106). A matching
// line that is not there changes nothing and sets nothing.
func (f File) remove(root *os.Root, in inputs, lines []string, set func(template.Value)) error {
	var i int
	switch f.Kind {
	case FileRemoveLine:
		var err error
		if i, err = index(in.line, len(lines)); err != nil {
			return field("line", err)
		}
	case FileRemoveRandomLine:
		if len(lines) == 0 {
			return field("path", errors.New("the file has no lines"))
		}
		i = f.ports.IntN(len(lines))
	default:
		if i = slices.Index(lines, in.text); i < 0 {
			return nil
		}
	}
	removed := lines[i]
	if err := replace(root, in.path, joinLines(slices.Delete(lines, i, i+1))); err != nil {
		return field("path", err)
	}
	set(template.TextValue(removed))
	return nil
}

// readLines returns the lines of the file for each_line, at most
// MaxEachLines (B107).
func (f File) readLines(root *os.Root, path string) ([]string, error) {
	content, err := read(root, path)
	if err != nil {
		return nil, err
	}
	lines := splitLines(content)
	if len(lines) > MaxEachLines {
		return nil, fmt.Errorf("%w: %d lines, at most %d", action.ErrInvalid, len(lines), MaxEachLines)
	}
	return lines, nil
}

// eachLine runs the child actions for each line, with the line as result
// value (B107).
func (f File) eachLine(ctx context.Context, run *engine.Run, lines []string) error {
	for _, line := range lines {
		run.Scope().SetValue(string(*f.Result), template.TextValue(line))
		for i := range f.Actions {
			next, err := run.PerformChild(ctx, i)
			if err != nil || next == engine.ChildEnd {
				return err
			}
		}
	}
	return nil
}

// index returns the index of line number n in a file with count lines.
func index(n, count int) (int, error) {
	if n < 1 || n > count {
		return 0, fmt.Errorf("%w: line %d of a file with %d lines", action.ErrInvalid, n, count)
	}
	return n - 1, nil
}

// read returns the content of the file as UTF-8, invalid bytes as U+FFFD
// (B103). A missing file is ErrNoFile, a file over MaxFileSize an error
// (B108).
func read(root *os.Root, path string) (string, error) {
	info, err := root.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("%w: %s", ErrNoFile, filepath.ToSlash(path))
	case err != nil:
		return "", err
	case !info.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a file", filepath.ToSlash(path))
	case info.Size() > MaxFileSize:
		return "", fmt.Errorf("%s has %d bytes, more than %d", filepath.ToSlash(path), info.Size(), MaxFileSize)
	}
	data, err := root.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > MaxFileSize {
		return "", fmt.Errorf("%s has %d bytes, more than %d", filepath.ToSlash(path), len(data), MaxFileSize)
	}
	return strings.ToValidUTF8(string(data), "�"), nil
}

// splitLines returns the lines of content: they end with "\n" or "\r\n";
// an empty line at the end is not a line (B103).
func splitLines(content string) []string {
	if content == "" {
		return []string{}
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// joinLines returns lines as content, each ending with "\n" (B103).
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// replace writes content to the file atomically: it writes a new file next
// to it and then renames it, so that a reader such as a text source in OBS
// never sees half a file (B109). Missing directories are created (B104).
// Content over MaxFileSize is not written (B108).
func replace(root *os.Root, path, content string) error {
	if len(content) > MaxFileSize {
		return fmt.Errorf("%w: %d bytes, more than %d", action.ErrInvalid, len(content), MaxFileSize)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := root.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	mode := fs.FileMode(0o644)
	if info, err := root.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix) // crypto/rand.Read never fails
	tmp := path + ".streamcrew-" + hex.EncodeToString(suffix)
	file, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, werr := file.WriteString(content)
	serr := file.Sync()
	cerr := file.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, path); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// fileLocks lets accesses to the same file run one after the other (B109).
// It is safe for concurrent use.
type fileLocks struct {
	mu   sync.Mutex
	held map[string]*fileLock
}

// fileLock is the lock of one file and how many accesses wait for it.
type fileLock struct {
	mu   sync.Mutex
	refs int
}

// lock locks the file at path and returns the function that unlocks it.
func (l *fileLocks) lock(path string) func() {
	l.mu.Lock()
	if l.held == nil {
		l.held = make(map[string]*fileLock)
	}
	fl := l.held[path]
	if fl == nil {
		fl = &fileLock{}
		l.held[path] = fl
	}
	fl.refs++
	l.mu.Unlock()
	fl.mu.Lock()
	return func() {
		fl.mu.Unlock()
		l.mu.Lock()
		defer l.mu.Unlock()
		if fl.refs--; fl.refs == 0 {
			delete(l.held, path)
		}
	}
}
