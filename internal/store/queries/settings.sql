-- name: GetSettings :one
SELECT document, updated_at FROM settings WHERE section = ?;

-- name: PutSettings :exec
INSERT INTO settings (section, document, updated_at) VALUES (?, ?, ?)
ON CONFLICT (section) DO UPDATE SET document = excluded.document, updated_at = excluded.updated_at;

-- name: ListSettings :many
SELECT section, document, updated_at FROM settings ORDER BY section;

-- SPDX-License-Identifier: Apache-2.0
