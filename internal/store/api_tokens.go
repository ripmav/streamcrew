// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/apitoken"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// APIToken returns the API token with the ID; found is false if there is
// none.
func (s *Store) APIToken(ctx context.Context, id string) (apitoken.Token, bool, error) {
	row, err := s.reader().GetAPIToken(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return apitoken.Token{}, false, nil
	}
	if err != nil {
		return apitoken.Token{}, false, fmt.Errorf("api token %s: %w", id, translate(err))
	}
	return toAPIToken(row), true, nil
}

// APITokenByHash returns the API token with the SHA-256 hash of its value;
// found is false if there is none.
func (s *Store) APITokenByHash(ctx context.Context, hash string) (apitoken.Token, bool, error) {
	row, err := s.reader().GetAPITokenByHash(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return apitoken.Token{}, false, nil
	}
	if err != nil {
		return apitoken.Token{}, false, fmt.Errorf("api token by hash: %w", translate(err))
	}
	return toAPIToken(row), true, nil
}

// APITokenByName returns the API token with the name; found is false if
// there is none.
func (s *Store) APITokenByName(ctx context.Context, name string) (apitoken.Token, bool, error) {
	row, err := s.reader().GetAPITokenByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return apitoken.Token{}, false, nil
	}
	if err != nil {
		return apitoken.Token{}, false, fmt.Errorf("api token %q: %w", name, translate(err))
	}
	return toAPIToken(row), true, nil
}

// CountAPITokensByName counts the API tokens with the name.
func (s *Store) CountAPITokensByName(ctx context.Context, name string) (int64, error) {
	n, err := s.reader().CountAPITokensByName(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("count api tokens %q: %w", name, translate(err))
	}
	return n, nil
}

// ListAPITokens returns the API tokens, oldest first.
func (s *Store) ListAPITokens(ctx context.Context) ([]apitoken.Token, error) {
	rows, err := s.reader().ListAPITokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", translate(err))
	}
	out := make([]apitoken.Token, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAPIToken(r))
	}
	return out, nil
}

// CreateAPIToken inserts the token; a taken name fails with
// apitoken.ErrNameTaken. The check and the insert run in one transaction,
// so two concurrent creates of the same name cannot both pass.
func (s *Store) CreateAPIToken(ctx context.Context, tok apitoken.Token) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		n, err := q.CountAPITokensByName(ctx, tok.Name)
		if err != nil {
			return translate(err)
		}
		if n > 0 {
			return fmt.Errorf("%w: %q", apitoken.ErrNameTaken, tok.Name)
		}
		if err := q.InsertAPIToken(ctx, fromAPIToken(tok)); err != nil {
			return fmt.Errorf("insert api token %q: %w", tok.Name, translate(err))
		}
		return nil
	})
}

// DeleteAPIToken removes the API token with the ID; found reports whether
// it existed.
func (s *Store) DeleteAPIToken(ctx context.Context, id string) (bool, error) {
	var n int64
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		var err error
		n, err = q.DeleteAPIToken(ctx, id)
		return translate(err)
	})
	return n > 0, err
}

func toAPIToken(r sqlcgen.ApiToken) apitoken.Token {
	fields := strings.Fields(r.Scopes)
	scopes := make([]apitoken.Scope, len(fields))
	for i, f := range fields {
		scopes[i] = apitoken.Scope(f)
	}
	return apitoken.Token{
		ID:        r.ID,
		Name:      r.Name,
		Scopes:    scopes,
		CreatedAt: time.UnixMilli(r.CreatedAt).UTC(),
		SHA256:    r.Sha256,
	}
}

func fromAPIToken(t apitoken.Token) sqlcgen.InsertAPITokenParams {
	fields := make([]string, len(t.Scopes))
	for i, s := range t.Scopes {
		fields[i] = string(s)
	}
	return sqlcgen.InsertAPITokenParams{
		ID:        t.ID,
		Name:      t.Name,
		Scopes:    strings.Join(fields, " "),
		CreatedAt: t.CreatedAt.UnixMilli(),
		Sha256:    t.SHA256,
	}
}
