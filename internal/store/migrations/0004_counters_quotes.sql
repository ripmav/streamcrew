-- SPDX-License-Identifier: Apache-2.0
--
-- Counters and quotes (docs/spec/counters-and-quotes.md). IDs are UUIDv7
-- text, times Unix milliseconds in UTC (Code-ADR-0009), flags 0/1.

-- +goose Up
CREATE TABLE counters (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE COLLATE NOCASE, -- ASCII only (B7), so NOCASE suffices (B1)
    value          INTEGER NOT NULL,
    reset_on_start INTEGER NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
) STRICT;

-- AUTOINCREMENT: SQLite never gives out a number again, even after the
-- quote with the highest number was deleted (B41).
CREATE TABLE quotes (
    number     INTEGER PRIMARY KEY AUTOINCREMENT,
    id         TEXT NOT NULL UNIQUE,
    text       TEXT NOT NULL,
    game       TEXT NOT NULL,
    quoted_at  INTEGER NOT NULL,
    added_by   TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX quotes_added_by ON quotes (added_by);

-- +goose Down
DROP TABLE quotes;
DROP TABLE counters;
