-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- ListUserIDs lists the IDs of the users that have an identity whose login
-- starts with the prefix (case-insensitive, empty prefix is all users),
-- ordered by ID, limited (roadmap 6.2.1, API).
-- name: ListUserIDs :many
SELECT u.id
FROM users u
WHERE EXISTS (
    SELECT 1 FROM user_identities i
    WHERE i.user_id = u.id AND lower(i.login) LIKE lower(?) || '%'
)
ORDER BY u.id
LIMIT ?;

-- name: ListUsersRegular :many
SELECT u.id AS id, u.regular AS regular, s.watch_minutes AS watch_minutes
FROM users u
JOIN user_stats s ON s.user_id = u.id
ORDER BY u.id;

-- One top list per statistic (roadmap 5.2.5): excluded users are never
-- ranked (users-and-roles.md, B7), neither are users whose value is 0; ties
-- are ordered by user ID.
-- name: TopUsersWatchMinutes :many
SELECT u.id AS id, s.watch_minutes AS val
FROM user_stats s
JOIN users u ON u.id = s.user_id
WHERE u.excluded = 0 AND s.watch_minutes > 0
ORDER BY s.watch_minutes DESC, u.id
LIMIT ?;

-- name: TopUsersMessages :many
SELECT u.id AS id, s.messages AS val
FROM user_stats s
JOIN users u ON u.id = s.user_id
WHERE u.excluded = 0 AND s.messages > 0
ORDER BY s.messages DESC, u.id
LIMIT ?;

-- name: TopUsersCommandsRun :many
SELECT u.id AS id, s.commands_run AS val
FROM user_stats s
JOIN users u ON u.id = s.user_id
WHERE u.excluded = 0 AND s.commands_run > 0
ORDER BY s.commands_run DESC, u.id
LIMIT ?;

-- name: TopUsersMentions :many
SELECT u.id AS id, s.mentions AS val
FROM user_stats s
JOIN users u ON u.id = s.user_id
WHERE u.excluded = 0 AND s.mentions > 0
ORDER BY s.mentions DESC, u.id
LIMIT ?;

-- name: TopUsersStreamsWatched :many
SELECT u.id AS id, s.streams_watched AS val
FROM user_stats s
JOIN users u ON u.id = s.user_id
WHERE u.excluded = 0 AND s.streams_watched > 0
ORDER BY s.streams_watched DESC, u.id
LIMIT ?;

-- name: InsertUser :exec
INSERT INTO users (id, title, notes, excluded, regular, entrance_command_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateUser :exec
UPDATE users SET
    title = ?,
    notes = ?,
    excluded = ?,
    regular = ?,
    entrance_command_id = ?,
    updated_at = ?
WHERE id = ?;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = ?;

-- name: GetUserStats :one
SELECT * FROM user_stats WHERE user_id = ?;

-- name: PutUserStats :exec
INSERT INTO user_stats (
    user_id, watch_minutes, messages, commands_run, mentions, streams_watched,
    first_seen, last_seen, donated_cents, strikes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET
    watch_minutes = excluded.watch_minutes,
    messages = excluded.messages,
    commands_run = excluded.commands_run,
    mentions = excluded.mentions,
    streams_watched = excluded.streams_watched,
    first_seen = excluded.first_seen,
    last_seen = excluded.last_seen,
    donated_cents = excluded.donated_cents,
    strikes = excluded.strikes;

-- name: GetIdentity :one
SELECT * FROM user_identities WHERE platform = ? AND platform_user_id = ?;

-- name: ListIdentities :many
SELECT * FROM user_identities WHERE user_id = ? ORDER BY created_at, platform, platform_user_id;

-- name: InsertIdentity :exec
INSERT INTO user_identities (
    platform, platform_user_id, user_id, login, display_name, color, avatar_url, roles,
    followed_at, subscribed_at, sub_tier, account_created_at, data_updated_at,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateIdentityNames :exec
UPDATE user_identities SET
    login = ?,
    display_name = ?,
    color = ?,
    avatar_url = ?,
    updated_at = ?
WHERE platform = ? AND platform_user_id = ?;

-- name: UpdateIdentityRoles :execrows
UPDATE user_identities SET roles = ?, updated_at = ?
WHERE platform = ? AND platform_user_id = ?;

-- name: UpdateIdentityData :execrows
UPDATE user_identities SET
    followed_at = ?,
    subscribed_at = ?,
    sub_tier = ?,
    account_created_at = ?,
    data_updated_at = ?,
    updated_at = ?
WHERE platform = ? AND platform_user_id = ?;

-- SPDX-License-Identifier: MIT

-- name: GetIdentityByLogin :one
SELECT * FROM user_identities WHERE platform = ? AND login = ? COLLATE NOCASE
ORDER BY updated_at DESC, platform_user_id LIMIT 1;

-- name: ResetStrikes :exec
UPDATE user_stats SET strikes = 0 WHERE strikes <> 0;

-- name: AddUserStats :execrows
UPDATE user_stats SET
    watch_minutes   = watch_minutes + ?,
    messages        = messages + ?,
    commands_run    = commands_run + ?,
    mentions        = mentions + ?,
    streams_watched = streams_watched + ?,
    first_seen      = COALESCE(first_seen, ?),
    last_seen       = COALESCE(?, last_seen)
WHERE user_id = ?;

-- Roadmap 5.2.4: move every platform identity of one user to another
-- (linking the accounts; the dropped user is deleted after its statistics
-- are folded in). users-and-roles.md, B3/B40.
-- name: MoveIdentitiesToUser :execrows
UPDATE user_identities
SET user_id = ?, updated_at = ?
WHERE user_id = ?;
