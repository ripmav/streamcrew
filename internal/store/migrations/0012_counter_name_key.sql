-- SPDX-License-Identifier: Apache-2.0
--
-- Counter names with Unicode letters (docs/spec/counters-and-quotes.md, B1,
-- B7): COLLATE NOCASE ignores the case of ASCII letters only, so the name in
-- lower case, as counter.Key computes it, makes names unique and finds
-- them. Names were ASCII until now, so lower() computes the same key.

-- +goose Up
ALTER TABLE counters ADD COLUMN name_key TEXT NOT NULL DEFAULT '';
UPDATE counters SET name_key = lower(name);
CREATE UNIQUE INDEX counters_name_key ON counters (name_key);

-- +goose Down
DROP INDEX counters_name_key;
ALTER TABLE counters DROP COLUMN name_key;
