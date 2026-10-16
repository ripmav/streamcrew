# Plan: 5.5 Strike-Stufen und Folge-Commands

**Status:** erledigt 2026-10-16
**Stand:** 2026-10-16
**Stack:** auf PR #247 (Wortfilter)

## Goal
Beim Verstoß steigt der Strike-Zähler des Nutzers und der Folge-Command der
erreichten Stufe läuft (spec moderation.md, B9, B10, B11, B23, B25, B26).

## Design decisions
- **Ports des Moderation-Service** (alle required):
  - `Strikes`: `UpdateUser(ctx, userID, fn) (user.User, error)` — der
    User-Service implementiert es; der Verstoß erhöht `Stats.Strikes` um
    eins in einer Transaktion (B9).
  - `Commands`: `CommandByName(ctx, name) (command.Command, bool, error)`.
  - `Engine`: `Submit(ctx, engine.Request, done)` — fire-and-forget wie der
    Trigger-Pfad des Event-Service; die Outcomes werden geloggt (B11, B26).
- **Check bei Verstoß:**
  1. Strike erhöhen (`Strikes.UpdateUser`), neuen Zähler lesen (B9).
  2. Stufe für den neuen Zähler auflösen (B10): exakte Stufe, wenn gesetzt;
     über der höchsten gesetzten Stufe → die höchste (B23); sonst (zwischen
     gesetzten Stufen) → kein Command (nur der Zähler, B25).
  3. Ist ein Command gesetzt: nach Namen suchen, `Submit` mit den
     Parametern der Nachricht (Nutzer, Plattform, Channel, Nachricht,
     MessageID, Emotes), `Source: SourceChat` (B10, B11).
  4. `Decision` trägt den verletzten Filter, den neuen Zähler und den
     gelaufenen Command (für Tests und Log).
- **Verwerfen** bleibt wie PR #247: `Allow = false` verwirft die Nachricht
  (keine Ereignisse, kein Trigger, kein Verlauf).
- **Zurücksetzen** (B12) ist die bestehende Moderation-Action B84 —
  außerhalb dieses PRs.

## Files
- `internal/moderation/moderation.go`: `Ports` + `Strikes`/`Commands`/
  `Engine`, `Check` (Strike + Folge-Command), `Decision`-Felder,
  `strikeLevel`.
- `internal/moderation/filter.go`: (unverändert).
- `internal/moderation/moderation_test.go`: Fakes für Strikes/Commands/
  Engine; Tests für B9, B10, B23, B25.
- `internal/eventservice/fakes_test.go`: `fakeModeration` bekommt die
  Ports.
- `internal/app/app.go`: Service verdrahten (`userSvc`, `commands`,
  `engine`).
- `docs/roadmap.md` (5.5, Strikes-Bullet), dieses Planfile.

## Tests
| Test | Level |
|------|-------|
| Verstoß steigert den Strike (B9) | unit (moderation) |
| Folge-Command der exakten Stufe läuft (B10) | unit (moderation) |
| Über der höchsten Stufe → höchste (B23) | unit (moderation) |
| Keine Stufe → nur Zähler (B25) | unit (moderation) |
| Command-Parameter = die der Nachricht (B10) | unit (moderation) |
| Pipeline: Verstoß verwirft + steigert Strike | unit (eventservice) |
