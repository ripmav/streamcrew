-- SPDX-License-Identifier: MIT
--
-- Counters hold exact decimals (docs/spec/counters-and-quotes.md, B5, B8;
-- Code-ADR-0020): value and step become TEXT in the canonical form, e.g.
-- "2.5". STRICT tables cannot change the type of a column, so the table is
-- rebuilt with its columns in the same order; whole numbers keep their
-- digits.

-- +goose Up
CREATE TABLE counters_decimal (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE COLLATE NOCASE,
    value          TEXT NOT NULL, -- canonical decimal
    reset_on_start INTEGER NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    step           TEXT NOT NULL  -- canonical decimal, greater than 0
) STRICT;
INSERT INTO counters_decimal (id, name, value, reset_on_start, created_at, updated_at, step)
    SELECT id, name, CAST(value AS TEXT), reset_on_start, created_at, updated_at, CAST(step AS TEXT) FROM counters;
DROP TABLE counters;
ALTER TABLE counters_decimal RENAME TO counters;

-- +goose Down
-- Decimal places are cut off; a step below 1 becomes 1.
CREATE TABLE counters_integer (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE COLLATE NOCASE,
    value          INTEGER NOT NULL,
    reset_on_start INTEGER NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    step           INTEGER NOT NULL DEFAULT 1
) STRICT;
INSERT INTO counters_integer (id, name, value, reset_on_start, created_at, updated_at, step)
    SELECT id, name, CAST(value AS INTEGER), reset_on_start, created_at, updated_at, max(CAST(step AS INTEGER), 1) FROM counters;
DROP TABLE counters;
ALTER TABLE counters_integer RENAME TO counters;
