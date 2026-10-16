-- SPDX-License-Identifier: MIT
--
-- The per-user answers of user-specific chat commands (roadmap 5.6, P1):
-- each user stores one answer per key, and a command answers a user with
-- their own stored answer instead of a shared one. Times in milliseconds
-- (Code-ADR-0009).

-- +goose Up
CREATE TABLE user_answer (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    answer     TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, key)
) STRICT;

-- +goose Down
DROP TABLE user_answer;
