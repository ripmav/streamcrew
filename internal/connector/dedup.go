// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultDedupTTL is how long a Dedup remembers an ID unless NewDedup gets
// another duration: long enough for a platform to repeat a message after a
// lost connection.
const DefaultDedupTTL = 10 * time.Minute

// ErrInvalidTTL is returned by NewDedup for a duration that is not
// positive.
var ErrInvalidTTL = errors.New("invalid time to live")

// Dedup remembers the IDs of the messages and events an adapter received,
// so that it hands each one to the Receiver once (spec events.md, B22;
// Code-ADR-0011, point 5). It is safe for concurrent use.
type Dedup struct {
	ttl time.Duration

	mu sync.Mutex
	// until holds the end of each remembered ID.
	until map[string]time.Time
	// order holds the remembered IDs in the order they came; as the time
	// to live is the same for all, their ends come in this order too.
	order []string
}

// NewDedup returns a Dedup that remembers an ID for ttl.
func NewDedup(ttl time.Duration) (*Dedup, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("new dedup: %w: %s", ErrInvalidTTL, ttl)
	}
	return &Dedup{ttl: ttl, until: make(map[string]time.Time)}, nil
}

// First reports whether the platform's ID messageID comes for the first
// time within the time to live, and remembers it. An adapter checks only
// messages and events the platform gave an ID; an empty ID is never
// remembered, so First reports true for it.
func (d *Dedup) First(messageID string) bool {
	if messageID == "" {
		return true
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.forget(now)
	if _, seen := d.until[messageID]; seen {
		return false
	}
	d.until[messageID] = now.Add(d.ttl)
	d.order = append(d.order, messageID)
	return true
}

// Len returns the number of IDs remembered now.
func (d *Dedup) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.forget(time.Now())
	return len(d.until)
}

// forget drops the IDs whose time to live ended at or before now. d.mu is
// held.
func (d *Dedup) forget(now time.Time) {
	n := 0
	for _, messageID := range d.order {
		if d.until[messageID].After(now) {
			break
		}
		delete(d.until, messageID)
		n++
	}
	if n > 0 {
		clear(d.order[:n])
		d.order = d.order[n:]
	}
}
