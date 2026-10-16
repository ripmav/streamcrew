# Plan: 5.5 Wortfilter und Bannwörter

**Status:** erledigt 2026-10-16
**Stand:** 2026-10-16

## Goal
Die Wortfilter und Bannwörter an der Moderationsstufe der Chat-Pipeline
([`moderation.md`](../../spec/moderation.md), B4–B8, B11): ein Service prüft
jede eingehende Nachricht nach dem Stummen Chat; eine verletzte Nachricht
wird verworfen.

## Design decisions
- **Service** `internal/moderation`: `Check(ctx, Message) Decision`.
  `Message` trägt Plattform, Channel, Text, Emotes, Nachricht-ID und den
  aufgelösten Nutzer; `Decision.Allow` = false verwirft die Nachricht.
- **Filter-Arten** (B4–B7):
  - `word`: Bannwort, ohne Rücksicht auf die Schreibweise, `*` als Wildcard
    (am Anfang/Ende/beiden) — das Wort muss ganz passen (B5).
  - `link`: `http(s)://` oder eine Bare-Domain mit Punkt (B6).
  - `uppercase`/`punctuation`/`emote`: absolut (Anzahl) oder prozentual
    (`percent`); Schwelle 0 = aus (B7). Prozentual: Großbuchstaben an den
    Buchstaben, Satzzeichen an den Buchstaben, Emotes an den Worten.
  - `exempt`: Rollen, befreit (B4, B22).
- **Reihenfolge:** der erste verletzte Filter zählt; eine Nachricht ist
  höchstens ein Verstoß (B8). Streamer läuft durch (B2).
- **Pipeline:** der Event-Service ruft `Moderation.Check` nach dem Stummen
  Chat auf (Port `Moderation`, required); `Allow=false` verwirft die
  Nachricht wie der Stumme Chat (keine Ereignisse, kein Trigger, kein
  Verlauf, B1, B11).
- **Ports:** `Settings` (liest `moderation` je Nachricht, B13). Strikes und
  Folge-Commands folgen im nächsten PR (B9–B10).

## Files
- `internal/moderation/moderation.go`: `Service`, `Ports`, `Message`,
  `Decision`, `Check`.
- `internal/moderation/filter.go`: die Filter-Prüfung (word/link/uppercase/
  punctuation/emote, absolut und prozentual).
- `internal/moderation/moderation_test.go`: Unit-Tests.
- `internal/eventservice`: Port `Moderation`, Pipeline-Aufruf, Fake.
- `internal/app`: Service verdrahten, `moderationSettings`.
- `docs/roadmap.md` (5.5, Filter-Bullet), dieses Planfile.

## Tests
| Test | Level |
|------|-------|
| wordViolates (Wildcards, Schreibweise, ganzes Wort) | unit (moderation) |
| linkViolates (http/https, Bare-Domain, keine) | unit (moderation) |
| uppercase/punctuation/emote Violates (absolut + prozentual, Schwelle 0) | unit (moderation) |
| Check (kein Filter, Verstoß, Streamer, Ausnahmen-Rolle) | unit (moderation) |
| Pipeline: verletzte Nachricht wird verworfen (keine Events/Trigger/Verlauf) | unit (eventservice) |
