-- SPDX-License-Identifier: MIT
--
-- The channel scope of a command (docs/spec/commands.md, B9): the channel
-- the command runs in. Empty for the global scope, all operated channels;
-- existing commands are global, as before.

-- +goose Up
ALTER TABLE commands ADD COLUMN channel TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE commands DROP COLUMN channel;
