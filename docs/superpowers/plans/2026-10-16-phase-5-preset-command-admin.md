# Phase 5.6: Preset AddCommand, UpdateCommand, DisableCommand

Status: **erledigt** (2026-10-16)

## Ziel

Roadmap 5.6, P0-Teil: Drei Presets, die Chat-Commands aus dem Chat verwalten:
- `!addcmd <name> <answer>` — legt einen Chat-Command mit Trigger `<name>` an, der mit `<answer>` antwortet.
- `!updatecmd <name> <answer>` — ändert den Antworttext eines vorhandenen Chat-Commands.
- `!disablecmd <name>` — deaktiviert einen vorhandenen Chat-Command.

Sicherheit: Die Presets tragen die Anforderung „Rolle: Moderator“, damit die Chat-Nutzer die Commands des Profils nicht ändern können.

## Umsetzung

### Action-Typ `command_admin` (`internal/action/commandadmin`)

- Kinds: `add`, `update`, `disable`.
- Einzelnes Feld `kind`; Name und Antworttext liest die Action aus den
  Argumenten des Durchlaufs (`run.Params().Args`): erstes Argument = Trigger,
  der Rest (per Leerzeichen verbunden) = Antworttext. Es existiert kein
  eigener Identifier für „alle Argumente ab dem zweiten“, daher liest die
  Action die Argumente direkt (wie die Moderation die Params liest).
- Ports: `Commands` (Interface mit `Commands`, `Save`, `SwitchCommand`) und
  `Logger`. Der Command-Service wird erst nach dem Build der Registry über
  einen Late-Port (`lateCommands` in `internal/app`) aufgelöst, wie der
  Command-Schalter (Code-ADR-0013, Punkt 3).
- Verhalten:
  - `add`: aktiver Chat-Command (Trigger = Argument 1, Antwort = Rest);
    scheitert bei leerem Text oder vorhandenem Trigger.
  - `update`: ändert den Antworttext des Chat-Actions eines vorhandenen
    Commands; scheitert bei fehlendem Command oder leerem Text.
  - `disable`: schaltet den vorhandenen Command aus (`SwitchOff`).
  - Trigger-Suche unabhängig von Groß-/Kleinschreibung (ASCII); mehrere
    Treffer scheitern (eindeutig).

### Presets (`internal/preset`)

- `admin(name, kind)`-Helfer: Chat-Command mit `commandadmin`-Action und
  `command.RoleRequirement{Role: role.Moderator}`.
- `addcmd` (KindAdd), `updatecmd` (KindUpdate), `disablecmd` (KindDisable).
- Preset-Anzahl: 7 → 10.

### Registrierung

- `internal/app/app.go`: `lateCommands` anlegen, `actionTypes` um den
  `commandadmin`-Descriptor erweitern, nach dem Build des Command-Services
  `lateCmds.set(a.commands)`.
- `internal/app/catalog.go`: `commandadmin.Catalog()`.
- Schema golden (`schemas/streamcrew-v1alpha1.schema.json`) regeneriert
  (`STREAMCREW_UPDATE_GOLDEN=1`).

### Tests

- `internal/action/commandadmin/commandadmin_test.go`: add, add-weigert-sich
  bei vorhandenem Trigger, update, disable, nicht gefunden, keine Argumente,
  Validate.
- `internal/preset/preset_test.go`: 10 Presets, Admin-Presets mit
  Moderator-Rollenanforderung.
- `internal/app/app_test.go`: `TestNewSeedsPresets` erwartet 10 Presets.

### Doku

- `docs/spec/actions.md`: Abschnitt „Command-Verwaltung“ (B122–B125) +
  Änderungshistorie.
- `docs/roadmap.md`: P0-Teil 5.6 komplett, Counter-Service abgedeckt; Phase-5
  Status-Zeile und Änderungshistorie.

## Festlegungen

- Name/Antworttext aus den Argumenten des Durchlaufs (kein eigener
  Identifier), weil dafür kein Identifier existiert.
- Die Presets sind Moderator-Rolle beschränkt (Sicherheit: der Chat darf die
  Commands des Profils nicht ändern).
- Neue Commands sind Daten wie jeder andere Command (Code-ADR-0013) und
  werden mit der Fehlerpolitik des aufgerufenen Commands gespeichert.
- Trigger-Suche ASCII-schreibungsunabhängig; mehrdeutig = Scheitern.

## Verzicht

- Kein eigener Identifier für „Argumente ab dem zweiten“ (die Action liest
  die Params direkt).
- Keine Mehrfach-Trigger oder mehrwortige Trigger über die Presets (ein
  Token pro Command); dafür der Command-Editor.
- Keine E2E auf echten Twitch-Kanälen (bleibt Exit-Kriterium Phase 6).
