# Test-Matrix: User-Nachrichten-Datei und API-Antworten im Log

Anfrage des Projektinhabers (2026-10-21): im Debug-Log-Level und/oder im
Dry-Run sollen (1) alle User-Nachrichten aus IRC/EventSub/Helix in einer
extra Datei geloggt werden und (2) alle Antworten der APIs (Twitch,
BetterTTV usw.) mit ihrem Body im normalen Log landen.

Beides wird an einer Stelle gefasst: alle Adapter (IRC, EventSub, Helix)
reichen ihre User-Nachrichten über denselben `connector.Receiver` in den
Event-Service, und alle API-Anfragen laufen durch denselben
`httpclient.Client` (Code-ADR-0014).

## Erwartetes Verhalten

| Fall | Erwartet |
|---|---|
| Gate offen (Log-Level ≤ debug oder `dry_run`) und Log-Datei an | `<Log-Verzeichnis>/messages.json` wird geöffnet (JSON, Rotation wie `streamcrew.log`), Info-Eintrag `the user messages are logged to the separate file` |
| Gate zu (info/warn/error ohne Dry-Run) oder `log_file: false` | keine `messages.json`, kein Eintrag |
| Chat-Nachricht eines Users (IRC, EventSub oder jede andere Quelle) | Eintrag `user chat message` in `messages.json` mit `platform`, `channel`, `user`, `from_bot`, vollem `text`. IRC und EventSub laufen über denselben `connector.Receiver`: `TestIRCSessionMessage` (PRIVMSG/Whisper → Receiver) + `TestUserMessagesLog` (Receiver → Messages-Logger) decken die Kette ab |
| Whisper eines Users (IRC `#whisper` oder `user.whisper.message`) | Eintrag `user whisper` mit `platform`, `user`, vollem `text` |
| Andere Ereignisse (Follow, Raid, Subs, …) | kein Eintrag in `messages.json` |
| Gate offen: API-Antwort (Helix, BTTV, FFZ, Auth) | Info-Eintrag `response body` im normalen Log mit `api`, `method`, `url`, `status`, `truncated`, `body` |
| Antwort-Body > 64 KiB | `truncated: true`, Log hält nur das Präfix; **der Caller bekommt den vollständigen Body** |
| Antwort eines Token-Endpunkts (Pfad endet auf `/oauth2/token`, z. B. Code-Exchange, Refresh) | **kein** `response body`-Eintrag — die Antwort trägt die Tokens selbst; der Caller bekommt sie ungekürzt |
| Gate zu: API-Antwort | kein `response body`-Eintrag |
| Gate-Änderung zur Laufzeit (Hot-Setting) | gilt erst beim nächsten Start (beim Start entschieden, dokumentiert) |

## Abgedeckte Tests

| Fall | Test | Datei |
|---|---|---|
| Separate Datei: JSON-Zeile mit Attributen, Closer | `TestFileLogger` | `internal/logging/logging_test.go` |
| Antwort-Body im Client-Log (Info), vollständiger Body beim Caller, Trunkation über 64 KiB, Token-Antwort nie geloggt | `TestDoLogsResponseBodies` | `internal/httpclient/client_test.go` |
| Chat-Nachricht und Whisper im Messages-Logger, kein anderes Ereignis, ohne Option kein Logger | `TestUserMessagesLog` | `internal/eventservice/service_test.go` |

## Manuelle Prüfung

- [ ] `streamcrew serve --log-level debug` (oder `--dry-run`):
      `<data-dir>/logs/messages.json` entsteht, jede Chat-Nachricht und
      jeder Whisper landet dort mit vollem Text.
- [ ] Dasselbe Run: normale Logs (`streamcrew.log`, Konsole) zeigen
      `response body`-Einträge für die Helix-/BTTV-/FFZ-Anfragen.
- [ ] Ohne Debug-Level und ohne `--dry-run`: keine `messages.json`,
      keine `response body`-Einträge.
