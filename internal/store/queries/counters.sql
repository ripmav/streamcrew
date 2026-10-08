-- name: GetCounter :one
SELECT * FROM counters WHERE name_key = ?;

-- name: ListCounters :many
SELECT * FROM counters ORDER BY name;

-- name: InsertCounter :exec
INSERT INTO counters (id, name, name_key, value, step, reset_on_start, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateCounter :exec
UPDATE counters SET name = ?, name_key = ?, value = ?, step = ?, reset_on_start = ?, updated_at = ? WHERE id = ?;

-- name: DeleteCounter :execrows
DELETE FROM counters WHERE name_key = ?;

-- name: ResetCountersOnStart :execrows
UPDATE counters SET value = '0', updated_at = ? WHERE reset_on_start = 1;

-- SPDX-License-Identifier: MIT
