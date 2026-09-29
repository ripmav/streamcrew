# streamcrew

`streamcrew` ist der Codename eines Bot- und Automatisierungsdienstes für Livestreams: Chat-Commands, Events, Overlays, Economy und Spiele. Es ist eine Neuimplementierung des dokumentierten Verhaltens von Mix It Up in Go ([ADR-0001](docs/adr/0001-neuimplementierung-und-nutzung-des-originals.md)).

- Der Core läuft headless, vor allem auf dem Streaming-PC und wahlweise als Server-Anwendung ([ADR-0003](docs/adr/0003-betriebsmodi.md)).
- Frontends sprechen ausschließlich über die API mit ihm ([ADR-0010](docs/adr/0010-api-protokoll.md)): CLI/TUI, eine Desktop-App und eine Weboberfläche.
- Zum Start wird nur Twitch unterstützt ([ADR-0004](docs/adr/0004-plattformumfang-zum-start.md)).

## Status

Phase 1 (Fundament) ist umgesetzt: Toolchain, Linting, CI, Container-Image und das Skelett des Cores. `streamcrew serve` startet, meldet sich über `/healthz` und `/readyz` gesund und beendet sich sauber; fachliche Funktionen folgen ab Phase 2. Den Stand zeigt die [Roadmap](docs/roadmap.md).

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
```

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

## Container

```bash
docker build -t streamcrew .
docker run -d --name streamcrew -p 8740:8740 -v streamcrew-data:/data streamcrew
```

- Das Image enthält nur das statische Binary und die CA-Zertifikate (`scratch`, rund 16 MB) und läuft als Nutzer `65532`.
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

- `scripts/check.sh` führt die Checkliste aus Plan §11.1 in der festgelegten Reihenfolge aus: `go fix`, `gofmt`, `go vet`, golangci-lint, `govulncheck`, `go test`. `go fix` und `gofmt` schreiben Dateien um; den Diff vor dem Commit ansehen.
- Die CI (`.github/workflows/ci.yml`) wiederholt diese Prüfungen und ergänzt:
  - Tests mit Race-Detector
  - kurze Fuzz-Läufe
  - Lizenzprüfung der Abhängigkeiten
  - Cross-Builds für linux, windows und darwin (amd64, arm64)
  - Docker-Smoke-Test (`scripts/docker-smoke.sh`)
  - wöchentlich und auf Anforderung: Tests nativ unter Windows und macOS ([Code-ADR-0006](docs/adr/code/0006-teststrategie.md))
- `.github/workflows/docs.yml` prüft die internen Links aller Markdown-Dateien.
- Tests nutzen `testing` mit testify (`assert`, `require`) und `testing/synctest` für Zeitverhalten ([Code-ADR-0006](docs/adr/code/0006-teststrategie.md)).
- Wie im Repository gearbeitet wird (Branches, Commits, ADR-Abnahme, Herkunftsregeln), steht in [`CONTRIBUTING.md`](CONTRIBUTING.md).

### Aufbau

| Paket | Aufgabe |
|---|---|
| `cmd/streamcrew` | kong-CLI, Signale, Exit-Codes |
| `internal/app` | Composition Root: verdrahtet alles, Bereitschaft ([Code-ADR-0002](docs/adr/code/0002-dependency-injection.md)) |
| `internal/config` | Startkonfiguration, Konfigurationsdatei, Datenverzeichnis ([Code-ADR-0005](docs/adr/code/0005-konfiguration.md)) |
| `internal/logging` | slog-Handler, Level je Komponente, Rotation, Maskierung ([Code-ADR-0003](docs/adr/code/0003-fehler-und-logging.md)) |
| `internal/supervisor` | Runnables mit Neustart, Backoff und geordnetem Shutdown ([Code-ADR-0004](docs/adr/code/0004-nebenlaeufigkeit-und-supervisor.md)) |
| `internal/httpserver` | HTTP-Server mit `/healthz`, `/readyz`, pprof |
| `internal/doctor` | Prüfungen für `streamcrew doctor` |
| `internal/buildinfo` | Version aus den eingebetteten Build-Informationen |

## Abhängigkeiten

[Renovate](https://docs.renovatebot.com/) hält die Abhängigkeiten aktuell. Dependabot wird nicht verwendet. Renovate läuft als GitHub-App und liest `renovate.json` ([Code-ADR-0001](docs/adr/code/0001-go-toolchain-und-linting.md)).

- **Zeitplan:** Pull Requests kommen montags vor 6 Uhr (Europe/Berlin), ohne Limit, wie viele gleichzeitig offen sind. Die Gruppierung begrenzt sie ohnehin auf einen je Ökosystem plus dessen Major-Updates.
- **Ein Pull Request je Ökosystem**, damit sich jedes einzeln übernehmen oder zurückhalten lässt:

  | Scope und Label | Inhalt |
  |---|---|
  | `(go)` / `go` | alle Go-Module, dazu die `go`-Direktive in `go.mod`; danach läuft `go mod tidy` |
  | `(actions)` / `github-actions` | alle Actions, dazu die Werkzeugversionen in den Workflows (golangci-lint, `go-licenses`) |
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
