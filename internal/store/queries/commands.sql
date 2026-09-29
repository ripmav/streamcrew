-- name: GetCommand :one
SELECT * FROM commands WHERE id = ?;

-- name: ListCommands :many
SELECT * FROM commands ORDER BY name, id;

-- name: PutCommand :exec
INSERT INTO commands (
    id, name, kind, enabled, unlocked, group_id, wildcard, event_type,
    requirements, actions, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    name = excluded.name,
    kind = excluded.kind,
    enabled = excluded.enabled,
    unlocked = excluded.unlocked,
    group_id = excluded.group_id,
    wildcard = excluded.wildcard,
    event_type = excluded.event_type,
    requirements = excluded.requirements,
    actions = excluded.actions,
    updated_at = excluded.updated_at;

-- name: DeleteCommand :execrows
DELETE FROM commands WHERE id = ?;

-- name: ListTriggers :many
SELECT trigger_text FROM command_triggers WHERE command_id = ? ORDER BY position;

-- name: ListAllTriggers :many
SELECT command_id, trigger_text FROM command_triggers ORDER BY command_id, position;

-- name: DeleteTriggers :exec
DELETE FROM command_triggers WHERE command_id = ?;

-- name: InsertTrigger :exec
INSERT INTO command_triggers (command_id, position, trigger_text, trigger_key, active)
VALUES (?, ?, ?, ?, ?);

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
