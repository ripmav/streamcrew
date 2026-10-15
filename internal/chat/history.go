// SPDX-License-Identifier: MIT

package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/logging"
)

// Limits of the protocol (spec chat-service.md, B18).
const (
	// ProtocolMaxSize is the size in bytes at which a protocol file
	// rotates.
	ProtocolMaxSize = 10 * 1024 * 1024
	// ProtocolMaxFiles is the number of rotated protocol files to keep.
	ProtocolMaxFiles = 3
)

// errProtocolDead is the error of a protocol file that could not be
// opened; it is not retried until the service ends, so that a broken
// disk does not make every message pay for an open attempt.
//
//	shortcut: no retry after a failure, retrying per message would
//	redesign the disk per message; a restart reopens.
var errProtocolDead = errors.New("the protocol file is not open")

// Entry is a message of the chat history and the chat protocol (B13).
type Entry struct {
	// Time is when the pipeline took the message, in UTC.
	Time time.Time `json:"time"`
	// Platform is the platform the message came from.
	Platform platform.Name `json:"platform"`
	// Channel is the channel the message came from; empty for the home
	// channel.
	Channel string `json:"channel,omitempty"`
	// UserID and Name are the sender.
	UserID id.ID  `json:"user"`
	Name   string `json:"name"`
	// Text is the text as written.
	Text string `json:"text"`
	// FromBot reports whether the bot account of the channel wrote it.
	FromBot bool `json:"fromBot,omitempty"`
	// MessageID is the platform's ID of the message; empty if there is
	// none.
	MessageID string `json:"messageId,omitempty"`
}

// protocolFile is a protocol on disk (B17, B18).
type protocolFile struct {
	f *logging.RotatingFile
}

// Write implements io.Writer.
func (p *protocolFile) Write(data []byte) (int, error) { return p.f.Write(data) }

// Close closes the file.
func (p *protocolFile) Close() error { return p.f.Close() }

// ring is the history of one platform: the last entries, oldest first
// (B12, B16).
type ring struct {
	mu sync.Mutex
	es []Entry
}

// push adds e and keeps at most size entries.
func (r *ring) push(e Entry, size int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.es = append(r.es, e)
	if len(r.es) > size {
		r.es = r.es[len(r.es)-size:]
	}
}

// get returns the history, oldest first; a copy, safe to use.
func (r *ring) get() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.es)
}

// countSince counts the entries since (inclusive) that are not from the bot;
// the entries are stored oldest first.
func (r *ring) countSince(since time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.es {
		if !e.Time.Before(since) && !e.FromBot {
			n++
		}
	}
	return n
}

// history returns the history of p, created if new.
func (s *Service) history(p platform.Name) *ring {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.histories[p]
	if !ok {
		r = &ring{}
		s.histories[p] = r
	}
	return r
}

// Record takes e into the history of its platform and, if the protocol is
// on, into the protocol file (B14 to B18). The size applies from the next
// save (B15). A failure to read the settings is an error; a write failure
// of the protocol is a warning and never an error (B18, B37).
func (s *Service) Record(ctx context.Context, e Entry) error {
	cfg, err := s.ports.Settings(ctx)
	if err != nil {
		return fmt.Errorf("chat history: %w", err)
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	s.history(e.Platform).push(e, cfg.HistorySize)
	if !cfg.Protocol {
		return nil
	}
	f, err := s.protocolFile(e.Platform)
	if err != nil {
		s.ports.Logger.WarnContext(ctx, "chat protocol not written", "platform", e.Platform, "error", err)
		return nil
	}
	line, err := json.Marshal(e)
	if err != nil {
		s.ports.Logger.WarnContext(ctx, "chat protocol not written", "platform", e.Platform, "error", err)
		return nil
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		s.ports.Logger.WarnContext(ctx, "chat protocol not written", "platform", e.Platform, "error", err)
	}
	return nil
}

// History returns the chat history of p, oldest first (B12, B16).
func (s *Service) History(p platform.Name) []Entry {
	return s.history(p).get()
}

// MessagesSince counts the chat messages of p since (inclusive) that were
// not written by the bot (roadmap 5.4): the timer uses it for its minimum
// of chat messages. It is bounded by the history size.
func (s *Service) MessagesSince(p platform.Name, since time.Time) int {
	return s.history(p).countSince(since)
}

// protocolFile returns the protocol file of p, created if new (B17, B18).
func (s *Service) protocolFile(p platform.Name) (*protocolFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.protocols[p]; ok {
		return f, nil
	}
	if s.protocolFailed[p] {
		return nil, errProtocolDead
	}
	path := filepath.Join(s.ports.DataDir, "chat", string(p)+".jsonl")
	f, err := logging.OpenRotatingFile(path, ProtocolMaxSize, ProtocolMaxFiles)
	if err != nil {
		s.protocolFailed[p] = true
		return nil, err
	}
	w := &protocolFile{f: f}
	s.protocols[p] = w
	return w, nil
}
