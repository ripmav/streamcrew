# streamcrew

`streamcrew` ist der Codename eines Bot- und Automatisierungsdienstes für Livestreams: Chat-Commands, Events, Overlays, Economy und Spiele. Es ist eine Neuimplementierung des dokumentierten Verhaltens von Mix It Up in Go ([ADR-0001](docs/adr/0001-neuimplementierung-und-nutzung-des-originals.md)).

- Der Core läuft headless, vor allem auf dem Streaming-PC und wahlweise als Server-Anwendung ([ADR-0003](docs/adr/0003-betriebsmodi.md)).
- Frontends sprechen ausschließlich über die API mit ihm ([ADR-0010](docs/adr/0010-api-protokoll.md)): CLI/TUI, eine Desktop-App und eine Weboberfläche.
- Zum Start wird nur Twitch unterstützt ([ADR-0004](docs/adr/0004-plattformumfang-zum-start.md)).

## Status

Phase 1 (Fundament) ist abgeschlossen. In Phase 2 stehen Speicher, Profile, Backups, Event-Bus und die Verschlüsselung der Secrets; das Domänenmodell (Nutzer, Rollen, Commands) folgt nach seinen Spezifikationen. `streamcrew serve` startet mit dem aktiven Profil, meldet sich über `/healthz` und `/readyz` gesund und beendet sich sauber. Den Stand zeigt die [Roadmap](docs/roadmap.md).

Das Repository ist privat. Nur der Projektinhaber schaltet es öffentlich.

## Dokumentation

| Dokument | Inhalt |
|---|---|
| [`docs/plan.md`](docs/plan.md) | Architektur, Technologie-Stack, Konventionen, Risiken |
| [`docs/roadmap.md`](docs/roadmap.md) | Phasen, Aufgaben, Änderungshistorie |
| [`docs/adr/`](docs/adr/README.md) | Architekturentscheidungen |
| [`docs/adr/code/`](docs/adr/code/README.md) | Entscheidungen zum Code |
| [`docs/spec/`](docs/spec/README.md) | Verhaltensspezifikationen mit Quellennachweis |

## Bedienung

```bash
streamcrew serve                  # Core starten; Strg+C oder SIGTERM beendet ihn geordnet
streamcrew version [-o json]      # Version, Commit, Go-Version, Plattform
streamcrew config show [-o json]  # wirksame Konfiguration im Format der Konfigurationsdatei
streamcrew config path [-o json]  # Konfigurationsdatei, Daten- und Log-Verzeichnis
streamcrew doctor [-o json]       # Umgebung prüfen: Datenverzeichnis, Adresse, Zeitzonen

streamcrew profile list [-o json]              # Profile; * markiert das aktive
streamcrew profile create <name> [--id <id>]   # Profil anlegen; die ID folgt sonst aus dem Namen
streamcrew profile rename <id> <name>          # Anzeigenamen ändern; die ID bleibt
streamcrew profile use <id>                    # aktives Profil festlegen
streamcrew profile delete <id> --yes           # löschen; vorher entsteht ein Backup

streamcrew backup create                       # Backup des Profils, auch bei laufendem Core
streamcrew backup list [-o json]               # Backups des Profils, neueste zuerst
streamcrew backup restore <datei.zip> --yes    # zurückspielen; vorher wird der aktuelle Stand gesichert

streamcrew secret rotate                       # neuen Schlüssel erzeugen, Tokens aller Profile neu verschlüsseln
```

- `profile …` (außer `list`), `backup restore` und `secret rotate` brauchen einen gestoppten Core. Sie nehmen dieselbe Sperre wie `serve` und brechen sonst mit einem Hinweis ab ([ADR-0012](docs/adr/0012-persistenz.md)).
- `backup …` wirkt auf das aktive Profil oder auf `--profile <id>`.

Exit-Codes: `0` Erfolg, `1` Fehler, `2` ungültige Kommandozeile oder Konfiguration.

Der HTTP-Server lauscht standardmäßig auf `127.0.0.1:8740`, im Server-Modus auf `:8740`:

