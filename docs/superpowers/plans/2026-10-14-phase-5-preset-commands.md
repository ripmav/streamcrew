# Plan: 5.6 Vorgefertigte Commands (P0) — Mechanismus + Anzeige-Commands

**Status:** erledigt 2026-10-16 (Mechanismus + Anzeige-Commands; Set*/Management-Commands folgen)
**Stand:** 2026-10-16
**Stack:** Basis für die übrigen P0-Commands

## Goal
Der Core liefert vorgefertigte Chat-Commands (Roadmap 5.6, P0; Plan
Anhang A.3). Dieser PR legt den Mechanismus an und liefert die
Anzeige-Commands, die mit bestehenden Templates und der Chat-Action laufen:
Uptime, FollowAge, Titel, Kategorie.

## Design decisions
- **Paket** `internal/preset`: `Commands() []command.Command` gibt die
  vorgefertigten Commands als Daten zurück (Commands-as-Code, Code-ADR-0013).
  Jeder Anzeige-Command ist ein Chat-Command (`!name`), der über die
  bestehende Chat-Action (`action/chat`, Kind `message`) den passenden
  Identifier antwortet:
  - `uptime` → `$streamuptimetotal` (z. B. `3:42`)
  - `followage` → `$followage` (Follow-Dauer des anfragenden Nutzers)
  - `title` → `$streamtitle`
  - `game` → `$streamgamename`
- **Mechanismus (Seed):** die App legt beim Start jeden vorgefertigten
  Command an, solange kein Command mit demselben Namen existiert. So sind
  die Presets auf einem frischen Profil da und bleiben bei späteren Starts
  erhalten; ein gelöschtes eigenes Command mit gleichem Namen wird nicht
  überschrieben. Der Seed läuft nach dem Command-Service, vor der Engine.
- **Nicht in diesem PR:** `Commands` (Liste), `SetTitle`/`SetGame` (brauchen
  neue Stream-Actions + Plattform-Operationen) und `AddCommand`/
  `UpdateCommand`/`DisableCommand` (Command-Management-Actions) — folgen als
  eigene PRs auf dem Stack.

## Files
- `internal/preset/preset.go`: `Commands()`.
- `internal/preset/preset_test.go`: die Presets sind gültige Chat-Commands
  mit je einer message-Action und dem richtigen Identifier.
- `internal/app/app.go`: `seedPresets` (Seed je Name), Aufruf im Start.
- `internal/app/*_test.go`: Seed-Test (frisch → vorhanden; vorhanden →
  unangetastet).
- `docs/roadmap.md` (5.6, P0-Bullet: Anzeige-Commands), dieses Planfile.

## Tests
| Test | Level |
|------|-------|
| `Commands()` liefert uptime/followage/title/game als Chat-Commands | unit (preset) |
| jeder Preset hat eine message-Action mit dem Identifier | unit (preset) |
| Seed: leeres Profil → die Presets werden angelegt | unit (app) |
| Seed: vorhandener Command mit gleichem Namen bleibt | unit (app) |
