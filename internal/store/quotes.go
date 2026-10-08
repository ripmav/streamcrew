// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/quote"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

var _ quote.Repository = (*Store)(nil)

// AddQuote implements quote.Repository. The numbers come from SQLite's
// AUTOINCREMENT, which never gives a number out twice and moves past
// numbers given on import (B21, B41, B42).
func (s *Store) AddQuote(ctx context.Context, q quote.Quote) (quote.Quote, error) {
	if err := q.Validate(); err != nil {
		return quote.Quote{}, err
	}
	if q.ID.IsZero() {
		q.ID = id.New()
	}
	t := now()
	if q.QuotedAt.IsZero() {
		q.QuotedAt = t
	}
	q.QuotedAt = fromMillis(q.QuotedAt.UnixMilli()) // as stored
	q.CreatedAt, q.UpdatedAt = t, t
	err := s.Write(ctx, func(qs *sqlcgen.Queries) error {
		if q.Number > 0 {
			return qs.InsertQuoteWithNumber(ctx, sqlcgen.InsertQuoteWithNumberParams{
				Number:    q.Number,
				ID:        q.ID.String(),
				Text:      q.Text,
				Game:      q.Game,
				QuotedAt:  q.QuotedAt.UnixMilli(),
				AddedBy:   nullID(q.AddedBy),
				CreatedAt: t.UnixMilli(),
				UpdatedAt: t.UnixMilli(),
			})
		}
		n, err := qs.InsertQuote(ctx, sqlcgen.InsertQuoteParams{
			ID:        q.ID.String(),
			Text:      q.Text,
			Game:      q.Game,
			QuotedAt:  q.QuotedAt.UnixMilli(),
			AddedBy:   nullID(q.AddedBy),
			CreatedAt: t.UnixMilli(),
			UpdatedAt: t.UnixMilli(),
		})
		if err != nil {
			return err
		}
		q.Number = n
		return nil
	})
	if err != nil {
		return quote.Quote{}, fmt.Errorf("add quote: %w", err)
	}
	return q, nil
}

// UpdateQuote implements quote.Repository.
func (s *Store) UpdateQuote(ctx context.Context, q quote.Quote) (quote.Quote, error) {
	if err := q.Validate(); err != nil {
		return quote.Quote{}, err
	}
	var out quote.Quote
	err := s.Write(ctx, func(qs *sqlcgen.Queries) error {
		n, err := qs.UpdateQuote(ctx, sqlcgen.UpdateQuoteParams{
			Text:      q.Text,
			Game:      q.Game,
			QuotedAt:  q.QuotedAt.UnixMilli(),
			AddedBy:   nullID(q.AddedBy),
			UpdatedAt: now().UnixMilli(),
			Number:    q.Number,
		})
		if err != nil {
			return err
		}
		if err := notFound(n, fmt.Sprintf("quote %d", q.Number)); err != nil {
			return err
		}
		row, err := qs.GetQuote(ctx, q.Number)
		if err != nil {
			return err
		}
		out, err = toQuote(row)
		return err
	})
	if err != nil {
		return quote.Quote{}, fmt.Errorf("update quote %d: %w", q.Number, err)
	}
	return out, nil
}

// DeleteQuote implements quote.Repository.
func (s *Store) DeleteQuote(ctx context.Context, number int64) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		n, err := q.DeleteQuote(ctx, number)
		if err != nil {
			return err
		}
		return notFound(n, fmt.Sprintf("quote %d", number))
	})
}

// Quote implements quote.Repository.
func (s *Store) Quote(ctx context.Context, number int64) (quote.Quote, error) {
	row, err := s.reader().GetQuote(ctx, number)
	if err != nil {
		return quote.Quote{}, fmt.Errorf("quote %d: %w", number, translate(err))
	}
	return toQuote(row)
}

// RandomQuote implements quote.Repository.
func (s *Store) RandomQuote(ctx context.Context) (quote.Quote, error) {
	row, err := s.reader().RandomQuote(ctx)
	if err != nil {
		return quote.Quote{}, fmt.Errorf("random quote: %w", translate(err))
	}
	return toQuote(row)
}

// LatestQuote implements quote.Repository.
func (s *Store) LatestQuote(ctx context.Context) (quote.Quote, error) {
	row, err := s.reader().LatestQuote(ctx)
	if err != nil {
		return quote.Quote{}, fmt.Errorf("latest quote: %w", translate(err))
	}
	return toQuote(row)
}

// QuoteCount implements quote.Repository.
func (s *Store) QuoteCount(ctx context.Context) (int64, error) {
	n, err := s.reader().CountQuotes(ctx)
	if err != nil {
		return 0, fmt.Errorf("count quotes: %w", translate(err))
	}
	return n, nil
}

// Quotes implements quote.Repository.
func (s *Store) Quotes(ctx context.Context) ([]quote.Quote, error) {
	rows, err := s.reader().ListQuotes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list quotes: %w", translate(err))
	}
	out := make([]quote.Quote, 0, len(rows))
	for _, row := range rows {
		q, err := toQuote(row)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, nil
}

func toQuote(row sqlcgen.Quote) (quote.Quote, error) {
	qid, err := id.Parse(row.ID)
	if err != nil {
		return quote.Quote{}, err
	}
	addedBy, err := parseNullID(row.AddedBy)
	if err != nil {
		return quote.Quote{}, err
	}
	return quote.Quote{
		Number:    row.Number,
		ID:        qid,
		Text:      row.Text,
		Game:      row.Game,
		QuotedAt:  fromMillis(row.QuotedAt),
		AddedBy:   addedBy,
		CreatedAt: fromMillis(row.CreatedAt),
		UpdatedAt: fromMillis(row.UpdatedAt),
	}, nil
}