| Pfad | Bedeutung |
|---|---|
| `/healthz` | Der Prozess läuft (Liveness). |
| `/readyz` | Alle Komponenten laufen, kein Shutdown im Gange (Readiness). |
| `/debug/pprof/` | nur mit `--dev` und nur auf einer Loopback-Adresse |

## Konfiguration

Die Startkonfiguration kommt aus Flags, Umgebungsvariablen und einer optionalen YAML-Datei, in dieser Rangfolge ([Code-ADR-0005](docs/adr/code/0005-konfiguration.md)). `streamcrew --help` listet alle Einstellungen.

| Flag | Umgebungsvariable | Schlüssel in der Datei | Standard |
|---|---|---|---|
| `--config` | `STREAMCREW_CONFIG` | – | `config.yaml` im Standard-Datenverzeichnis, falls vorhanden |
| `--data-dir` | `STREAMCREW_DATA_DIR` | `data_dir` | `os.UserConfigDir()/streamcrew`, z. B. `~/.config/streamcrew` |
| `--profile` | `STREAMCREW_PROFILE` | `profile` | das aktive Profil (`profile use`); beim ersten Start `default` |
| `--mode` | `STREAMCREW_MODE` | `mode` | `daemon`; außerdem `desktop` und `server` ([ADR-0003](docs/adr/0003-betriebsmodi.md)) |
| `--listen` | `STREAMCREW_LISTEN` | `listen` | `127.0.0.1:8740`, im Server-Modus `:8740` |
| `--dev` | `STREAMCREW_DEV` | `dev` | aus |
| `--shutdown-timeout` | `STREAMCREW_SHUTDOWN_TIMEOUT` | `shutdown_timeout` | `15s` |
| `--log-level` | `STREAMCREW_LOG_LEVEL` | `log_level` | `info` |
| `--log-component-level` | `STREAMCREW_LOG_COMPONENT_LEVEL` | `log_component_level` | – |
| `--log-format` | `STREAMCREW_LOG_FORMAT` | `log_format` | `text` |
| `--[no-]log-file` | `STREAMCREW_LOG_FILE` | `log_file` | an: `<data-dir>/logs/streamcrew.log` (JSON Lines) |
| `--log-max-size`, `--log-max-files` | `STREAMCREW_LOG_MAX_SIZE`, `…_MAX_FILES` | `log_max_size`, `log_max_files` | 10 MiB, 5 Dateien |

Beispiel für `config.yaml`; die Ausgabe von `streamcrew config show` hat dasselbe Format:

```yaml
mode: daemon
log_level: info
log_component_level:
  supervisor: debug
```

- Unbekannte Schlüssel in der Datei sind ein Fehler.
- **Portabler Modus:** Liegt neben dem Binary eine Datei `streamcrew.portable`, liegen die Daten in `streamcrew-data` neben dem Binary.
- Secrets werden in Logs maskiert ([Code-ADR-0003](docs/adr/code/0003-fehler-und-logging.md)).
- Einstellungen, die während des Betriebs änderbar sind (Backup-Zeitplan, Zeitzone), liegen im Profil, nicht in der Startkonfiguration.

### Datenverzeichnis

| Pfad | Inhalt |
|---|---|
| `profiles/<id>.db` | SQLite-Datenbank je Profil ([ADR-0012](docs/adr/0012-persistenz.md)) |
| `profiles/active` | ID des aktiven Profils |
| `backups/<id>-<Zeit>.zip` | Backups mit `profile.db` und `manifest.json`; automatisch täglich um 04:00 in der Zeitzone des Profils, behalten werden 7 tägliche, 4 wöchentliche und 12 monatliche |
| `logs/` | Log-Dateien (JSON Lines) |
| `streamcrew.lock` | Sperre gegen einen zweiten Core auf demselben Verzeichnis |
| `secret.key` | Schlüssel für die Tokens, nur ohne Schlüsselbund des Systems und ohne `STREAMCREW_SECRET_KEY` |

**Tokens** liegen verschlüsselt (AES-256-GCM) in der Profildatenbank. Den Schlüssel sucht der Core in dieser Reihenfolge: `STREAMCREW_SECRET_KEY` (32 Byte in Base64), der Schlüsselbund des Systems, `secret.key`. Backups enthalten den Schlüssel nicht; für einen Umzug auf einen anderen Rechner wird er mitgenommen oder die Anmeldungen erfolgen neu.

