-- SPDX-License-Identifier: MIT
--
-- The fine levels of the role ranking (docs/spec/users-and-roles.md, B20):
-- the stored roles of platform accounts move from the shared levels to the
-- level of their platform. A shared level becomes the level of the
-- account's platform, otherwise the lowest of its levels, so that nobody
-- gains a role; the next update from the platform sets the exact one.
-- Role requirements of commands move with their codec (version 2).

-- +goose Up
UPDATE user_identities SET roles = (
    SELECT json_group_array(DISTINCT CASE value
        WHEN 'creator' THEN 'twitch_affiliate'
        WHEN 'follower' THEN iif(user_identities.platform = 'youtube', 'youtube_subscriber', 'follower')
        WHEN 'vip' THEN iif(user_identities.platform = 'kick', 'kick_vip', 'twitch_vip')
        WHEN 'subscriber' THEN iif(user_identities.platform = 'youtube', 'youtube_member', 'subscriber')
        WHEN 'platform_staff' THEN 'twitch_global_mod'
        ELSE value
    END)
    FROM json_each(user_identities.roles)
);

-- +goose Down
UPDATE user_identities SET roles = (
    SELECT json_group_array(DISTINCT CASE value
        WHEN 'twitch_affiliate' THEN 'creator'
        WHEN 'twitch_partner' THEN 'creator'
        WHEN 'youtube_subscriber' THEN 'follower'
        WHEN 'twitch_vip' THEN 'vip'
        WHEN 'kick_vip' THEN 'vip'
        WHEN 'kick_og' THEN 'vip'
        WHEN 'velora_vip' THEN 'vip'
        WHEN 'vpzone_plus' THEN 'vip'
        WHEN 'vpzone_founder' THEN 'vip'
        WHEN 'vpzone_ambassador' THEN 'vip'
        WHEN 'youtube_member' THEN 'subscriber'
        WHEN 'twitch_global_mod' THEN 'platform_staff'
        WHEN 'twitch_staff' THEN 'platform_staff'
        ELSE value
    END)
    FROM json_each(user_identities.roles)
);
