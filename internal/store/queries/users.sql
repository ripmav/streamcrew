-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

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
