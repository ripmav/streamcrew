-- name: GetSecret :one
SELECT name, key_id, nonce, ciphertext, updated_at FROM secrets WHERE name = ?;

-- name: PutSecret :exec
INSERT INTO secrets (name, key_id, nonce, ciphertext, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
    key_id = excluded.key_id,
    nonce = excluded.nonce,
    ciphertext = excluded.ciphertext,
    updated_at = excluded.updated_at;

-- name: DeleteSecret :execrows
DELETE FROM secrets WHERE name = ?;

-- name: ListSecrets :many
SELECT name, key_id, nonce, ciphertext, updated_at FROM secrets ORDER BY name;

-- SPDX-License-Identifier: Apache-2.0
