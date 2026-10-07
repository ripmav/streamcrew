-- SPDX-License-Identifier: Apache-2.0
-- name: GetAccount :one
SELECT platform, role, login, user_id, scopes, client_id, updated_at FROM accounts WHERE platform = ? AND role = ?;

-- name: UpsertAccount :exec
INSERT INTO accounts (platform, role, login, user_id, scopes, client_id, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, role) DO UPDATE SET
    login = excluded.login,
    user_id = excluded.user_id,
    scopes = excluded.scopes,
    client_id = excluded.client_id,
    updated_at = excluded.updated_at;

-- name: ListAccounts :many
SELECT platform, role, login, user_id, scopes, client_id, updated_at FROM accounts ORDER BY platform, role;

-- name: DeleteAccount :execrows
DELETE FROM accounts WHERE platform = ? AND role = ?;
