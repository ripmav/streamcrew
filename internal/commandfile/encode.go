// SPDX-License-Identifier: Apache-2.0

package commandfile

import (
	"bytes"
	"encoding/json/jsontext"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// EncodeYAML writes documents as YAML, separated by "---" (B37), indented
// by two spaces, the members in their order (B36). Text that YAML would
// read as something else, e.g. "123", is quoted. Without documents the
// YAML is empty.
func EncodeYAML(docs []Exported) ([]byte, error) {
	if len(docs) == 0 {
		return []byte{}, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, d := range docs {
		if err := enc.Encode(d.root.node()); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeJSON writes documents as JSON, indented by two spaces (B37): a
// list of documents, or with one, if single is set, the document itself.
func EncodeJSON(docs []Exported, single bool) ([]byte, error) {
	var out []byte
	var err error
	if single && len(docs) == 1 {
		out, err = docs[0].root.JSON()
	} else {
		list := &value{kind: kindArray, items: make([]*value, len(docs))}
		for i, d := range docs {
			list.items[i] = d.root
		}
		out, err = list.JSON()
	}
	if err != nil {
		return nil, err
	}
	v := jsontext.Value(out)
	if err := v.Indent(jsontext.WithIndent("  ")); err != nil {
		return nil, err
	}
	return append(v, '\n'), nil
}

// node returns v as a YAML node.
func (v *value) node() *yaml.Node {
	switch v.kind {
	case kindNull:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case kindBool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v.boolean)}
	case kindNumber:
		tag := "!!float"
		if v.number.IsWhole() {
			tag = "!!int"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: v.text}
	case kindString:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v.text}
	case kindArray:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: make([]*yaml.Node, len(v.items))}
		for i, item := range v.items {
			n.Content[i] = item.node()
		}
		return n
	default:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: make([]*yaml.Node, 0, 2*len(v.members))}
		for _, m := range v.members {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: m.key}, m.value.node())
		}
		return n
	}
}
