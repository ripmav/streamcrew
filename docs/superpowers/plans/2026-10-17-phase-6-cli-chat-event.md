# Phase 6.3 (2/4): `chat send --as` und `event simulate` über die API

Status: **in Arbeit** (2026-10-17)
Branch/PR: `feat/cli-chat-event`, auf `feat/desktop-mode` gestapelt (Stack #258).

## Ziel (Roadmap 6.3, Plan §7.2)

`streamcrew chat send` und `streamcrew event simulate` sprechen einen
**laufenden Core** über die API an (statt nur der Mock-Konsole aus 3.6).
`chat send --as <login>` simuliert eine Chat-Nachricht eines Nutzers.

## Entscheidungen

### Simulate-RPC (neu, StreamService)

- `rpc Simulate(SimulateEventRequest) returns (SimulateEventResponse)` in
  `stream.proto`: Felder `event_type`, `user`, `target`, `text`; Antwort
  mit `event_id`.
- `event_type = "chat.message"` mit `user` + `text` simuliert eine
  Chat-Nachricht des Nutzers (Mock-Plattform, wie `Say` der Mock-Konsole);
  alle anderen Katalog-Typen gehen über `Platform.Simulate` der
  Mock-Plattform (deren Validierung bleibt: unbekannte Typen,
  Plattform-Typen, Core-Typen und chat.message ohne Say werden dort
  abgelehnt).
- Der Core braucht eine **verbundene Mock-Plattform**, sonst Fehler —
  simulate ist nur für Mock-/Dev-Zwecke (Plan §7.2).
- Scope: `control` (Schreibpfad).

### Chat send

- `streamcrew chat send <text…>` ohne `--as` ⇒ `ChatService.Send` (Bot
  sendet; `--as-streamer` optional, `--channel`).
- `streamcrew chat send --as <login> <text…>` ⇒ `StreamService.Simulate`
  mit `event_type=chat.message` (Nutzer sendet; unbekanntes Login wird
  Nutzer, wie in der Mock-Konsole).

### Remote-Verbindung (Grundlage, Rest in 6.3-Item 1)

- Global `--server URL` (Default `http://127.0.0.1:8740`) und
  `--token VALUE` (oder `STREAMCREW_TOKEN`), in `Root` neben
  `config.Config`.
- `e.apiClients()` baut den `http.Client` (Bearer-Header bei Token) und
  die Connect-Client.

## Tests

- `internal/api`: Simulate-Handler über den Connect-Client (chat.message
  mit --as erscheint im Chat, channel.follow feuert, ohne Mock-Plattform
  Fehler).
- `internal/cli`: `chat send`, `chat send --as`, `event simulate` gegen
  einen in-process gestarteten Core mit Mock-Plattform (wie `apiCore`),
  inkl. `--server`/`--token`.

## Reihenfolge

1. stream.proto: Simulate + buf generate
2. api: Stream-Interface + Handler + Scope
3. app: Simulate über die Mock-Plattform
4. cli: --server/--token + chat + event
5. Tests, Roadmap/Changelog, Checks, PR
