# Plan: Phase 5, Teil 5.2.2 – Cross-platform roles, Regular role, follow/subscription cache

Status: **abgeschlossen** (PR 1: Regular-Rolle; PR 2: Follow-/Abo-Cache)

**Goal:** The regular role (B26) from whole hours of watch time, and follow/subscription data with on-demand cache (B10).

Two PRs, one stack: PR 1 is the regular role, PR 2 the follow/subscription cache.

## Scope

| # | Item | Source | Size |
|---|------|--------|------|
| 1 | Settings section `users` (regular hours), Regular role on user, immediate re-evaluation | roadmap.md §5.2.2, users-and-roles.md B26, A4 | M |
| 2 | Connector port for follow/subscription data, Twitch + Mock implementations, on-demand cache in the users service | roadmap.md §5.2.2, users-and-roles.md B10 | M |

Cross-platform roles (B20–B24) are already fully modeled in `internal/domain/user` (per-identity roles + union via `User.Roles(p)`); the regular role is the only role that is not platform-managed and it belongs to the user (A4). Nothing to implement beyond items 1–2; covered by tests.

## 1. PR 1: Regular role

### Design decisions

- **Threshold:** New settings section `users` (polydoc, v1): `regularHours` (int ≥ 0, default 0 = role off). Whole hours: a user is regular iff `regularHours > 0` and `WatchMinutes >= regularHours * 60`.
- **Immediate re-evaluation:** Watch time only changes in the per-minute tick, so the tick evaluates the regular role for every user whose watch time was credited. A changed threshold is picked up by the next tick (≤ 1 min) and triggers a full sweep over all users. Decision: "immediately" = within the same minute the watch time is credited, within one minute for threshold changes; a settings-change event does not exist and is not added (decision).
- **On the user, not the identity (A4):** the stored `regular` flag on the user row; `User.Roles(p)` already unions it in on every platform. Requirement "minimum role" and templates read it via the fresh user lookup.
- No new events; the users service stays the only writer of the flag.

### Store

- New query `SetUserRegular` (`UPDATE users SET regular = $1 WHERE id = $2`), `Store.SetRegular(ctx, id, bool)`.

### Users service

- On each watch tick: after `AddStats`, re-evaluate the credited users (fresh `Get` → compare → `SetRegular` when it changes).
- Read the `users` section each tick; if `regularHours` differs from the previous value, re-evaluate all users (sweep).
- Threshold 0 → regular is revoked for everyone (sweep).

## 2. PR 2: Follow/subscription cache

### Design decisions

- **Port:** Optional connector capability `connector.PlatformData`:
  `UserPlatformData(ctx, platformUserID) (PlatformUserData, error)` with `FollowedAt time.Time` (zero = no follow), `SubscribedAt time.Time` (zero = no subscription, B10), `SubTier int` (0 = none).
- **On demand with cache:** the users service refreshes the identity's cached `PlatformData` when a user joins or messages and the cache is missing or older than 1 hour (constant, decision). The refresh runs in the service goroutine and never blocks the event path. Templates and requirements keep reading the cached value (no I/O in templates — decision).
- **Twitch:** `GetFollows(user, channel)` for `FollowedAt`; `GetChannelSubscriptions(channel)` for tier/`SubscribedAt` (the Helix endpoint has no start date, so `SubscribedAt` stays zero — decision, documented in the spec).
- **Mock:** known users return `FollowedAt` 30 days before now; unknown users return zeros.

### Mock platform

- Implement `connector.PlatformData`; join/message of a known user makes it "follow the channel".

## 3. Tests

| Test | Level |
|------|-------|
| Regular: watch time crosses the threshold → regular on; below → off; threshold 0 → off | unit (users service) |
| Threshold change → sweep flips all users | unit |
| Store: `ListUsersRegular` sweep rows (ID order, regular flag, watch minutes) | integration (embedded) |
| Settings: defaults, save/load, validation of `regularHours` | unit |

No app-level E2E for the regular role: the tick is a real-time minute and a threshold of at least one hour, so the role cannot flip inside an app test (decision). The template and requirement read the stored `Regular` flag, which the domain tests and the 5.1 E2E already cover.

PR 2 (same test-matrix file, added with the PR):
| PlatformData: stale/missing cache refreshed on contact, fresh cache not refetched | unit |
| Twitch: `UserPlatformData` maps Helix responses (httptest) | unit |
| Mock: follow age visible, unknown user zeros | unit |
| Store: `SetRegular` round-trip | integration (embedded) |
| E2E: user watches a (fake) hour → regular role; requirement with minimum role regular allows, below denies | app |

Test matrix: `docs/tests/phase-5-regular-role.md`.

## 4. Docs

- roadmap.md: 5.2.2 checked; `docs/spec/users-and-roles.md`: B26/B10 decisions; plan marked done.

## 5. Open questions

(none – decisions documented above)