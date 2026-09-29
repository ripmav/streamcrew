-- SPDX-License-Identifier: Apache-2.0
--
-- Users with platform identities and statistics
-- (docs/spec/users-and-roles.md). IDs are UUIDv7 text, times Unix
-- milliseconds in UTC (Code-ADR-0009), flags 0/1.

-- +goose Up
CREATE TABLE users (
    id                  TEXT PRIMARY KEY,
    title               TEXT NOT NULL,
    notes               TEXT NOT NULL,
    excluded            INTEGER NOT NULL,
    regular             INTEGER NOT NULL,
    entrance_command_id TEXT REFERENCES commands (id) ON DELETE SET NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
) STRICT;

CREATE INDEX users_entrance_command_id ON users (entrance_command_id);

-- One row per user; a table of its own because the statistics change far
-- more often than the rest of the user.
CREATE TABLE user_stats (
    user_id         TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    watch_minutes   INTEGER NOT NULL,
    messages        INTEGER NOT NULL,
    commands_run    INTEGER NOT NULL,
    mentions        INTEGER NOT NULL,
    streams_watched INTEGER NOT NULL,
    first_seen      INTEGER, -- NULL for never
    last_seen       INTEGER,
    donated_cents   INTEGER NOT NULL,
    strikes         INTEGER NOT NULL
) STRICT;

-- A platform account belongs to exactly one user (B3). roles is a JSON array
-- of role IDs; the other columns after it cache platform data (B10).
CREATE TABLE user_identities (
    platform           TEXT NOT NULL,
    platform_user_id   TEXT NOT NULL,
    user_id            TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    login              TEXT NOT NULL,
    display_name       TEXT NOT NULL,
    color              TEXT NOT NULL,
    avatar_url         TEXT NOT NULL,
    roles              TEXT NOT NULL,
    followed_at        INTEGER,
    subscribed_at      INTEGER,
    sub_tier           INTEGER NOT NULL,
    account_created_at INTEGER,
    data_updated_at    INTEGER,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    PRIMARY KEY (platform, platform_user_id)
) STRICT;

CREATE INDEX user_identities_user_id ON user_identities (user_id);
CREATE INDEX user_identities_login ON user_identities (platform, login);

-- +goose Down
DROP TABLE user_identities;
DROP TABLE user_stats;
DROP TABLE users;
