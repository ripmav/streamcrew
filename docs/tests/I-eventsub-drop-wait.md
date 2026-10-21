# Test-Matrix: eventsub-drop-wait

Fester 10-s-Wart nach ungeplanten EventSub-Abbrüchen (Plan A,
Log-Analyse 2026-10-21, F1): `Client.Run` benutzt kein exponentielles
Backoff mit 30-s-Lifetime-Budget mehr; nach dem 10-s-Wart (`dropWait`,
`Options.DropWait` für Tests) wird am Base-URL erneut gedialt.
`session_reconnect` bleibt sofort; ein offener Breaker mit bekanntem
`retry-after` wird im eigenen Takt ge-probt (der längere der beiden
Wartezeiten). Code-ADR-0015, Revision 2026-10-21.

## Erwartetes Verhalten

| Fall | Erwartet |
|---|---|
| Dial schlägt fehl (500) | 10 s warten, dann erneut am Base-URL dialen, Handshake trägt `Twitch-Session-Id` |
| Breaker offen, `retry-after` +30 s | kein Dial vor `retry-after`; Probe mit `retry-after`; kein Busy-Loop (~286 Dial/s) |
| Drop nach einer erfolgreichen Session | 10-s-Wart gilt weiterhin (kein Lifetime-Budget, das aufgebraucht wäre) |
| `session_reconnect` | sofortiger Reconnect an der gegebenen URL (kein Wart) |
| Token-Verlust (`ErrToken`) | `Run` endet ohne Wart (fatal) |

## Abgedeckte Tests

| Fall | Test | Datei |
|---|---|---|
| Dial-Fehler: 10-s-Wart, Retry am Base-URL, `Twitch-Session-Id` | `TestRunDialFailureRetriesAtTheBaseURL` (synctest, Fake-Clock) | `internal/twitch/ws_test.go` |
| Busy-Loop-Regression: kein Dial vor Breaker-`retry-after`, Probe danach | `TestRunBreakerOpenWaitsTheRetryAfter` (synctest, Fake-Clock) | `internal/twitch/ws_test.go` |
| 10-s-Rhythmus auch nach erfolgreicher Session (kein Budget) | `TestRunDropWaitAfterASuccessfulSession` (synctest, Fake-Clock) | `internal/twitch/ws_test.go` |
| `session_reconnect` sofort an der gegebenen URL | `TestRunSessionReconnectAtTheGivenURL` | `internal/twitch/ws_test.go` |
| Token-Verlust endet ohne Backoff | `TestRunMissingTokenEndsWithoutBackoff` | `internal/twitch/ws_test.go` |
| Breaker wird von den Handshake-Fehlern geöffnet (100-ms-Test-Wart) | `TestClientBreakerOpens` | `internal/twitch/ws_test.go` |
