-- SPDX-License-Identifier: MIT
--
-- The target channel of a command group's timer interval
-- (docs/spec/commands.md, B31): the channel the interval fires in. Empty
-- for the home channel; existing groups fire at home, as before.

-- +goose Up
ALTER TABLE command_groups ADD COLUMN timer_channel TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE command_groups DROP COLUMN timer_channel;
