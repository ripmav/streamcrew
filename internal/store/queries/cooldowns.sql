-- name: GetCooldownEnd :one
SELECT ends_at FROM cooldowns
WHERE command_id IS sqlc.narg(command_id) AND group_id IS sqlc.narg(group_id) AND user_id IS sqlc.narg(user_id);

-- name: DeleteCooldown :exec
DELETE FROM cooldowns
WHERE command_id IS sqlc.narg(command_id) AND group_id IS sqlc.narg(group_id) AND user_id IS sqlc.narg(user_id);

-- name: DeleteCooldownEndingAt :exec
DELETE FROM cooldowns
WHERE command_id IS sqlc.narg(command_id) AND group_id IS sqlc.narg(group_id) AND user_id IS sqlc.narg(user_id)
    AND ends_at = sqlc.arg(ends_at);

-- name: DeleteEndedCooldowns :exec
DELETE FROM cooldowns WHERE ends_at <= ?;

-- name: InsertCooldown :exec
INSERT INTO cooldowns (command_id, group_id, user_id, ends_at) VALUES (?, ?, ?, ?);

-- SPDX-License-Identifier: MIT
