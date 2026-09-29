// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
	"github.com/ripmav/streamcrew/internal/vault"
)

// GetSecret implements vault.Repository.
func (s *Store) GetSecret(ctx context.Context, name string) (vault.Record, bool, error) {
	row, err := s.reader().GetSecret(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return vault.Record{}, false, nil
	}
	if err != nil {
		return vault.Record{}, false, fmt.Errorf("get vault entry: %w", translate(err))
	}
	return toRecord(row), true, nil
}

// PutSecret implements vault.Repository.
func (s *Store) PutSecret(ctx context.Context, rec vault.Record) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		return q.PutSecret(ctx, fromRecord(rec))
	})
}

// DeleteSecret implements vault.Repository.
func (s *Store) DeleteSecret(ctx context.Context, name string) (bool, error) {
	var n int64
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		var err error
		n, err = q.DeleteSecret(ctx, name)
		return err
	})
	return n > 0, err
}

// ListSecrets implements vault.Repository.
func (s *Store) ListSecrets(ctx context.Context) ([]vault.Record, error) {
	rows, err := s.reader().ListSecrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list vault entries: %w", translate(err))
	}
	out := make([]vault.Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, toRecord(r))
	}
	return out, nil
}

// RewriteSecrets implements vault.Repository. Reading and writing happen in
// one transaction on the writer pool, which holds the write lock from its
// start, so other writes wait until it ends.
func (s *Store) RewriteSecrets(ctx context.Context, fn func([]vault.Record) ([]vault.Record, error)) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListSecrets(ctx)
		if err != nil {
			return err
		}
		recs := make([]vault.Record, 0, len(rows))
		for _, r := range rows {
			recs = append(recs, toRecord(r))
		}
		out, err := fn(recs)
		if err != nil {
			return err
		}
		for _, rec := range out {
			if err := q.PutSecret(ctx, fromRecord(rec)); err != nil {
				return err
			}
		}
		return nil
	})
}

func toRecord(r sqlcgen.Secret) vault.Record {
	return vault.Record{
		Name:       r.Name,
		KeyID:      r.KeyID,
		Nonce:      r.Nonce,
		Ciphertext: r.Ciphertext,
		UpdatedAt:  time.UnixMilli(r.UpdatedAt).UTC(),
	}
}

func fromRecord(r vault.Record) sqlcgen.PutSecretParams {
	return sqlcgen.PutSecretParams{
		Name:       r.Name,
		KeyID:      r.KeyID,
		Nonce:      r.Nonce,
		Ciphertext: r.Ciphertext,
		UpdatedAt:  r.UpdatedAt.UnixMilli(),
	}
}
