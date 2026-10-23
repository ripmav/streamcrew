# Test-Matrix: eventsub-waf-keepalive

Der EventSub-WebSocket-Handshake dialt mit
`keepalive_timeout_seconds=10` neben `access_token` im Query-String
(Log-Analyse 2026-10-23): Twitchs CDN-WAF (CloudFront) blockiert Requests
unter `/ws` mit unbekanntem Query-Parameter, der kein gültiges
`keepalive_timeout_seconds` trägt, mit einer 403-Blockseite, bevor der
Token geprüft wird. Der Parameter ist dokumentiert (EventSub Reference
„Handling WebSocket Events“, Werte 10–600); der Wert 10 ist der
Server-Default, das Session-Verhalten ändert sich nicht.
Code-ADR-0015, Revision 2026-10-23.

## Erwartetes Verhalten

| Fall | Erwartet |
|---|---|
| Frischer Start (Base-URL) | Query trägt `access_token` **und** `keepalive_timeout_seconds=10` |
| `session_reconnect` | Reconnect-URL wird wie bisher verwendet (Token wird gemerged, `keepalive_timeout_seconds=10` kommt dazu) |
| Feld (echte Endpoint) | `session_welcome` statt CloudFront-403, auch von WAF-blockierten IPs |

## Abgedeckte Tests

| Fall | Test | Datei |
|---|---|---|
| Dial-Query trägt `keepalive_timeout_seconds=10` (und `access_token`) | `TestRunSessionReconnectAtTheGivenURL` (Assertion im `/`-Fall) | `internal/twitch/ws_test.go` |
| Echter Endpoint, echter Token: `session_welcome` | Feldtest 2026-10-23 (einmaliges Probe-Programm, nicht committet) | — |
