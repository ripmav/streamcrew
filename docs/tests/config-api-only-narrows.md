# Test-Matrix: Config-API erweitert keine Rechte

Issue: Code-Review 2026-10-24, V3/C14, entschieden im Nachtrag 2026-10-24
zu Code-ADR-0021 — `ConfigService/Update` darf die aktuell wirksame
Konfiguration nur einschränken, gemessen am aktuell wirksamen Stand (nicht
am Start); ein Dokument mit einer Erweiterung wird ganz abgelehnt und
nicht geschrieben, die Antwort (PermissionDenied) nennt die betroffenen
Schlüssel. `core.Run` lädt den File-Resolver (C14), `serve` gibt den
Bootstrap-Token-Wert weiter.

| Test | Datei | Deckt ab |
|---|---|---|
| `TestWatcherUpdateOnlyNarrows` | `internal/config/watch_narrow_test.go` | Punkt 1–3: neue `grant`-/`revoke`-Einträge, neue/umgezogene `file-root` (Unterverzeichnis ist Einschränkung), neue `outbound-allow`-Einträge, jeder `mode`-Wechsel (auch server → daemon), neue `cors-allow`-Origin, neuer `listen`-Host (anderer Port am selben Host und Unix-Sockets sind erlaubt), neuer `overlay-listen`-Host (anderer Port am selben Host ist erlaubt, Review 341-8c), `dev` einschalten; Ganz-oder-gar-nicht (Datei bleibt unverändert) und der Hinweis auf die Konfigurationsdatei in der Antwort. |
| `TestWatcherUpdateDeniesTLSRemovalBeyondLoopback` | `internal/config/watch_narrow_test.go` | Punkt 2: TLS entfernen, während der Bind über Loopback hinausreicht, ist eine Erweiterung; Zertifikate tauschen bleibt erlaubt. |
| `TestWatcherUpdateMeasuresAgainstTheCurrentState` | `internal/config/watch_narrow_test.go` | Punkt 1: Maßstab ist der aktuell wirksame Stand — was die Datei eingeschränkt hat (revoke), darf die API nicht wieder erweitern, obwohl der Start es hatte; der aktuelle Stand bleibt erlaubt. |
| `TestConfigUpdateMapsErrors` (narrow-Fall) | `internal/api/config_test.go` | Fehler-Mapping: `config.ErrNarrowOnly` wird auf PermissionDenied gemappt, die Schlüssel bleiben in der Nachricht. |
| `TestConfigServiceRejectsAnExtendingDocument` | `internal/app/configservice_test.go` | End-to-End im Server-Modus: ein erweiterndes Dokument wird mit PermissionDenied abgelehnt, die Datei bleibt unverändert, ein einschränkendes Dokument wird akzeptiert. |
| `TestRunWithConfigFileWiresTheWatcher` | `core/core_test.go` | C14: `core.Run` mit Config-Datei lädt den File-Resolver — `Get` meldet `live: true` und `log-file` in der Restart-Liste, `Update` akzeptiert und `serve`-Aufrufe erreichen eine geschützte API (Bootstrap-Token). |
| `TestWatcherReportsTheLogFileAsRestart` | `internal/config/watch_apply_test.go` | Nachtrag Punkt 6: `log-file` wechselt von hot zu restart, für die Datei wie für die API; der Log-Apply behält die Datei-Einstellung des Starts. |

Nicht abgedeckt: das Verhalten eines fiktiven Desktop-Aufrufs von
`core.Run` mit Config-Datei und gesetzten Flags (Präzedenz Flag > Datei im
`Load`-Pfad) — `Load` kennt keine Flags und markiert alle Schlüssel als
`fromFile`; der CLI-Weg setzt die Präzedenz über Kong.
