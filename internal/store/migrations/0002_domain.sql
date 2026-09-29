-- SPDX-License-Identifier: Apache-2.0
--
-- Domain model of roadmap phase 2.2: commands and their groups, users with
-- platform identities and statistics, counters and quotes (specs in
-- docs/spec). IDs are UUIDv7 text, times Unix milliseconds in UTC
-- (Code-ADR-0009), flags 0/1.

-- +goose Up
CREATE TABLE command_groups (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    name_key       TEXT NOT NULL UNIQUE, -- lowercase name: unique regardless of case (B30)
    timer_interval INTEGER NOT NULL,     -- milliseconds; 0 for no own interval (B31)
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
) STRICT;

-- Requirements and actions are JSON arrays of polymorphic documents
-- (Code-ADR-0010).
CREATE TABLE commands (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL,
    enabled      INTEGER NOT NULL,
    unlocked     INTEGER NOT NULL,
    group_id     TEXT REFERENCES command_groups (id) ON DELETE SET NULL, -- B62
    wildcard     INTEGER NOT NULL,
    event_type   TEXT UNIQUE, -- event commands only; at most one per type (B20)
    requirements TEXT NOT NULL,
    actions      TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX commands_group_id ON commands (group_id);

-- The triggers of chat commands. active mirrors commands.enabled, so that a
-- trigger is unique among enabled chat commands only (B14).
CREATE TABLE command_triggers (
    command_id   TEXT NOT NULL REFERENCES commands (id) ON DELETE CASCADE,
    position     INTEGER NOT NULL,
    trigger_text TEXT NOT NULL, -- as entered, without "!"
    trigger_key  TEXT NOT NULL, -- lowercase
    active       INTEGER NOT NULL,
    PRIMARY KEY (command_id, position)
) STRICT;

CREATE UNIQUE INDEX command_triggers_active_key ON command_triggers (trigger_key) WHERE active = 1;

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
DROP TABLE user_identities;
DROP TABLE user_stats;
DROP TABLE users;
DROP TABLE command_triggers;
DROP TABLE commands;
DROP TABLE command_groups;
