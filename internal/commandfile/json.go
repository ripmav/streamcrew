// SPDX-License-Identifier: Apache-2.0

package commandfile

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// jsonReader reads a JSON file into values with their places. Duplicate
// names pass the decoder, so that the reader reports them with their place
// (B66).
type jsonReader struct {
	data []byte
	dec  *jsontext.Decoder
	// lines are the offsets at which the lines of data start.
	lines  []int
	values int
	// last is the last place found and its offset; places come in order,
	// so the column of the next counts on from it, not from the start of a
	// line that may be long.
	last       position
	lastOffset int
}

// readJSON reads the documents of a JSON file: an object, or a list of
// objects.
func readJSON(file string, data []byte) ([]Document, []Problem) {
	r := &jsonReader{data: data, dec: jsontext.NewDecoder(bytes.NewReader(data), jsontext.AllowDuplicateNames(true)), lines: []int{0}}
	for i, b := range data {
		if b == '\n' {
			r.lines = append(r.lines, i+1)
		}
	}
	fail := func(bad *errAt) ([]Document, []Problem) {
		return nil, []Problem{{Severity: SeverityError, File: file, Line: bad.pos.line, Column: bad.pos.column, Path: bad.path, Message: bad.msg}}
	}
	var roots []*value
	if r.dec.PeekKind() == '[' {
		if _, err := r.dec.ReadToken(); err != nil {
			return fail(r.syntax(err))
		}
		for r.dec.PeekKind() != ']' {
			r.values = 0
			v, bad := r.value("", 1)
			if bad != nil {
				return fail(bad)
			}
			roots = append(roots, v)
		}
		if _, err := r.dec.ReadToken(); err != nil {
			return fail(r.syntax(err))
		}
	} else {
		v, bad := r.value("", 0)
		if bad != nil {
			return fail(bad)
		}
		roots = append(roots, v)
	}
	at := r.pos()
	if _, err := r.dec.ReadToken(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fail(r.syntax(err))
		}
		return fail(&errAt{pos: at, msg: "more than one JSON value"})
	}
	docs := make([]Document, 0, len(roots))
	for _, v := range roots {
		if v.kind != kindObject {
			return fail(&errAt{pos: v.pos, msg: "a document must be an object, not " + typeName(v.kind.String())})
		}
		docs = append(docs, Document{File: file, root: v})
	}
	return docs, nil
}

// value reads the next value at path.
func (r *jsonReader) value(path string, depth int) (*value, *errAt) {
	pos := r.pos()
	r.values++
	switch {
	case r.values > maxValues:
		return nil, &errAt{pos, path, fmt.Sprintf("more than %d values", maxValues)}
	case depth > maxDepth:
		return nil, &errAt{pos, path, fmt.Sprintf("nested more than %d levels deep", maxDepth)}
	}
	tok, err := r.dec.ReadToken()
	if err != nil {
		return nil, r.syntax(err)
	}
	switch tok.Kind() {
	case 'n':
		return &value{kind: kindNull, pos: pos}, nil
	case 't', 'f':
		return &value{kind: kindBool, pos: pos, boolean: tok.Bool()}, nil
	case '"':
		return &value{kind: kindString, pos: pos, text: tok.String()}, nil
	case '0':
		d, err := decimal.Parse(tok.String())
		if err != nil {
			return nil, &errAt{pos, path, fmt.Sprintf("number %s: %v", tok.String(), err)}
		}
		return &value{kind: kindNumber, pos: pos, text: d.String(), number: d}, nil
	case '[':
		v := &value{kind: kindArray, pos: pos}
		for r.dec.PeekKind() != ']' {
			item, bad := r.value(path+"["+strconv.Itoa(len(v.items))+"]", depth+1)
			if bad != nil {
				return nil, bad
			}
			v.items = append(v.items, item)
		}
		return v, r.close()
	default: // '{'
		return r.object(pos, path, depth)
	}
}

// object reads the members of an object after its "{".
func (r *jsonReader) object(pos position, path string, depth int) (*value, *errAt) {
	v := &value{kind: kindObject, pos: pos}
	for r.dec.PeekKind() != '}' {
		keyPos := r.pos()
		tok, err := r.dec.ReadToken()
		if err != nil {
			return nil, r.syntax(err)
		}
		key := tok.String()
		at := join(path, key)
		if _, dup := v.lookup(key); dup {
			return nil, &errAt{keyPos, at, "duplicate key"}
		}
		item, bad := r.value(at, depth+1)
		if bad != nil {
			return nil, bad
		}
		v.members = append(v.members, member{key: key, keyPos: keyPos, value: item})
	}
	return v, r.close()
}

// close reads the end of an array or an object.
func (r *jsonReader) close() *errAt {
	if _, err := r.dec.ReadToken(); err != nil {
		return r.syntax(err)
	}
	return nil
}

// syntax returns a syntax error at its place.
func (r *jsonReader) syntax(err error) *errAt {
	if se, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
		return &errAt{pos: r.place(int(se.ByteOffset)), msg: "JSON: " + se.Err.Error()}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return &errAt{pos: r.place(len(r.data)), msg: "JSON: unexpected end of file"}
	}
	return &errAt{pos: r.pos(), msg: "JSON: " + err.Error()}
}

// pos returns the place of the next token: after white space and the
// separators "," and ":" that follow the last one.
func (r *jsonReader) pos() position {
	i := int(r.dec.InputOffset())
	for i < len(r.data) && bytes.IndexByte([]byte(" \t\r\n,:"), r.data[i]) >= 0 {
		i++
	}
	return r.place(i)
}

// place returns the line and column of offset; columns count characters.
func (r *jsonReader) place(offset int) position {
	offset = min(offset, len(r.data))
	i, found := slices.BinarySearch(r.lines, offset)
	if !found {
		i--
	}
	p := position{line: i + 1}
	if r.last.line == p.line && offset >= r.lastOffset {
		p.column = r.last.column + utf8.RuneCount(r.data[r.lastOffset:offset])
	} else {
		p.column = utf8.RuneCount(r.data[r.lines[i]:offset]) + 1
	}
	r.last, r.lastOffset = p, offset
	return p
}
