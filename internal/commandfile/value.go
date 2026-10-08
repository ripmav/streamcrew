// SPDX-License-Identifier: MIT

package commandfile

import (
	"encoding/json/jsontext"
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// Limits of a document, against files that expand aliases without end.
const (
	// maxValues is the most values a document may have, aliases expanded.
	maxValues = 1 << 20
	// maxDepth is the deepest nesting of a document: actions nest at most
	// 16 levels (Code-ADR-0013, point 5), each with a few levels of their
	// own.
	maxDepth = 256
)

// valueKind is the JSON type of a value.
type valueKind int

// The kinds of values.
const (
	kindNull valueKind = iota + 1
	kindBool
	kindNumber
	kindString
	kindArray
	kindObject
)

// String names the kind in a message, as JSON Schema names the types.
func (k valueKind) String() string {
	switch k {
	case kindNull:
		return "null"
	case kindBool:
		return "boolean"
	case kindNumber:
		return "number"
	case kindString:
		return "string"
	case kindArray:
		return "array"
	default:
		return "object"
	}
}

// position is a place in a file, from 1.
type position struct {
	line, column int
}

// value is a value of a document as JSON sees it, with its place in the
// file.
type value struct {
	kind valueKind
	pos  position
	// text is a string, or a number in the canonical form of
	// decimal.Decimal.
	text    string
	number  decimal.Decimal
	boolean bool
	items   []*value
	members []member
}

// member is a member of an object.
type member struct {
	key    string
	keyPos position
	value  *value
}

// lookup returns the member key of an object; ok is false if there is
// none.
func (v *value) lookup(key string) (m member, ok bool) {
	for _, m := range v.members {
		if m.key == key {
			return m, true
		}
	}
	return member{}, false
}

// JSON returns v as JSON, the members of objects in their order.
func (v *value) JSON() (jsontext.Value, error) {
	return v.appendJSON(nil)
}

func (v *value) appendJSON(b []byte) ([]byte, error) {
	var err error
	switch v.kind {
	case kindNull:
		return append(b, "null"...), nil
	case kindBool:
		return strconv.AppendBool(b, v.boolean), nil
	case kindNumber:
		return append(b, v.text...), nil
	case kindString:
		return jsontext.AppendQuote(b, v.text)
	case kindArray:
		b = append(b, '[')
		for i, item := range v.items {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = item.appendJSON(b); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	default:
		b = append(b, '{')
		for i, m := range v.members {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = jsontext.AppendQuote(b, m.key); err != nil {
				return nil, err
			}
			b = append(b, ':')
			if b, err = m.value.appendJSON(b); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	}
}

// converter turns the nodes of a YAML document into values and counts them
// against the limits.
type converter struct {
	values int
}

// errAt is an error at a place of the document; path is empty for the
// whole document.
type errAt struct {
	pos  position
	path string
	msg  string
}

// value returns the value of node n at path. YAML is read by version 1.2
// (B5): only true and false are truth values, "no" and "on" are text.
// Numbers are read exactly from their text (Code-ADR-0020); forms
// decimal.Parse does not read, e.g. "0o17" or ".inf", are errors. Keys are
// scalars, taken as their text, and appear once (B4, B66).
func (c *converter) value(n *yaml.Node, path string, depth int) (*value, *errAt) {
	pos := position{n.Line, n.Column}
	c.values++
	switch {
	case c.values > maxValues:
		return nil, &errAt{pos, path, fmt.Sprintf("more than %d values", maxValues)}
	case depth > maxDepth:
		return nil, &errAt{pos, path, fmt.Sprintf("nested more than %d levels deep", maxDepth)}
	}
	switch n.Kind {
	case yaml.AliasNode:
		return c.value(n.Alias, path, depth) // an alias adds no level
	case yaml.SequenceNode:
		v := &value{kind: kindArray, pos: pos, items: make([]*value, 0, len(n.Content))}
		for i, item := range n.Content {
			iv, err := c.value(item, path+"["+strconv.Itoa(i)+"]", depth+1)
			if err != nil {
				return nil, err
			}
			v.items = append(v.items, iv)
		}
		return v, nil
	case yaml.MappingNode:
		return c.object(n, path, depth)
	case yaml.ScalarNode:
		return scalar(n, path)
	default:
		return nil, &errAt{pos, path, "unexpected YAML node"}
	}
}

// object returns the value of a mapping.
func (c *converter) object(n *yaml.Node, path string, depth int) (*value, *errAt) {
	v := &value{kind: kindObject, pos: position{n.Line, n.Column}, members: make([]member, 0, len(n.Content)/2)}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, item := n.Content[i], n.Content[i+1]
		keyPos := position{k.Line, k.Column}
		switch {
		case k.Kind != yaml.ScalarNode:
			return nil, &errAt{keyPos, path, "a key must be text"}
		case k.Tag == "!!merge":
			return nil, &errAt{keyPos, path, "merge keys (<<) are not supported"}
		}
		key := k.Value
		at := key
		if path != "" {
			at = path + "." + key
		}
		if _, dup := v.lookup(key); dup {
			return nil, &errAt{keyPos, at, "duplicate key"}
		}
		iv, err := c.value(item, at, depth+1)
		if err != nil {
			return nil, err
		}
		v.members = append(v.members, member{key: key, keyPos: keyPos, value: iv})
	}
	return v, nil
}

// scalar returns the value of a scalar by its resolved tag.
func scalar(n *yaml.Node, path string) (*value, *errAt) {
	pos := position{n.Line, n.Column}
	switch n.Tag {
	case "!!null":
		return &value{kind: kindNull, pos: pos}, nil
	case "!!bool":
		switch n.Value {
		case "true":
			return &value{kind: kindBool, pos: pos, boolean: true}, nil
		case "false":
			return &value{kind: kindBool, pos: pos}, nil
		default:
			return nil, &errAt{pos, path, fmt.Sprintf("truth value %q: only true and false", n.Value)}
		}
	case "!!int", "!!float":
		d, err := decimal.Parse(n.Value)
		if err != nil {
			return nil, &errAt{pos, path, fmt.Sprintf("number %q: %v", n.Value, err)}
		}
		return &value{kind: kindNumber, pos: pos, text: d.String(), number: d}, nil
	case "!!str", "!!timestamp":
		return &value{kind: kindString, pos: pos, text: n.Value}, nil
	default:
		return nil, &errAt{pos, path, "unsupported YAML tag " + n.Tag}
	}
}

// typeName names a JSON type in a message.
func typeName(typ string) string {
	switch typ {
	case "string":
		return "text"
	case "array":
		return "a list"
	case "number":
		return "a number"
	case "boolean":
		return "a truth value"
	case "object":
		return "an object"
	default:
		return typ
	}
}

// join returns the path of the member key of the object at path.
func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
