-- SPDX-License-Identifier: MIT
--
-- The event feed and the per-session statistics (roadmap 5.3): the recent
-- events of the feed types (follow, subscription, raid, cheer, donation)
-- that frontends read, and the statistics of each stream session. Times in
-- milliseconds (Code-ADR-0009).

-- +goose Up
-- The recent feed events, newest last; bounded, the store keeps only the
-- latest 200 (roadmap 5.3). amount is the type's number (bits, viewers,
-- gift count), cents a donation, tier a subscription, text the shared text.
CREATE TABLE event_feed (
    id           INTEGER PRIMARY KEY,
    platform     TEXT NOT NULL,
    type         TEXT NOT NULL,
    user_id      TEXT REFERENCES users (id) ON DELETE CASCADE,
    login        TEXT NOT NULL,
    display_name TEXT NOT NULL,
    occurred_at  INTEGER NOT NULL,
    amount       INTEGER,
    cents        INTEGER,
    tier         INTEGER,
    text         TEXT
) STRICT;
CREATE INDEX event_feed_occurred ON event_feed (occurred_at, id);

-- The statistics of one stream session (platform, session start), so the
-- history of the sessions stays; the current session's row is updated as
-- feed events fire (roadmap 5.3).
CREATE TABLE session_stats (
    platform             TEXT NOT NULL,
    started_at           INTEGER NOT NULL,
    followers            INTEGER NOT NULL DEFAULT 0,
    subscribers          INTEGER NOT NULL DEFAULT 0,
    resubscribers        INTEGER NOT NULL DEFAULT 0,
    gift_subscriptions   INTEGER NOT NULL DEFAULT 0,
    raids                INTEGER NOT NULL DEFAULT 0,
    cheers               INTEGER NOT NULL DEFAULT 0,
    donations            INTEGER NOT NULL DEFAULT 0,
    bits_total           INTEGER NOT NULL DEFAULT 0,
    donation_cents_total INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (platform, started_at)
) STRICT;

-- +goose Down
DROP TABLE session_stats;
DROP TABLE event_feed;
