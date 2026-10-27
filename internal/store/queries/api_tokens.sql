-- SPDX-License-Identifier: MIT

-- name: GetAPIToken :one
SELECT * FROM api_tokens WHERE id = ?;

-- name: GetAPITokenByHash :one
SELECT * FROM api_tokens WHERE sha256 = ?;

-- name: GetAPITokenByName :one
SELECT * FROM api_tokens WHERE name = ?;

-- name: CountAPITokensByName :one
SELECT count(*) FROM api_tokens WHERE name = ?;

-- name: ListAPITokens :many
SELECT * FROM api_tokens ORDER BY created_at, id;

-- name: InsertAPIToken :exec
INSERT INTO api_tokens (id, name, scopes, created_at, sha256)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteAPIToken :execrows
DELETE FROM api_tokens WHERE id = ?;
