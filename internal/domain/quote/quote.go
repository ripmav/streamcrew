// SPDX-License-Identifier: Apache-2.0

// Package quote is the model of quotes (spec counters-and-quotes.md, B20 to
// B26): collected sayings with a number, text, game and time.
//
// The output format (B24), the prebuilt quote commands (B22) and the import
// (B25) follow with the template engine and the prebuilt commands (roadmap
// phases 3 and 5).
package quote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// Quote is a saved quote (B20).
type Quote struct {
	// Number is unique per profile, given out in order when a quote is added
	// and never given out again after a quote is deleted (B21, B41). It is
	// how users refer to a quote.
	Number int64
	// ID identifies the quote like every entity (Code-ADR-0009).
	ID id.ID
	// Text is the quote itself.
	Text string
	// Game is the game or category at the time of the quote.
	Game string
	// QuotedAt is when the quote was said.
	QuotedAt time.Time
	// AddedBy is the user who added the quote; zero if unknown (B26).
	AddedBy id.ID
	// CreatedAt and UpdatedAt are maintained by the repository.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrInvalid is wrapped by validation errors.
var ErrInvalid = errors.New("invalid quote")

// Validate checks a quote before it is stored.
func (q Quote) Validate() error {
	if strings.TrimSpace(q.Text) == "" {
		return fmt.Errorf("%w: empty text", ErrInvalid)
	}
	if q.Number < 0 {
		return fmt.Errorf("%w: negative number", ErrInvalid)
	}
	return nil
}

// Repository stores quotes; *store.Store implements it. Methods return an
// error wrapping store.ErrNotFound for a missing quote and store.ErrConflict
// for a number in use (B42).
type Repository interface {
	// AddQuote stores a new quote and returns it as stored. A zero Number
	// gets the next number (B21); a given one, e.g. from an import, must be
	// free (B42). A zero ID or QuotedAt is filled in.
	AddQuote(ctx context.Context, q Quote) (Quote, error)
	// UpdateQuote replaces text, game, time and author of the quote with
	// q.Number.
	UpdateQuote(ctx context.Context, q Quote) (Quote, error)
	// DeleteQuote deletes a quote; its number is not given out again (B41).
	DeleteQuote(ctx context.Context, number int64) error
	// Quote returns the quote with a number (B23).
	Quote(ctx context.Context, number int64) (Quote, error)
	// RandomQuote returns a random quote (B23).
	RandomQuote(ctx context.Context) (Quote, error)
	// LatestQuote returns the quote with the highest number (B23).
	LatestQuote(ctx context.Context) (Quote, error)
	// QuoteCount returns the number of quotes (B23).
	QuoteCount(ctx context.Context) (int64, error)
	// Quotes returns all quotes by number.
	Quotes(ctx context.Context) ([]Quote, error)
}
