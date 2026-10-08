// SPDX-License-Identifier: MIT

// Package event is the in-process event bus of the core (Code-ADR-0011):
// the envelope, the catalog of event types and the non-blocking bus.
//
// Producers publish envelopes; consumers subscribe with filters and read from
// a buffered channel. Publishing never blocks: when a subscription's buffer
// is full, the event is dropped for that subscription, and the subscriber
// receives a Lag envelope before its next event.
package event

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// Type is the stable name of an event type: lowercase, dot-separated
// <area>.<subject>.<action>, e.g. "twitch.channel.follow" or "chat.message".
// A published type name is never renamed.
type Type string

// Source kinds.
const (
	SourcePlatform    = "platform"
	SourceIntegration = "integration"
	SourceSystem      = "system"
)

// Source names where an event comes from, e.g. {Kind: "platform", Name:
// "twitch"}.
type Source struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Envelope wraps every event on the bus.
type Envelope struct {
	ID      id.ID     `json:"id"`
	Time    time.Time `json:"time"`
	Source  Source    `json:"source"`
	Type    Type      `json:"type"`
	Payload any       `json:"payload"`
}

// New returns an envelope with a new ID and the current time in UTC.
func New(src Source, typ Type, payload any) Envelope {
	return Envelope{ID: id.New(), Time: time.Now().UTC(), Source: src, Type: typ, Payload: payload}
}

// TypeLag is the type of the envelope a subscriber receives after the bus
// dropped events for it. Its payload is Lag.
const TypeLag Type = "event.lag"

// Lag reports how many events a subscription missed because its buffer was
// full.
type Lag struct {
	Dropped uint64 `json:"dropped"`
}

// Payload returns the payload of e as T.
func Payload[T any](e Envelope) (T, bool) {
	p, ok := e.Payload.(T)
	return p, ok
}

// Catalog lists the event types and the Go type of their payload.
type Catalog struct {
	checks map[Type]func(any) bool
}

// NewCatalog returns a catalog that knows TypeLag.
func NewCatalog() *Catalog {
	c := &Catalog{checks: make(map[Type]func(any) bool)}
	mustRegister[Lag](c, TypeLag)
	return c
}

// ErrUnknownType is returned for an event type that is not in the catalog.
var ErrUnknownType = errors.New("unknown event type")

// ErrPayloadType is returned when a payload does not have the Go type the
// catalog lists for its event type.
var ErrPayloadType = errors.New("wrong payload type")

// Register adds typ with payload type T to the catalog. It fails for an
// invalid name or a type that is already registered.
func Register[T any](c *Catalog, typ Type) error {
	if err := validName(typ); err != nil {
		return err
	}
	if _, dup := c.checks[typ]; dup {
		return fmt.Errorf("event type %q already registered", typ)
	}
	c.checks[typ] = func(p any) bool {
		_, ok := p.(T)
		return ok
	}
	return nil
}

func mustRegister[T any](c *Catalog, typ Type) {
	if err := Register[T](c, typ); err != nil {
		panic(err)
	}
}

// Check reports whether e has a registered type and a matching payload.
func (c *Catalog) Check(e Envelope) error {
	check, ok := c.checks[e.Type]
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownType, e.Type)
	}
	if !check(e.Payload) {
		return fmt.Errorf("%w %T for %q", ErrPayloadType, e.Payload, e.Type)
	}
	return nil
}

// Types returns the registered event types in sorted order.
func (c *Catalog) Types() []Type {
	return slices.Sorted(maps.Keys(c.checks))
}

// validName checks the naming rule: at least two dot-separated parts of
// lowercase letters, digits and underscores.
func validName(typ Type) error {
	parts := strings.Split(string(typ), ".")
	if len(parts) < 2 {
		return fmt.Errorf("event type %q: want <area>.<subject>[.<action>]", typ)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("event type %q: empty part", typ)
		}
		for _, r := range p {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
				return fmt.Errorf("event type %q: invalid character %q", typ, r)
			}
		}
	}
	return nil
}
