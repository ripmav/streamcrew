-- SPDX-License-Identifier: Apache-2.0
--
-- The error policy of a command (docs/spec/command-engine.md, B71): what
-- happens after one of its actions fails. Existing commands keep running
-- their next actions, the default.

-- +goose Up
ALTER TABLE commands ADD COLUMN error_policy TEXT NOT NULL DEFAULT 'continue';

-- +goose Down
ALTER TABLE commands DROP COLUMN error_policy;
