-- name: GetCommand :one
SELECT * FROM commands WHERE id = ?;

-- name: ListCommands :many
SELECT * FROM commands ORDER BY name, id;

-- name: PutCommand :exec
INSERT INTO commands (
    id, name, name_key, kind, enabled, unlocked, group_id, trigger_mode, event_type,
    error_policy, requirements, actions, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    name = excluded.name,
    name_key = excluded.name_key,
    kind = excluded.kind,
    enabled = excluded.enabled,
    unlocked = excluded.unlocked,
    group_id = excluded.group_id,
    trigger_mode = excluded.trigger_mode,
    event_type = excluded.event_type,
    error_policy = excluded.error_policy,
    requirements = excluded.requirements,
    actions = excluded.actions,
    updated_at = excluded.updated_at;

-- name: DeleteCommand :execrows
DELETE FROM commands WHERE id = ?;

-- name: SetCommandEnabled :exec
UPDATE commands SET enabled = ?, updated_at = ? WHERE id = ?;

-- name: ListTriggers :many
SELECT trigger_text FROM command_triggers WHERE command_id = ? ORDER BY position;

-- name: ListAllTriggers :many
SELECT command_id, trigger_text FROM command_triggers ORDER BY command_id, position;

-- name: DeleteTriggers :exec
DELETE FROM command_triggers WHERE command_id = ?;

-- name: InsertTrigger :exec
INSERT INTO command_triggers (command_id, position, trigger_text, trigger_key, wildcard, active)
VALUES (?, ?, ?, ?, ?, ?);

-- name: SetTriggersActive :exec
UPDATE command_triggers SET active = ? WHERE command_id = ?;

-- name: GetCommandGroup :one
SELECT * FROM command_groups WHERE id = ?;

-- name: ListCommandGroups :many
SELECT * FROM command_groups ORDER BY name_key, id;

-- name: PutCommandGroup :exec
INSERT INTO command_groups (id, name, name_key, timer_interval, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    name = excluded.name,
    name_key = excluded.name_key,
    timer_interval = excluded.timer_interval,
    updated_at = excluded.updated_at;

-- name: DeleteCommandGroup :execrows
DELETE FROM command_groups WHERE id = ?;

-- SPDX-License-Identifier: Apache-2.0

-- name: GetCooldownGroup :one
SELECT * FROM cooldown_groups WHERE id = ?;

-- name: ListCooldownGroups :many
SELECT * FROM cooldown_groups ORDER BY name_key, id;

-- name: PutCooldownGroup :exec
INSERT INTO cooldown_groups (id, name, name_key, duration, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    name = excluded.name,
    name_key = excluded.name_key,
    duration = excluded.duration,
    updated_at = excluded.updated_at;

-- name: DeleteCooldownGroup :execrows
DELETE FROM cooldown_groups WHERE id = ?;
