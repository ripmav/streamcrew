# Plan: 5.6 Commands-Liste (Preset `!commands`)

**Status:** erledigt
**Stand:** 2026-10-16
**Stack:** auf PR #251 (SetTitle/SetGame)

## Goal
Der P0-Preset `!commands` listet die verfügbaren Chat-Commands (Roadmap 5.6,
P0, „Commands-Liste“), damit Streamer und Zuschauer sehen, was es gibt.

## Design decisions
- **Template-Quelle** `$commands` (roadmap 5.6): ein fester Identifier, der die
  Triggers aller **aktiven Chat-Commands** aufzählt, mit `„, “` getrennt.
  Exclamation-Commands erhalten das `!` wie eingegeben. Inaktive und
  Nicht-Chat-Commands (Events, Timer) bleiben draußen.
- **`template.CommandSource(lister)`** (`internal/template/commands.go`):
  `Match` trifft nur auf exakt `commands` (keine Präfix-Treffer, z. B.
  `$commandsfollowage` zählt nicht). Die Liste wird je Render einmal gelesen
  (`Scope.Memo`), wie bei `CounterSource`.
- **Preset** `commands` → Antwort `$commands` (`internal/preset`), wie die
  anderen Anzeige-Commands.
- **Kein neues Speichern/Reservieren nötig:** `$commands` ist eine
  Quell-Identifikation, kein von Nutzern vergeben Name. Ein evtl. gleichnamiger
  Counter gewinnt gegen die Quelle (Reihenfolge der Quellen), vertretbar für
  P0.

## Tests
- `internal/template/commands_test.go`: Aufzählung (aktiv/Chat, `!`-Präfix),
  inaktive und Event-Commands aussortiert, kein Präfix-Treffer, leere Liste,
  einmal je Render.
- `internal/preset`, `internal/app`: der Preset wird mitgezählt.
