-- SPDX-License-Identifier: MIT
--
-- Base tables of a profile database (ADR-0012, Code-ADR-0008): profile
-- metadata, typed settings sections and encrypted secrets.

-- +goose Up
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

-- One polymorphic document (Code-ADR-0010) per settings section.
CREATE TABLE settings (
    section    TEXT PRIMARY KEY,
    document   TEXT NOT NULL,
    updated_at INTEGER NOT NULL -- Unix milliseconds, UTC (Code-ADR-0009)
) STRICT;

-- AES-256-GCM ciphertexts; the plaintext never reaches the database
-- (ADR-0012). key_id names the key that encrypted the value.
CREATE TABLE secrets (
    name       TEXT PRIMARY KEY,
    key_id     TEXT NOT NULL,
    nonce      BLOB NOT NULL,
    ciphertext BLOB NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE secrets;
DROP TABLE settings;
DROP TABLE meta;
