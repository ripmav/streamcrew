-- SPDX-License-Identifier: MIT
--
-- The per-user answers of user-specific chat commands (roadmap 5.6, P1).

-- name: SetUserAnswer :exec
INSERT INTO user_answer (user_id, key, answer, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, key) DO UPDATE SET answer = excluded.answer, updated_at = excluded.updated_at;

-- name: GetUserAnswer :one
SELECT answer FROM user_answer WHERE user_id = ? AND key = ?;

-- name: DeleteUserAnswer :execrows
DELETE FROM user_answer WHERE user_id = ? AND key = ?;
