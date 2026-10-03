-- SPDX-License-Identifier: MIT
--
-- The name of a command is unique regardless of case
-- (docs/spec/commands.md, B7; docs/spec/commands-as-code.md, B22). The
-- column holds the name in lower case, as command.NameKey computes it.
-- Command names are free text, so SQLite's lower(), which knows ASCII
-- only, cannot fill it: Go migration 14 (commandNameKeys in
-- migrations_go.go) fills it, renames duplicates and adds the unique
-- index.

-- +goose Up
ALTER TABLE commands ADD COLUMN name_key TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE commands DROP COLUMN name_key;
