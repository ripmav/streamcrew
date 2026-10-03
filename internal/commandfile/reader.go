// SPDX-License-Identifier: Apache-2.0

package commandfile

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// reader reads the members of a document and keeps its first problem
// (B32); once it has one, it reads nothing more.
type reader struct {
	doc Document
	err *Problem
}

// fail records a problem at pos and path unless there is one already.
func (r *reader) fail(pos position, path, msg string) {
	if r.err == nil {
		p := r.doc.problem(pos, path, msg)
		r.err = &p
	}
}

// members checks that the object v at path has only the members allowed
// (B4).
func (r *reader) members(v *value, path string, allowed ...string) {
	if r.err != nil || v == nil {
		return
	}
	if v.kind != kindObject {
		r.fail(v.pos, path, "must be an object, not "+describe(v))
		return
	}
	for _, m := range v.members {
		if !slices.Contains(allowed, m.key) {
			r.fail(m.keyPos, join(path, m.key), "unknown member")
			return
		}
	}
}

// require returns the member key of the object v at path; nil, and a
// problem, if it is missing.
func (r *reader) require(v *value, path, key string) *value {
	if r.err != nil {
		return nil
	}
	m, ok := v.lookup(key)
	if !ok {
		r.fail(v.pos, path, "missing member "+quote(key))
		return nil
	}
	return m.value
}

// optional returns the member key of the object v; nil if it is missing or
// there is a problem already. A required member that is missing is a
// problem.
func (r *reader) optional(v *value, path, key string, required bool) *value {
	if r.err != nil || v == nil || v.kind != kindObject {
		return nil
	}
	if required {
		return r.require(v, path, key)
	}
	m, ok := v.lookup(key)
	if !ok {
		return nil
	}
	return m.value
}

// text returns the text member key of v; def if it is missing and not
// required (B4).
func (r *reader) text(v *value, path, key string, required bool, def string) string {
	m := r.optional(v, path, key, required)
	if m == nil {
		return def
	}
	if m.kind != kindString {
		r.fail(m.pos, join(path, key), "must be text, not "+describe(m))
		return def
	}
	return m.text
}

// texts returns the list of texts key of v; nil if it is missing and not
// required.
func (r *reader) texts(v *value, path, key string, required bool) []string {
	m := r.optional(v, path, key, required)
	if m == nil {
		return nil
	}
	at := join(path, key)
	if m.kind != kindArray {
		r.fail(m.pos, at, "must be a list, not "+describe(m))
		return nil
	}
	out := make([]string, 0, len(m.items))
	for i, item := range m.items {
		if item.kind != kindString {
			r.fail(item.pos, at+"["+itoa(i)+"]", "must be text, not "+describe(item))
			return nil
		}
		out = append(out, item.text)
	}
	return out
}

// boolean returns the truth value key of v; def if it is missing (B4,
// B5).
func (r *reader) boolean(v *value, path, key string, def bool) bool {
	m := r.optional(v, path, key, false)
	if m == nil {
		return def
	}
	if m.kind != kindBool {
		r.fail(m.pos, join(path, key), "must be true or false, not "+describe(m))
		return def
	}
	return m.boolean
}

// duration returns the duration key of v, text such as "30s" or "1m30s"
// (B5, Code-ADR-0009); 0 if it is missing and not required.
func (r *reader) duration(v *value, path, key string, required bool) time.Duration {
	m := r.optional(v, path, key, required)
	if m == nil {
		return 0
	}
	if m.kind != kindString {
		r.fail(m.pos, join(path, key), "must be a duration such as 30s, not "+describe(m))
		return 0
	}
	d, err := time.ParseDuration(m.text)
	if err != nil {
		r.fail(m.pos, join(path, key), "must be a duration such as 30s or 1m30s")
		return 0
	}
	return d
}

// valid reports a member key of v whose value is not one of values, as
// ok says; a missing member took its default.
func valid[T ~string](r *reader, v *value, path, key string, ok bool, values ...T) {
	if ok {
		return
	}
	names := make([]string, len(values))
	for i, val := range values {
		names[i] = string(val)
	}
	m, _ := v.lookup(key)
	r.fail(m.value.pos, join(path, key), "must be "+oneOfValues(names))
}

// decodeFailed reports an error of decoding the document v at path: at the
// member it names if there is one, else at v. Documents nested too deep
// are reported at v.
func (r *reader) decodeFailed(v *value, path string, err error) {
	if errors.Is(err, polydoc.ErrTooDeep) {
		r.fail(v.pos, path, fmt.Sprintf("actions nested more than %d levels deep", polydoc.MaxDepth))
		return
	}
	se, ok := errors.AsType[*json.SemanticError](err)
	if !ok {
		r.fail(v.pos, path, cleanMessage(err))
		return
	}
	at, atPath, keyPos := v.at(se.JSONPointer, path)
	switch {
	case errors.Is(err, json.ErrUnknownName):
		r.fail(keyPos, atPath, "unknown member")
	case se.Err != nil:
		r.fail(at.pos, atPath, cleanMessage(se.Err))
	default:
		r.fail(at.pos, atPath, "has the wrong type: "+describe(at))
	}
}

// at returns the value that pointer names in v, which is at path, its path
// and the place of its key; v itself if pointer names nothing in it.
func (v *value) at(pointer jsontext.Pointer, path string) (*value, string, position) {
	cur, keyPos := v, v.pos
	for tok := range pointer.Tokens() {
		switch cur.kind {
		case kindObject:
			m, ok := cur.lookup(tok)
			if !ok {
				return cur, path, keyPos
			}
			cur, path, keyPos = m.value, join(path, tok), m.keyPos
		case kindArray:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(cur.items) {
				return cur, path, keyPos
			}
			cur, path, keyPos = cur.items[i], path+"["+tok+"]", cur.items[i].pos
		default:
			return cur, path, keyPos
		}
	}
	return cur, path, keyPos
}

// cleanMessage returns the message of err without what the place already
// says: the family and type of the document, e.g. 'action "wait": ', and
// the words "invalid action".
func cleanMessage(err error) string {
	msg := err.Error()
	for _, family := range []string{"action ", "requirement "} {
		for {
			rest, cut := strings.CutPrefix(msg, family+`"`)
			_, after, ok := strings.Cut(rest, `": `)
			if !cut || !ok {
				break
			}
			msg = after
		}
	}
	return strings.ReplaceAll(msg, action.ErrInvalid.Error()+": ", "")
}

// trimInvalid returns the message of a validation error of the command
// package without its prefix: the place says what is invalid.
func trimInvalid(err error) string {
	return strings.TrimPrefix(err.Error(), command.ErrInvalid.Error()+": ")
}

// describe names the type and value of v in a message.
func describe(v *value) string {
	switch v.kind {
	case kindNumber:
		return "the number " + v.text
	case kindString:
		return "the text " + quote(v.text)
	case kindBool:
		return strconv.FormatBool(v.boolean)
	default:
		return typeName(v.kind.String())
	}
}

// oneOfValues writes the values a member allows.
func oneOfValues(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = quote(v)
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return "one of " + strings.Join(quoted, ", ")
}

// quote returns s in double quotes, as Go writes it.
func quote(s string) string {
	return strconv.Quote(s)
}

// itoa writes an index of a list.
func itoa(i int) string {
	return strconv.Itoa(i)
}
