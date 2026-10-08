-- SPDX-License-Identifier: MIT
--
-- Cooldown groups (docs/spec/commands.md, B33): named cooldowns that
-- commands share through the grouped scopes of their cooldown requirement,
-- with one duration each. They are independent of the command groups.
-- Times and durations in milliseconds (Code-ADR-0009).

-- +goose Up
CREATE TABLE cooldown_groups (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    name_key   TEXT NOT NULL UNIQUE, -- lowercase name: unique regardless of case (B33)
    duration   INTEGER NOT NULL,     -- milliseconds, positive
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE cooldown_groups;
