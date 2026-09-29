-- SPDX-License-Identifier: MIT
--
-- Commands and their groups (docs/spec/commands.md). IDs are UUIDv7 text,
-- times Unix milliseconds in UTC (Code-ADR-0009), flags 0/1.

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

-- +goose Down
DROP TABLE command_triggers;
DROP TABLE commands;
DROP TABLE command_groups;