## Container

```bash
docker build -t streamcrew .
docker run -d --name streamcrew -p 8740:8740 -v streamcrew-data:/data streamcrew
```

- Das Image enthält nur das statische Binary und die CA-Zertifikate (`scratch`, rund 25 MB) und läuft als Nutzer `65532`.
- Im Container gibt es keinen Schlüsselbund. Ohne `STREAMCREW_SECRET_KEY` legt der Core den Schlüssel als `/data/secret.key` ins selbe Volume wie die Daten; wer das Volume sichert, sichert dann den Schlüssel mit. Empfohlen: den Schlüssel über `-e STREAMCREW_SECRET_KEY=…` oder ein Secret des Orchestrierers übergeben (`openssl rand -base64 32`).
- Voreingestellt sind der Server-Modus, das Datenverzeichnis `/data` (Volume), JSON-Logs auf stderr und kein Datei-Log. Ein eingebundenes Host-Verzeichnis muss für den Nutzer `65532` beschreibbar sein.
- Für Orchestrierer eignen sich `/healthz` als Liveness- und `/readyz` als Readiness-Probe. Einen `HEALTHCHECK` im Image gibt es nicht, weil `scratch` kein Werkzeug für HTTP-Abfragen enthält.

## Entwicklung

Voraussetzungen ([Code-ADR-0001](docs/adr/code/0001-go-toolchain-und-linting.md)):

