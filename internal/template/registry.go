// SPDX-License-Identifier: MIT

package template

import (
	"errors"
	"fmt"
	"strings"
)

// Identifier is a built-in identifier with a fixed name.
type Identifier struct {
	// Name is the name without "$", in lower-case ASCII letters and digits.
	Name    string
	Resolve Resolver
	// Uncached identifiers are resolved at every occurrence instead of once
	// per render (B21).
	Uncached bool
}

// Pattern is a group of built-in identifiers with variable parts in their
// names, such as the numbers in arg<n>text (B6).
type Pattern struct {
	// Name describes the pattern in errors and logs, e.g. "arg<n>text".
	Name string
	// Prefixes are the fixed beginnings of the names the pattern matches,
	// e.g. "arg" for arg<n>text. Registry.Reserved uses them (B12).
	Prefixes []string
	// Match returns the length of the longest beginning of token that is an
	// identifier of the pattern, and its resolver; 0 if there is none. token
	// is in lower case, without "$". Invalid numbers are no match (B6).
	// Match is a small parser, not a regular expression (Code-ADR-0012).
	Match func(token string) (int, Resolver)
	// Uncached identifiers are resolved at every occurrence instead of once
	// per render, e.g. $randomnumber<max> (B21).
	Uncached bool
}

// Family is a group of built-in identifiers, such as the user identifiers.
// Functions like CharacterFamily return them; the composition root
// assembles them into a Registry.
type Family struct {
	// Name identifies the family in errors.
	Name        string
	Identifiers []Identifier
	Patterns    []Pattern
}

// ErrInvalidRegistry is wrapped by the errors of NewRegistry.
var ErrInvalidRegistry = errors.New("invalid identifier registry")

// Registry holds the built-in identifiers (B10, source 4): the fixed names
// in a prefix tree and the patterns. The zero value is an empty registry. A
// Registry does not change after NewRegistry and is safe for concurrent
// use.
type Registry struct {
	root     node
	patterns []Pattern
}

// node is a node of the prefix tree.
type node struct {
	children map[byte]*node
	// ident is the identifier whose name ends here; nil if none does.
	ident *Identifier
	// below is the name of an identifier at or below this node, for
	// Reserved.
	below string
}

// NewRegistry builds a registry from families. It rejects invalid names,
// names that two identifiers share and incomplete entries.
func NewRegistry(families ...Family) (*Registry, error) {
	r := &Registry{}
	owner := make(map[string]string) // identifier name → family name
	for _, f := range families {
		for _, ident := range f.Identifiers {
			if err := checkName(ident.Name); err != nil {
				return nil, fmt.Errorf("%w: family %q: identifier: %w", ErrInvalidRegistry, f.Name, err)
			}
			if ident.Resolve == nil {
				return nil, fmt.Errorf("%w: family %q: identifier %q has no resolver", ErrInvalidRegistry, f.Name, ident.Name)
			}
			if other, dup := owner[ident.Name]; dup {
				return nil, fmt.Errorf("%w: identifier %q is in families %q and %q", ErrInvalidRegistry, ident.Name, other, f.Name)
			}
			owner[ident.Name] = f.Name
			r.insert(ident)
		}
		for _, p := range f.Patterns {
			if p.Match == nil || len(p.Prefixes) == 0 {
				return nil, fmt.Errorf("%w: family %q: pattern %q needs a match function and prefixes", ErrInvalidRegistry, f.Name, p.Name)
			}
			for _, prefix := range p.Prefixes {
				if err := checkName(prefix); err != nil {
					return nil, fmt.Errorf("%w: family %q: pattern %q: prefix: %w", ErrInvalidRegistry, f.Name, p.Name, err)
				}
			}
			r.patterns = append(r.patterns, p)
		}
	}
	return r, nil
}

// checkName checks the name of an identifier or the prefix of a pattern.
func checkName(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	for i := range len(name) {
		if !isNameByte(name[i]) {
			return fmt.Errorf("name %q: want lower-case ASCII letters and digits", name)
		}
	}
	return nil
}

// insert adds ident to the prefix tree.
func (r *Registry) insert(ident Identifier) {
	n := &r.root
	for i := range len(ident.Name) {
		if n.below == "" {
			n.below = ident.Name
		}
		child := n.children[ident.Name[i]]
		if child == nil {
			child = &node{}
			if n.children == nil {
				n.children = make(map[byte]*node)
			}
			n.children[ident.Name[i]] = child
		}
		n = child
	}
	if n.below == "" {
		n.below = ident.Name
	}
	n.ident = &ident
}

// match finds the built-in identifier with the longest name that token
// starts with. For equal lengths, a fixed name wins over a pattern.
func (r *Registry) match(token string) hit {
	var h hit
	n := &r.root
	for i := range len(token) {
		if n = n.children[token[i]]; n == nil {
			break
		}
		if n.ident != nil {
			h = hit{n: i + 1, resolve: n.ident.Resolve, uncached: n.ident.Uncached}
		}
	}
	for _, p := range r.patterns {
		if m, resolve := p.Match(token); m > h.n && m <= len(token) && resolve != nil {
			h = hit{n: m, resolve: resolve, uncached: p.Uncached}
		}
	}
	return h
}

// Reserved reports whether a custom name, such as the name of a counter or
// of a local or global value, collides with a built-in identifier, and
// returns the identifier or pattern it collides with (B12, B74; spec
// counters-and-quotes.md, B7).
//
// A name collides if it starts with the name of a built-in identifier or
// with the fixed beginning of a pattern, because it would hide the built-in
// identifier in texts under the rule of the longest prefix; and if it is
// the beginning of one, like "user" for "username", because texts with a
// typo would silently use the custom name. The case of name does not
// matter.
func (r *Registry) Reserved(name string) (string, bool) {
	name = strings.ToLower(name)
	n := &r.root
	for i := range len(name) {
		if n = n.children[name[i]]; n == nil {
			break
		}
		if n.ident != nil {
			return n.ident.Name, true
		}
		if i == len(name)-1 {
			return n.below, true
		}
	}
	for _, p := range r.patterns {
		for _, prefix := range p.Prefixes {
			if strings.HasPrefix(name, prefix) || strings.HasPrefix(prefix, name) {
				return p.Name, true
			}
		}
	}
	return "", false
}

// hit is a match of a token: the length of the identifier's name and its
// resolver; n is 0 for none.
type hit struct {
	n        int
	resolve  Resolver
	uncached bool
}
