-- SPDX-License-Identifier: MIT

-- The event feed and the per-session statistics (roadmap 5.3).

-- name: InsertFeedEvent :exec
INSERT INTO event_feed (platform, type, user_id, login, display_name, occurred_at, amount, cents, tier, text)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: PruneFeed :exec
DELETE FROM event_feed WHERE id NOT IN (
    SELECT id FROM event_feed ORDER BY occurred_at DESC, id DESC LIMIT ?
);

-- name: LatestFeed :many
SELECT * FROM (
    SELECT * FROM event_feed ORDER BY occurred_at DESC, id DESC LIMIT ?
) ORDER BY occurred_at ASC, id ASC;

-- name: AddSessionStats :exec
INSERT INTO session_stats (
    platform, started_at, followers, subscribers, resubscribers, gift_subscriptions,
    raids, cheers, donations, bits_total, donation_cents_total
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, started_at) DO UPDATE SET
    followers = followers + excluded.followers,
    subscribers = subscribers + excluded.subscribers,
    resubscribers = resubscribers + excluded.resubscribers,
    gift_subscriptions = gift_subscriptions + excluded.gift_subscriptions,
    raids = raids + excluded.raids,
    cheers = cheers + excluded.cheers,
    donations = donations + excluded.donations,
    bits_total = bits_total + excluded.bits_total,
    donation_cents_total = donation_cents_total + excluded.donation_cents_total;

-- name: GetSessionStats :one
SELECT * FROM session_stats WHERE platform = ? AND started_at = ?;

-- name: ListSessionStats :many
SELECT * FROM session_stats WHERE platform = ? ORDER BY started_at DESC LIMIT ?;
