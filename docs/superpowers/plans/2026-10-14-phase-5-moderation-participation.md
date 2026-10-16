# Plan: 5.5 Teilnahmeregeln

**Status:** erledigt 2026-10-16
**Stand:** 2026-10-16
**Stack:** auf PR #248 (Strikes)

## Goal
Nur Nutzer, die alle gesetzten Teilnahmeregeln erfüllen, dürfen chatten
(spec moderation.md, B3, B24). Die Prüfung läuft an der Moderationsstufe vor
den Filtern; ein Nichtbestehen verwirft die Nachricht ohne Strike und ohne
Folge-Command.

## Design decisions
- **Prüfung** in `Service.Check`, nach dem Streamer-Bypass (B2), vor den
  Filtern (B1). Der Bot-Konto-Pfad läuft vor der Moderation im Event-Service
  (recordLocked), braucht keine Ausnahme hier.
- **Regeln** (B3), alle müssen erfüllt sein; jede einzelne nicht gesetzt =
  keine Regel:
  - **Mindestrolle** `MinRole` (leer = keine): `Identity.Roles.Meets(MinRole)`.
  - **Mindest-Watchtime** `MinWatchMinutes` (0 = keine):
    `user.Stats.WatchMinutes >= MinWatchMinutes`.
  - **Mindest-Follow-Dauer** `MinFollowDays` (0 = keine): ganze Tage seit
    `Identity.Data.FollowedAt`; unbekannt (zero) = nicht erfüllt.
- **Zeit** über einen Port `Now func() time.Time` (default `time.Now`),
  damit die Tage-Zahl testbar ist.
- **Nichtbestehen:** `Decision{Allow:false}` ohne Strike, ohne Command, ohne
  verletzten Filter (B3) — die Nachricht wird verworfen wie sonst.

## Files
- `internal/moderation/moderation.go`: Port `Now`, `participation`,
  `wholeDays`, Aufruf in `Check`.
- `internal/moderation/moderation_test.go`: `Now` im `fx`; Tests für B3, B24.
- `internal/app/app.go`: `Now: time.Now` (optional — Default reicht).
- `docs/roadmap.md` (5.5, Teilnahmeregeln-Bullet), dieses Planfile.

## Tests
| Test | Level |
|------|-------|
| Ohne Regeln: jeder chattet (B24) | unit (moderation) |
| Mindestrolle erfüllt / nicht erfüllt (B3) | unit (moderation) |
| Mindest-Watchtime erfüllt / nicht erfüllt (B3) | unit (moderation) |
| Mindest-Follow-Dauer erfüllt / nicht erfüllt / unbekannt (B3) | unit (moderation) |
| Nichtbestehen: verworfen, ohne Strike, ohne Command (B3) | unit (moderation) |
| Streamer läuft an den Regeln vorbei (B2) | unit (moderation) |
