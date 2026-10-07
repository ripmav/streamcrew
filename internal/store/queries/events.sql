-- SPDX-License-Identifier: Apache-2.0

-- name: ListStreamSessions :many
SELECT platform, started_at, state, since, seen_live FROM stream_sessions ORDER BY platform;

-- name: PutStreamSession :exec
INSERT INTO stream_sessions (platform, started_at, state, since, seen_live) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (platform) DO UPDATE SET
    started_at = excluded.started_at,
    state = excluded.state,
    since = excluded.since,
    seen_live = excluded.seen_live;

-- name: DeleteSessionEvents :exec
DELETE FROM session_events WHERE platform = ?;

-- name: InsertSessionEvent :execrows
INSERT INTO session_events (platform, type, user_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING;

-- name: InsertUserEvent :execrows
INSERT INTO user_events (user_id, type) VALUES (?, ?) ON CONFLICT DO NOTHING;
