# Phase 6.3 (1/4b): `command`- und `user`-Befehle über die API

Status: **in Arbeit** (2026-10-17)
Branch/PR: `feat/cli-command-user`, auf `feat/cli-counter` gestapelt
(Stack #258).

## Ziel (Plan §7.2, Roadmap 6.3.1)

Die restlichen MVP-Unterkommandos für einen laufenden Core:
`command list|show|run|delete|enable|disable` und `user list|show|set`
(`user merge` existiert lokal bereits), alle mit `-o json`.

## Entscheidungen

### `command` (API-Subkommandos neben den File-Befehlen)

- Commands sind über UUIDs adressiert; die CLI arbeitet mit **Namen** und
  löst sie per List (exakter Name-Treffer, mehrdeutig ⇒ Fehler) auf.
- `command list [--kind] [--prefix]`: List-RPC.
- `command show <name>`: Get-RPC, zeigt Summary + Dokument.
- `command run <name> [args…] [--channel]`: Run-RPC, zeigt die
  Instance-ID.
- `command delete <name>`: Delete-RPC.
- `command enable|disable <name>`: neuer RPC
  `CommandService.SetEnabled{id, enabled}` (Scope `control`) — ein
  dokument-Rundtrip nur fürs An-/Ausschalten wäre überdimensioniert;
  der Core holt das Command, flippt `Enabled` und speichert.
- Scope-Mapping: List/Show = read, Run/Delete/Enable/Disable = control.

### `user` (API-Subkommandos neben merge)

- `user list [--prefix] [--stat] [--limit]`
- `user show <login> --platform <p>`: Get per `platform/login`
- `user set <login> --platform <p> [--role] [--title] [--excluded/--no-excluded]`:
  Update (Get ⇒ ID ⇒ Update)
- `user merge` bleibt der lokale Befehl (Core gestoppt).

## Tests

- API: `TestAPIServesSetEnabled` (an/aus, unbekannter ID)
- CLI: Kong-Struktur + `command list/show/run/enable/disable/delete` und
  `user list/show/set` gegen einen in-process Core mit Mock-Plattform
  (Commands über den Import, Nutzer per simulierte Nachricht).

## Reihenfolge

1. command.proto: SetEnabled + buf generate
2. api + scopes + app-Adapter
3. cli: command- und user-Befehle
4. Tests, Roadmap/Changelog, Checks, PR
