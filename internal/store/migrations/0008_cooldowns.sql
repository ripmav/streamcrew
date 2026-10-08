-- SPDX-License-Identifier: MIT
--
-- Running cooldowns (docs/spec/requirements.md, B20 to B23): stored, so
-- that they outlast a restart, with the end each one had when it started.
-- A cooldown blocks a command (scopes standard and per_user) or a cooldown
-- group (group and per_user_group), for the scopes per user only for one
-- user. Times in milliseconds (Code-ADR-0009).

-- +goose Up
CREATE TABLE cooldowns (
    command_id TEXT REFERENCES commands (id) ON DELETE CASCADE,
    group_id   TEXT REFERENCES cooldown_groups (id) ON DELETE CASCADE,
    user_id    TEXT REFERENCES users (id) ON DELETE CASCADE, -- NULL for the scopes for everyone
    ends_at    INTEGER NOT NULL,
    CHECK ((command_id IS NULL) <> (group_id IS NULL))
) STRICT;

-- One cooldown per key; NULL counts as a value of its own here.
CREATE UNIQUE INDEX cooldowns_key ON cooldowns (ifnull(command_id, ''), ifnull(group_id, ''), ifnull(user_id, ''));
CREATE INDEX cooldowns_ends_at ON cooldowns (ends_at);

-- +goose Down
DROP TABLE cooldowns;
