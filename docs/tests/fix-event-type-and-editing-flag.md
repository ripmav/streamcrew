# Test-Matrix: Stream-Event-Typ und $EDITOR-Status

Issue: Folge-PR zu Phase 6.4.3 (Stack auf PR #326) — zwei bei der
teatest-Migration gefundene Produkt-Bugs: `convertEvent`
(`internal/app/api_stream.go`) setzte `Event.Type` nie, und die
`editing`-Flags der TUI wurden auf einem Value-Receiver-Copy gesetzt und
nie zurückgesetzt.

| Test | Datei | Deckt ab |
|---|---|---|
| `TestConvertEventCarriesTheProtocolType` | `internal/app/api_stream_internal_test.go` | Jede konvertierte Stream-Event-Zone trägt ihren Protokolltypen (stream.proto): `chat.message`, `feed`, `command.instance`, `prompt`, `log`; nicht gelieferte Typen (z. B. `chat.user.join`) bleiben `nil`. |
| `TestCommandsViewFetchesFiltersAndEdits/edit` | `internal/tui/views_test.go` | Wie vorher der no-op-Editor-Flow, zusätzlich: das `editing`-Flag wird nach dem `actionDoneMsg` wieder zurückgesetzt (final `false`). |
| `TestCommandsViewFetchesFiltersAndEdits/edit status` | `internal/tui/views_test.go` | Blockierender Editor (`yes`, wird vom 5-s-Editor-Kontext gekillt): während der Editor offen ist, zeigt die Commands-View „editing in $EDITOR …“; beim Quit ist das Flag noch `true` (der Editor läuft noch). |
| `waitForLogEvent` (Helfer) | `internal/tui/teatest_helper_test.go` | Erwartet jetzt `Event.Type == "log"` statt nur den Payload — alle TUI-Stream-Tests (Chat, Queue, Logs, Users) hängen am gefixten Typ. |

Nicht abgedeckt: der `platform.status`-Typen hat im Core noch keine
Quelle (der TUI-Zweig bleibt bis dahin unerreichbar, ist aber per Proto
dokumentiert); der Settings-`editing`-Flag (dieselbe Value-Receiver-Fix in
`editConfig` + Reset im `configUpdatedMsg`) hat keinen eigenen
Render-Test, weil die Settings-View die Zeile schon renderte und der
Commands-Test denselben Mechanismus abdeckt.
