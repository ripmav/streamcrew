# Test-Matrix: log-file-path

`log_file` akzeptiert neben `true`/`false` einen Pfad: die JSON-Logdatei
wird an der angegebenen Stelle geschrieben (Flag `--log-file`, Env
`STREAMCREW_LOG_FILE`, Config-Key `log_file`). Die Einstellung bleibt hot
(On-the-fly-Konfiguration, 6.5): der Watcher händigt den Wert an den
Log-Apply weiter.

## Erwartetes Verhalten

| Fall | Erwartet |
|---|---|
| `log_file: true` (Default) | Logdatei in `<data-dir>/logs/streamcrew.log` |
| `log_file: false` | keine Logdatei |
| `log_file: /pfad/zu/datei.log` | Logdatei an dem Pfad, das Default-Verzeichnis bleibt angetastet |
| YAML-Bool (`log_file: true`/`false`) | wird als Text `"true"`/`"false"` übernommen |
| Watcher: `log_file` auf einen Pfad geändert | Log-Apply wird mit dem Pfad aufgerufen (hot) |
| Watcher: `log_file: false` | Log-Apply wird mit `"false"` aufgerufen |

## Abgedeckte Tests

| Fall | Test | Datei |
|---|---|---|
| Default `File: "true"` in der aufgelösten Konfiguration | `TestParse…` (Log-Default-Assertion) | `internal/config/config_test.go` |
| `log_file: false` aus der Config-Datei | `TestParse…` (Log-Assertion) | `internal/config/config_test.go` |
| Custom-Pfad: Datei wird dort geschrieben, Default-Dir bleibt leer | `TestNewWritesTheLogFileAtTheConfiguredPath` | `internal/app/app_test.go` |
| Keine Datei bei `false` | `TestNewWithoutLogFile` | `internal/app/app_test.go` |
| Watcher: Pfad und Bool erreichen den Log-Apply | `TestWatcherAppliesTheLogFile` | `internal/config/watch_apply_test.go` |
