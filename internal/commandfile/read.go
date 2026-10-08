// SPDX-License-Identifier: MIT

package commandfile

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Document is a document of a file as read, before it is checked.
type Document struct {
	// File is the file it comes from, as given.
	File string
	root *value
}

// Kind returns the kind the document names; ok is false if its member
// "kind" is missing or not text.
func (d Document) Kind() (k Kind, ok bool) {
	m, ok := d.root.lookup("kind")
	if !ok || m.value.kind != kindString {
		return "", false
	}
	return Kind(m.value.text), true
}

// Name returns metadata.name; ok is false if it is missing or not text.
func (d Document) Name() (name string, ok bool) {
	meta, ok := d.root.lookup("metadata")
	if !ok || meta.value.kind != kindObject {
		return "", false
	}
	m, ok := meta.value.lookup("name")
	if !ok || m.value.kind != kindString {
		return "", false
	}
	return m.value.text, true
}

// JSON returns the document as JSON, the members in their order.
func (d Document) JSON() (jsontext.Value, error) {
	return d.root.JSON()
}

// problem returns an error about the document at pos and path.
func (d Document) problem(pos position, path, msg string) Problem {
	return Problem{Severity: SeverityError, File: d.File, Line: pos.line, Column: pos.column, Path: path, Message: msg}
}

// The extensions of files (B37).
const (
	extYAML = ".yaml"
	extYML  = ".yml"
	extJSON = ".json"
)

// known reports whether path has an extension of the files.
func known(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case extYAML, extYML, extJSON:
		return true
	default:
		return false
	}
}

// Files returns the files that paths name (B37): a file as it is, if it has
// an extension of the files, and of a directory all files with these
// extensions in it and its subdirectories, in the order of their paths. A
// file that two paths name comes once, where it first appears.
func Files(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !known(p) {
				return nil, fmt.Errorf("%s: not a file of commands as code: the extension must be .yaml, .yml or .json", p)
			}
			files = append(files, filepath.Clean(p))
			continue
		}
		var found []string
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && known(path) {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		slices.Sort(found)
		files = append(files, found...)
	}
	seen := make(map[string]bool, len(files))
	return slices.DeleteFunc(files, func(f string) bool {
		dup := seen[f]
		seen[f] = true
		return dup
	}), nil
}

// Read reads the documents of a file (B37): YAML with several documents
// separated by "---", or JSON, by the extension of file. Each document is
// an object; a list of objects at the top, as a JSON file may hold, gives
// one document per entry, also in YAML. Empty YAML documents do not count. The problems are syntax errors, text that is not
// UTF-8, duplicate keys and values JSON cannot hold; a file with a problem
// gives no documents.
func Read(file string, data []byte) ([]Document, []Problem) {
	fail := func(pos position, msg string) ([]Document, []Problem) {
		return nil, []Problem{{Severity: SeverityError, File: file, Line: pos.line, Column: pos.column, Message: msg}}
	}
	if !utf8.Valid(data) {
		return fail(place(data, invalidUTF8(data)), "not UTF-8")
	}
	if strings.ToLower(filepath.Ext(file)) == extJSON {
		return readJSON(file, data)
	}
	var docs []Document
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			line, msg := yamlError(err)
			return fail(position{line: line}, msg)
		}
		if len(n.Content) == 0 || n.Content[0].Kind == yaml.ScalarNode && n.Content[0].Tag == "!!null" {
			continue // an empty document
		}
		roots := []*yaml.Node{n.Content[0]}
		if n.Content[0].Kind == yaml.SequenceNode {
			roots = n.Content[0].Content
		}
		for _, r := range roots {
			c := &converter{}
			root, bad := c.value(r, "", 0)
			if bad != nil {
				return nil, []Problem{{Severity: SeverityError, File: file, Line: bad.pos.line, Column: bad.pos.column, Path: bad.path, Message: bad.msg}}
			}
			if root.kind != kindObject {
				return fail(root.pos, "a document must be an object, not "+typeName(root.kind.String()))
			}
			docs = append(docs, Document{File: file, root: root})
		}
	}
}

// yamlLine is the form of the syntax errors of YAML, which name a line.
var yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// yamlError returns the line and the message of a syntax error of YAML;
// the line is 0 if the error names none.
func yamlError(err error) (line int, msg string) {
	m := yamlLine.FindStringSubmatch(err.Error())
	if m == nil {
		return 0, "YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")
	}
	line, _ = strconv.Atoi(m[1]) // the pattern has only digits
	return line, "YAML: " + m[2]
}

// invalidUTF8 returns the offset of the first byte of data that is not
// UTF-8.
func invalidUTF8(data []byte) int {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}
		i += size
	}
	return len(data)
}

// place returns the line and column of offset in data; columns count
// characters, from 1.
func place(data []byte, offset int) position {
	before := data[:min(offset, len(data))]
	line := bytes.Count(before, []byte("\n")) + 1
	start := bytes.LastIndexByte(before, '\n') + 1
	return position{line: line, column: utf8.RuneCount(before[start:]) + 1}
}
