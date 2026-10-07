-- SPDX-License-Identifier: MIT
--
-- Stream sessions and the events that fire once (docs/spec/events.md, B3,
-- B8, B11, B21): stored, so that a restart during a stream neither starts
-- a new session nor repeats an alert. Times in milliseconds
-- (Code-ADR-0009).

-- +goose Up
-- The stream session of each platform (B11).
CREATE TABLE stream_sessions (
    platform   TEXT PRIMARY KEY,
    started_at INTEGER, -- NULL for the session before the first stream start
    state      TEXT NOT NULL CHECK (state IN ('live', 'grace', 'offline')),
    since      INTEGER, -- NULL for the session before the first stream start
    seen_live  INTEGER  -- NULL if the core never saw the stream live
) STRICT;

-- Events that fire once per stream session and user (B3, B11), under the
-- platform-neutral type where there is one. A new session forgets them.
CREATE TABLE session_events (
    platform TEXT NOT NULL,
    type     TEXT NOT NULL,
    user_id  TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (platform, type, user_id)
) STRICT;
CREATE INDEX session_events_user ON session_events (user_id);

-- Events that fire once per user, ever: chat.user.new and
-- chat.user.first_message (B11).
CREATE TABLE user_events (
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type    TEXT NOT NULL,
    PRIMARY KEY (user_id, type)
) STRICT;

-- +goose Down
DROP TABLE user_events;
DROP TABLE session_events;
DROP TABLE stream_sessions;
