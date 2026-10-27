-- SPDX-License-Identifier: MIT
--
-- The user-command answers are keyed by the ID of the command, not by its
-- name (docs/spec/actions.md, B127): a rename, even a case-only one, must
-- not orphan the stored answers. The existing answers keep the command they
-- were stored under; answers of commands that no longer exist are left
-- alone, they are dead either way.

-- +goose Up
UPDATE user_answer
SET key = (
    SELECT id FROM commands
    WHERE name_key = lower(user_answer.key)
    LIMIT 1
)
WHERE EXISTS (
    SELECT 1 FROM commands
    WHERE name_key = lower(user_answer.key)
);

-- +goose Down
UPDATE user_answer
SET key = (
    SELECT name FROM commands
    WHERE id = user_answer.key
    LIMIT 1
)
WHERE EXISTS (
    SELECT 1 FROM commands
    WHERE id = user_answer.key
);
