-- SPDX-License-Identifier: MIT
--
-- Platform accounts (ADR-0014, ADR-0023, roadmap 4.1): the metadata of the
-- logins of a platform, one streamer account and optionally one bot account.
-- The tokens themselves are encrypted in the vault (names
-- "auth/<platform>/<role>"); this table keeps what a login is, which scopes
-- it was granted and how the login was made (flow). Times in milliseconds
-- (Code-ADR-0009).

-- +goose Up
CREATE TABLE accounts (
    platform   TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('streamer', 'bot')),
    login      TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    scopes     TEXT NOT NULL, -- granted scopes, space separated
    client_id  TEXT NOT NULL,
    flow       TEXT NOT NULL CHECK (flow IN ('authorization_code', 'device_code')), -- how the login was made (ADR-0023)
    updated_at INTEGER NOT NULL, -- Unix milliseconds, UTC (Code-ADR-0009)
    PRIMARY KEY (platform, role)
) STRICT;

-- +goose Down
DROP TABLE accounts;
