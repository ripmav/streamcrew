# Test-Matrix: TUI-Stream-Reconnect

Issue: Code-Review 2026-10-24, U6 — der TUI-Stream hatte keinen Reconnect, verlor Events still ab Puffergrenze 64 und lief auf `context.Background()` statt dem TUI-Kontext.

| Test | Datei | Deckt ab |
|---|---|---|
| `TestStreamReconnectsAfterABreak` | `internal/tui/stream_test.go` | Eine abgerissene `Subscribe`-Strömung wird automatisch neu aufgezogen (mit dem `since`-Marker des letzten erhaltenen Events) und liefert danach die Events weiter; der Abbruch läuft über den TUI-Kontext. |
| `TestStreamReportsTheDroppedEvents` | `internal/tui/stream_test.go` | Fallen beim Reconnect Events weg, meldet der Stream die Lücke (Count der verlorenen Events), statt sie still zu verschlucken. |

Nicht abgedeckt: das Verhalten unter echtem Netzwerkausfall (Time-to-First-Byte, Server-Restarts) — die Tests treiben die Strömung über einen gesteuerten Fake-Server.
