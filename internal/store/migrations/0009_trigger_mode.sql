-- SPDX-License-Identifier: MIT
--
-- The trigger mode of chat commands (docs/spec/commands.md, B11, B13, B14):
-- exclamation, literal or wildcard, instead of the flag wildcard. Existing
-- chat commands keep their behavior: wildcard or exclamation.
--
-- Triggers are unique in their exact spelling as a user writes them, with
-- "!" for the mode exclamation; wildcard triggers are unique among
-- themselves regardless of case. command_triggers.wildcard separates the
-- two.

-- +goose Up
ALTER TABLE commands ADD COLUMN trigger_mode TEXT; -- chat commands only
UPDATE commands SET trigger_mode = CASE WHEN wildcard = 1 THEN 'wildcard' ELSE 'exclamation' END
    WHERE kind = 'chat';
ALTER TABLE commands DROP COLUMN wildcard;

ALTER TABLE command_triggers ADD COLUMN wildcard INTEGER NOT NULL DEFAULT 0;
UPDATE command_triggers SET wildcard = 1
    WHERE command_id IN (SELECT id FROM commands WHERE trigger_mode = 'wildcard');
UPDATE command_triggers SET trigger_key = '!' || trigger_text WHERE wildcard = 0;
DROP INDEX command_triggers_active_key;
CREATE UNIQUE INDEX command_triggers_active_key ON command_triggers (wildcard, trigger_key) WHERE active = 1;

-- +goose Down
-- Literal triggers become triggers with "!". It fails if two enabled chat
-- commands have triggers that differ in case only.
DROP INDEX command_triggers_active_key;
UPDATE command_triggers SET trigger_key = lower(trigger_text) WHERE wildcard = 0;
ALTER TABLE command_triggers DROP COLUMN wildcard;
CREATE UNIQUE INDEX command_triggers_active_key ON command_triggers (trigger_key) WHERE active = 1;

ALTER TABLE commands ADD COLUMN wildcard INTEGER NOT NULL DEFAULT 0;
UPDATE commands SET wildcard = 1 WHERE trigger_mode = 'wildcard';
ALTER TABLE commands DROP COLUMN trigger_mode;
