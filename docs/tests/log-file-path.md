# Test-Matrix: log-file-path

`log_file` akzeptiert neben `true`/`false` einen Pfad: die JSON-Logdatei
wird an der angegebenen Stelle geschrieben (Flag `--log-file`, Env
`STREAMCREW_LOG_FILE`, Config-Key `log_file`). Die Einstellung gilt nur
beim Start (On-the-fly-Konfiguration, 6.5; Code-ADR-0021, Nachtrag
2026-10-24, Punkt 6): der Watcher meldet eine Änderung als Restart, der
Log-Apply behält die Datei-Einstellung des Starts.

## Erwartetes Verhalten

| Fall | Erwartet |
|---|---|
| `log_file: true` (Default) | Logdatei in `<data-dir>/logs/streamcrew.log` |
| `log_file: false` | keine Logdatei |
| `log_file: /pfad/zu/datei.log` | Logdatei an dem Pfad, das Default-Verzeichnis bleibt angetastet |
| YAML-Bool (`log_file: true`/`false`) | wird als Text `"true"`/`"false"` übernommen |
| Watcher: `log_file` auf einen Pfad geändert | „configuration changes apply after a restart“ (settings=log_file), Log-Apply bleibt ungerufen |
| Watcher: `log_file` geändert, daneben ein hot-Log-Setting | Log-Apply wird mit der Datei-Einstellung des Starts aufgerufen |

## Abgedeckte Tests

| Fall | Test | Datei |
|---|---|---|
| Default `File: "true"` in der aufgelösten Konfiguration | `TestParse…` (Log-Default-Assertion) | `internal/config/config_test.go` |
| `log_file: false` aus der Config-Datei | `TestParse…` (Log-Assertion) | `internal/config/config_test.go` |
| Custom-Pfad: Datei wird dort geschrieben, Default-Dir bleibt leer | `TestNewWritesTheLogFileAtTheConfiguredPath` | `internal/app/app_test.go` |
| Keine Datei bei `false` | `TestNewWithoutLogFile` | `internal/app/app_test.go` |
| Watcher: `log_file` ist Restart-Only, Log-Apply behält die Start-Einstellung | `TestWatcherReportsTheLogFileAsRestart` | `internal/config/watch_apply_test.go` |
