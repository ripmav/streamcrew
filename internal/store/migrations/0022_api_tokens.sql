-- SPDX-License-Identifier: MIT
--
-- The API access tokens of the profile (ADR-0013 §5, roadmap 6.2.2): the
-- tokens are random 256-bit values that are stored only as their SHA-256
-- hash, with scopes. They belong to the profile — one core serves the
-- active profile, and the CLI manages them without a running core — so
-- they live in the profile database, not in a file of the data directory.
-- Times in milliseconds (Code-ADR-0009), scopes space separated.

-- +goose Up
CREATE TABLE api_tokens (
    id         TEXT NOT NULL,
    name       TEXT NOT NULL,
    scopes     TEXT NOT NULL, -- granted scopes, space separated
    created_at INTEGER NOT NULL,
    sha256     TEXT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (name),
    UNIQUE (sha256)
) STRICT;

-- +goose Down
DROP TABLE api_tokens;
