-- SPDX-License-Identifier: MIT

-- name: GetOverlayWidgetState :one
SELECT state FROM overlay_widget_state WHERE widget_id = ?;

-- name: SetOverlayWidgetState :exec
INSERT INTO overlay_widget_state (widget_id, state)
VALUES (?, ?)
ON CONFLICT (widget_id) DO UPDATE SET state = excluded.state;
