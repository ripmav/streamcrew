# Plan: 5.6 Phase-5 E2E-Exit-Kriterien

**Status:** erledigt
**Stand:** 2026-10-16
**Stack:** auf PR #252 (Commands-Liste)

## Goal
Die Phase-5-Exit-Kriterien: E2E-Tests mit der Mock-Plattform decken
Chat-Pipeline, Timer, Moderation und Watchtime (simulierte Live-Session) ab.

## Design decisions
- **Chat-Pipeline** ist schon in `internal/app/chat_test.go` abgedeckt
  (Protokoll, Stummer Chat, Flüstern, Nutzer-Statistiken).
- **Timer** (5.4): `TestTimerE2E` — ein Timer-Command in einer
  `CommandGroup` mit `timerInterval: 1s`; die Timer-Gruppe feuert den
  Command, dessen Chat-Action die Mock-Plattform erreicht.
- **Moderation** (5.5): `TestModerationFilterE2E` — Wortfilter blockiert
  `badsong`; `!play badsong` wird an der Moderationsstufe verworfen (kein
  Command, keine Antwort), `!play good` läuft durch. Neue
  `startedCoreModeration`-Testhilfe (sichert die `moderation`-Sektion).
- **Watchtime** (simulierte Live-Session): `TestWatchTimeE2E` — `GoLive`
  auf der Mock, Alice wird aktiv; der Watch-Tick zählt die Watchtime. Der
  Tick (Default 1 Min, B9) ist jetzt über `userservice.WithWatchTick` und
  `app.WithWatchTick` verkürzbar, damit der Test real laufen kann.

## Tests
- `internal/app/phase5_e2e_test.go`: die drei neuen E2E-Tests.
- `internal/userservice`: `WithWatchTick` (Default bleibt 1 Min,
  `TestWatchTime` unverändert).
