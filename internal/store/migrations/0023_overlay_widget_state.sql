-- SPDX-License-Identifier: MIT
--
-- The mutable state of the overlay widgets (spec overlays.md, B20,
-- roadmap 7.2): one JSON document per widget, e.g. the goal progress or
-- the persistent timer. A restart does not change the state; rebuilding
-- the profile starts with the defaults. The widget ID is the ID of the
-- widget in the settings section "overlay" (B19); the core keeps no other
-- data about the widget here.

-- +goose Up
CREATE TABLE overlay_widget_state (
    widget_id TEXT NOT NULL,
    state     TEXT NOT NULL, -- JSON document of the widget state
    PRIMARY KEY (widget_id)
) STRICT;

-- +goose Down
DROP TABLE overlay_widget_state;
