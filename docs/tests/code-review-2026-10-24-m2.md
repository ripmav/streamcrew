# Test-Matrix: Code-Review 2026-10-24 — Findings bis M2 (Twitch, Event-Service, IRC, EventSub)

Issue: Code-Review 2026-10-24 (`docs/reviews/open-findings-2026-10-26.md`, Sektion 1) — die neuen Tests zu den Findings T5, T7, T8, T9, T10, T11, T13, T14, IRC-CRLF und „Rollen“. Die Findings S5, S6, U5 und U6 haben ihre eigenen Matrizen ([api-token-auth](api-token-auth.md), [api-error-mapping](api-error-mapping.md), [tui-stream-reconnect](tui-stream-reconnect.md)).

| Test | Datei | Deckt ab |
|---|---|---|
| `TestIRCSessionSend` (überarbeitet) | `internal/twitch/irc_test.go` | T14: der zweite Send-Bucket (1 Nachricht in 3 s pro Channel, B18) hält die zweite Split-Hälfte um 3 s; der Split und das 20/30-s-Bucket bleiben wie vorher. |
| `TestIRCSessionSendReply` | `internal/twitch/irc_test.go` | T5: `SendReply` schreibt `PRIVMSG #<login> :@reply-parent-msg-id=<id> <text>`; ohne Verbindung ein Fehler. |
| `TestIRCSendSanitizesLineEndings` | `internal/twitch/irc_test.go` | T14/IRC-CRLF: CR und LF im Text werden zu Leerzeichen, die Nachricht bleibt eine Zeile des Protokolls (kein eingeschleuster zweiter Befehl). |
| `TestIRCSessionTagUnescape` | `internal/twitch/irc_test.go` | T14: die Escapes der Tag-Werte werden unescaped (`display-name=A\:lice` → `A:lice`). |
| `TestIRCTokenErrorRetries` | `internal/twitch/irc_test.go` | T13: ein fehlendes Token wird geloggt und wie ein Fehlschlag wiederholt (Backoff), die nächste Verbindung verbindet; `Run` gibt den Fehler nicht zurück. |
| `TestRunSessionReconnectDeliversOldSessionEvents` | `internal/twitch/ws_test.go` | T7: ein Event der alten Session, das während des ausstehenden neuen Welcomes ankommt, wird geliefert, nicht verloren. |
| `TestRunLoopCleansUpTheReader` | `internal/twitch/ws_test.go` | T7: die Reader-Goroutine endet mit dem Loop — nach dem Cancel kehrt die Goroutine-Zahl auf den Stand vor dem Cancel zurück. |
| `TestMapModerate` (erweitert) | `internal/twitch/map_test.go` | T8: die sechs Aktionen `unban`, `untimeout`, `mod`, `unmod`, `vip`, `unvip` mappen auf die neuen neutralen Typen; eine Aktion ohne Ziel (`clear`) wird verworfen, kein Ereignis. |
| `TestMapCheer` (erweitert) | `internal/twitch/map_test.go` | T10: ein anonymer Cheer trägt keinen Nutzer, nur die Bits und das `anonymous`-Flag. |
| `TestShapes` (erweitert) | `internal/domain/eventtype/shape_test.go` | T8: die sechs neuen neutralen Typen stehen in der Nutzlast-Tabelle mit der Form `user`. |
| `TestMutedChatDeleteHoldsNoLock` | `internal/eventservice/chatpipeline_test.go` | T9: das Löschen des stummen Chats läuft ohne den Service-Mutex — während es blockiert, läuft ein Join durch. |
| `TestModerationCheckHoldsNoLock` | `internal/eventservice/chatpipeline_test.go` | T9: `Moderation.Check` läuft ohne den Service-Mutex — während es blockiert, läuft ein Join durch; die blockierte Nachricht wird verworfen, wenn der Check es sagt. |
| `TestAdditionalChannelRolesStayLocal` | `internal/eventservice/chatpipeline_test.go` | „Rollen“ (B24): Badges eines Zusatzkanals landen nicht in der je-Plattform-Identität — nicht bei einem bestehenden, nicht bei einem neuen Account; dieselben Badges aus dem Heimkanal schon. |
| `TestParticipationFollowDaysPlatformFallback` | `internal/moderation/moderation_test.go` | T11: die Follow-Zeit der Teilnahmeregel kommt aus dem Cache des Heimkanals (wenn vorhanden), sonst von der Plattform; ein Zusatzkanal fragt immer live; eine unbeantwortbare Frage erfüllt die Regel nicht (fail-closed). |

Nicht abgedeckt: T6 (Stream-State nach EventSub-Reconnect) ist durch `TestPlatformStartsWithoutToken` abgesichert (genau eine Meldung pro Verbindung, keine Doppelung durch den Reconnect-Welcome) und sonst nur im Echtbetrieb beobachtbar; T15 (Mutex von `Manager.Reconcile`) hat keinen deterministischen Parallel-Fall, der das Fehlverhalten ohne den Mutex zuverlässig auslöst.
