# Plan: 5.5 Settings-Sektion moderation

**Status:** erledigt 2026-10-16
**Stand:** 2026-10-16

## Goal
Die Settings-Sektion `moderation` mit den Daten für die Teilnahmeregeln, die
Wortfilter und die Strike-Stufen ([`moderation.md`](../../spec/moderation.md),
B3, B4, B10, B13). Sie ist das Fundament der Moderation; die Prüfung folgt in
den nachgestapelten PRs.

## Design decisions
- **Sektion `moderation`** (`internal/settings`):
  - Teilnahmeregeln: `minFollowDays` (0 = keine), `minWatchMinutes` (0 = keine),
    `minRole` (leer = keine) (B3).
  - `filters`: Liste von Filtern, in der Reihenfolge der Prüfung (B8, B13).
    Je Filter `kind` (`word`/`link`/`uppercase`/`punctuation`/`emote`),
    `word` (Bannwort mit Wildcards, nur `word`), `threshold` + `percent`
    (Schwelle absolut oder prozentual, nur die drei Schwellen-Filter),
    `exempt` (Rollen, befreit) (B4–B7).
  - `strikeLevels`: Liste von (Stufe, Command), Stufen ab 1, keine doppelten
    (B10).
- **Validierung:** keine negativen Regeln; bekannte `kind`; `word` nicht leer;
  Schwellen ≥ 0 (prozentual 0–100); bekannte Ausnahmen- und Mindestrollen;
  Stufen ≥ 1 und eindeutig.
- **Defaults:** leer = keine Teilnahmeregeln, keine Filter, keine Stufen —
  alles läuft durch (B3, B20, B24).

## Files
- `internal/settings/settings.go`: `Moderation`, `Filter`, `FilterKind`,
  `StrikeLevel`, Defaults, Validate, Registry-Registrierung, `role`-Import.
- `internal/settings/settings_test.go`: Validierungsfälle.
- `docs/roadmap.md` (5.5, Settings-Bullet), dieses Planfile.

## Tests
| Test | Level |
|------|-------|
| `Moderation.Validate`: Regeln, Filter-`kind`, leeres Wort, Schwellen, Ausnahmen, Stufen (Negativ) | unit (settings) |