- Go in der Version aus `go.mod` (derzeit 1.27.1)
- [golangci-lint](https://golangci-lint.run/) v2 in der neuesten Version

```bash
go build ./...          # bauen
go test ./...           # testen
scripts/check.sh        # Pre-Commit-Checkliste, vor jedem Commit
scripts/fuzz.sh         # alle Fuzz-Ziele kurz laufen lassen (FUZZTIME, Standard 30s)
scripts/docker-smoke.sh # Image bauen und prüfen; DOCKER_BUILD_ARGS="--network host", falls Build-Container kein Netz haben
```

- Nach Änderungen an SQL in `internal/store/queries` oder `internal/store/migrations`: `go generate ./internal/store/...` (sqlc, per `go run` gepinnt). Die CI prüft mit `sqlc diff`, dass der generierte Code passt.
- `scripts/check.sh` führt die Checkliste aus Plan §11.1 in der festgelegten Reihenfolge aus: `go fix`, `gofmt`, `go vet`, golangci-lint, `govulncheck`, `go test`. `go fix` und `gofmt` schreiben Dateien um; den Diff vor dem Commit ansehen.
- Die CI (`.github/workflows/ci.yml`) wiederholt diese Prüfungen und ergänzt:
  - Tests mit Race-Detector
  - kurze Fuzz-Läufe
  - Lizenzprüfung der Abhängigkeiten
  - Cross-Builds für linux, windows und darwin (amd64, arm64)
  - Docker-Smoke-Test (`scripts/docker-smoke.sh`)
  - wöchentlich und auf Anforderung: Tests nativ unter Windows und macOS ([Code-ADR-0006](docs/adr/code/0006-teststrategie.md))
  - Jeder Job hat einen eigenen Go-Cache (Module und Build-Ausgaben), damit Race-, Fuzz- und Cross-Builds die Abhängigkeiten nicht bei jedem Lauf neu kompilieren.
- `.github/workflows/docs.yml` prüft die internen Links aller Markdown-Dateien.
- Tests nutzen `testing` mit testify (`assert`, `require`) und `testing/synctest` für Zeitverhalten ([Code-ADR-0006](docs/adr/code/0006-teststrategie.md)).
- Wie im Repository gearbeitet wird (Branches, Commits, ADR-Abnahme, Herkunftsregeln), steht in [`CONTRIBUTING.md`](CONTRIBUTING.md).

### Aufbau

| Paket | Aufgabe |
|---|---|
| `cmd/streamcrew` | nur `main.go`: Signale, Umgebung, kong-Initialisierung, Parsen, Exit-Code |
| `internal/cli` | Definition der Kommandozeile (`cli.Root`) mit allen Unterkommandos, Fehler und Exit-Codes |
| `internal/app` | Composition Root: verdrahtet alles, Bereitschaft ([Code-ADR-0002](docs/adr/code/0002-dependency-injection.md)) |
| `internal/config` | Startkonfiguration, Konfigurationsdatei, Datenverzeichnis ([Code-ADR-0005](docs/adr/code/0005-konfiguration.md)) |
| `internal/logging` | slog-Handler, Level je Komponente, Rotation, Maskierung ([Code-ADR-0003](docs/adr/code/0003-fehler-und-logging.md)) |
| `internal/supervisor` | Runnables mit Neustart, Backoff und geordnetem Shutdown ([Code-ADR-0004](docs/adr/code/0004-nebenlaeufigkeit-und-supervisor.md)) |
| `internal/httpserver` | HTTP-Server mit `/healthz`, `/readyz`, pprof |
| `internal/doctor` | Prüfungen für `streamcrew doctor` |
| `internal/buildinfo` | Version aus den eingebetteten Build-Informationen |
| `internal/domain/id` | IDs als UUIDv7 aus der Standardbibliothek ([Code-ADR-0009](docs/adr/code/0009-ids-und-zeit.md)) |
| `internal/domain/eventtype`, `internal/domain/platform` | Katalog der fachlichen Ereignistypen, Namen der Plattformen ([Spezifikation](docs/spec/events.md)) |
| `internal/domain/role` | Rollen mit Rangordnung und „erfüllt Mindestrolle“ ([Spezifikation](docs/spec/users-and-roles.md)) |
| `internal/domain/command` | Commands: Arten, Trigger, Gruppen, Anforderungen, Actions als polymorphe Dokumente ([Spezifikation](docs/spec/commands.md)) |
| `internal/domain/user` | Nutzer mit Plattform-Identitäten und Statistiken ([Spezifikation](docs/spec/users-and-roles.md)) |
| `internal/domain/counter`, `internal/domain/quote` | Counter und Quotes ([Spezifikation](docs/spec/counters-and-quotes.md)) |
| `internal/expr` | Ausdrücke mit `$`-Identifiern für Rechnungen und Bedingungen, mit `expr-lang/expr` ([Spezifikation](docs/spec/template.md), [Code-ADR-0012](docs/adr/code/0012-template-engine.md)) |
| `internal/template` | Templates mit `$`-Identifiern: Tokenizer, Präfixbaum, Quellen, Cache je Rendervorgang, Kodierung und die Identifier-Familien ([Spezifikation](docs/spec/template.md), [Code-ADR-0012](docs/adr/code/0012-template-engine.md)) |
| `internal/engine` | Command-Engine: Instanzen, Auslösen mit Anforderungen, Warteschlange mit Sperrmodi, Pause, Aufrufe, Abbrechen, Wiederholen, Verlauf und Ereignisse; Kind-Actions, Schalter „aktiv“, Zeitlimits und Capabilities der Actions ([Spezifikation](docs/spec/command-engine.md), [Code-ADR-0013](docs/adr/code/0013-typ-registry.md)) |
| `internal/action` | Typ-Registry der Actions: Descriptors, Kategorien, JSON-Schemas mit UI-Hinweisen (`internal/action/schema`), gemeinsame Feldtypen wie Templates und Mengenangaben, Konformitätstest (`internal/action/actiontest`) ([Spezifikation](docs/spec/actions.md), [Code-ADR-0013](docs/adr/code/0013-typ-registry.md)) |
| `internal/action/flow` | Actions für den Ablauf: Warten, Zufall, Gruppe, Wiederholen ([Spezifikation](docs/spec/actions.md), B10–B15) |
| `internal/capability` | Capabilities nach dem Sicherheitsmodell ([ADR-0013](docs/adr/0013-sicherheitsmodell.md)) |
| `internal/event` | Ereignisse, Katalog und nicht blockierender Event-Bus ([Code-ADR-0011](docs/adr/code/0011-event-bus.md)) |
| `internal/polydoc` | polymorphe JSON-Dokumente mit Typ, Version und Migrationen, auch verschachtelt, auf `encoding/json/v2` ([Code-ADR-0010](docs/adr/code/0010-polymorphe-serialisierung.md), [Code-ADR-0018](docs/adr/code/0018-json-v2.md), [Code-ADR-0013](docs/adr/code/0013-typ-registry.md)) |
| `internal/store` | SQLite je Profil, goose-Migrationen, sqlc-Abfragen und die Repositories der Domänenpakete ([Code-ADR-0008](docs/adr/code/0008-datenbankzugriff.md)) |
| `internal/profile`, `internal/lockfile` | Profile und die Sperre des Datenverzeichnisses ([ADR-0012](docs/adr/0012-persistenz.md)) |
| `internal/settings` | typisierte Einstellungen je Profil |
| `internal/backup` | Backups, Aufbewahrung, Zeitplan, Restore |
| `internal/vault` | verschlüsselte Secrets und ihr Schlüssel |

## Abhängigkeiten

[Renovate](https://docs.renovatebot.com/) hält die Abhängigkeiten aktuell. Dependabot wird nicht verwendet. Renovate läuft als GitHub-App und liest `renovate.json` ([Code-ADR-0001](docs/adr/code/0001-go-toolchain-und-linting.md)).

- **Zeitplan:** Pull Requests kommen montags vor 6 Uhr (Europe/Berlin), ohne Limit, wie viele gleichzeitig offen sind. Die Gruppierung begrenzt sie ohnehin auf einen je Ökosystem plus dessen Major-Updates.
- **Ein Pull Request je Ökosystem**, damit sich jedes einzeln übernehmen oder zurückhalten lässt:

  | Scope und Label | Inhalt |
  |---|---|
  | `(go)` / `go` | alle Go-Module, dazu die `go`-Direktive in `go.mod`; danach läuft `go mod tidy` |
  | `(actions)` / `github-actions` | alle Actions, dazu die Werkzeugversionen in Workflows und `go:generate`-Zeilen (golangci-lint, `go-licenses`, sqlc) |
  | `(docker)` / `docker` | Basis-Images im `Dockerfile`, per Digest gepinnt |

  Beispiel für einen Titel: `fix(go): update go modules`. Jeder Pull Request trägt zusätzlich das Label `dependencies`.
- **Major-Updates** kommen als zweiter Pull Request ihres Ökosystems (`renovate/major-…`), weil sie Codeänderungen brauchen können.
- **Commit-Typen:** Updates von Go-Modulen sind `fix:`. Pins, Action-Updates und die `go`-Direktive sind `chore:` (Voreinstellung von `config:recommended`).
- **Dependency Dashboard:** Das Issue listet alles, was Renovate kennt. Ein Häkchen dort öffnet den Pull Request sofort, statt bis Montag zu warten.
- **Logs:** Die Protokolle der Läufe liegen auf [developer.mend.io](https://developer.mend.io), nicht unter Actions.

**Sicherheitsupdates** warten nicht auf Montag. Renovate prüft bei jedem Lauf alle Abhängigkeiten gegen die [OSV](https://osv.dev)-Datenbank; die App läuft mehrmals täglich. Gibt es eine korrigierte Version, öffnet Renovate sofort einen Pull Request mit dem Label `security`. Das ersetzt `govulncheck` nicht, das zusätzlich erkennt, ob der verwundbare Code überhaupt aufgerufen wird, und die Standardbibliothek abdeckt.

**Gepinnte Actions:** Actions sind auf Commit-SHAs gepinnt, mit der lesbaren Version daneben (`actions/checkout@<sha> # v7.0.1`). Ein Tag lässt sich auf anderen Code umhängen, ein Digest nicht. Renovate hebt die Digests an.

Ein Update, das `ci.yml` ändert, startet die CI von selbst. Änderungen nur an anderen Workflows lassen sich von Hand prüfen:

    gh workflow run ci.yml --ref <renovate-branch>

## Lizenz

[Apache-2.0](LICENSE) ([ADR-0002](docs/adr/0002-lizenz-des-projekts.md)). Jede Quelldatei beginnt mit `SPDX-License-Identifier: Apache-2.0`.
