-- SPDX-License-Identifier: MIT
--
-- The step of a counter (docs/spec/counters-and-quotes.md, B8): what
-- increasing and decreasing by one step change the value by. Existing
-- counters get the default step 1.

-- +goose Up
ALTER TABLE counters ADD COLUMN step INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE counters DROP COLUMN step;
