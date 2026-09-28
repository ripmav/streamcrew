# streamcrew

`streamcrew` ist der Codename eines Bot- und Automatisierungsdienstes für Livestreams: Chat-Commands, Events, Overlays, Economy und Spiele. Es ist eine Neuimplementierung des dokumentierten Verhaltens von Mix It Up in Go ([ADR-0001](docs/adr/0001-neuimplementierung-und-nutzung-des-originals.md)).

- Der Core läuft headless, vor allem auf dem Streaming-PC und wahlweise als Server-Anwendung ([ADR-0003](docs/adr/0003-betriebsmodi.md)).
- Frontends sprechen ausschließlich über die API mit ihm ([ADR-0010](docs/adr/0010-api-protokoll.md)): CLI/TUI, eine Desktop-App und eine Weboberfläche.
- Zum Start wird nur Twitch unterstützt ([ADR-0004](docs/adr/0004-plattformumfang-zum-start.md)).

## Status

Phase 1 (Fundament): Toolchain, Linting und CI stehen. Der Core hat noch keine Funktion; `cmd/streamcrew` ist ein Platzhalter bis zum Skelett aus Phase 1.3. Den Stand zeigt die [Roadmap](docs/roadmap.md).

Das Repository ist privat. Nur der Projektinhaber schaltet es öffentlich.

## Dokumentation

| Dokument | Inhalt |
|---|---|
| [`docs/plan.md`](docs/plan.md) | Architektur, Technologie-Stack, Konventionen, Risiken |
| [`docs/roadmap.md`](docs/roadmap.md) | Phasen, Aufgaben, Änderungshistorie |
| [`docs/adr/`](docs/adr/README.md) | Architekturentscheidungen |
| [`docs/adr/code/`](docs/adr/code/README.md) | Entscheidungen zum Code |
| [`docs/spec/`](docs/spec/README.md) | Verhaltensspezifikationen mit Quellennachweis |

## Entwicklung

Voraussetzungen ([Code-ADR-0001](docs/adr/code/0001-go-toolchain-und-linting.md)):

- Go in der Version aus `go.mod` (derzeit 1.27.1)
- [golangci-lint](https://golangci-lint.run/) v2 in der neuesten Version

```bash
go build ./...          # bauen
go test ./...           # testen
scripts/check.sh        # Pre-Commit-Checkliste, vor jedem Commit
scripts/fuzz.sh         # alle Fuzz-Ziele kurz laufen lassen (FUZZTIME, Standard 30s)
```

- `scripts/check.sh` führt die Checkliste aus Plan §11.1 in der festgelegten Reihenfolge aus: `go fix`, `gofmt`, `go vet`, golangci-lint, `govulncheck`, `go test`. `go fix` und `gofmt` schreiben Dateien um; den Diff vor dem Commit ansehen.
- Die CI (`.github/workflows/ci.yml`) wiederholt diese Prüfungen und ergänzt:
  - Tests mit Race-Detector
  - kurze Fuzz-Läufe
  - Lizenzprüfung der Abhängigkeiten
  - Cross-Builds für linux, windows und darwin (amd64, arm64)
- `.github/workflows/docs.yml` prüft die internen Links aller Markdown-Dateien.
- Weitere Konventionen stehen in Plan §11.1: Branches, Commits, Herkunftsregeln, ADR-Prozess.

## Abhängigkeiten

[Renovate](https://docs.renovatebot.com/) hält die Abhängigkeiten aktuell. Dependabot wird nicht verwendet. Renovate läuft als GitHub-App und liest `renovate.json` ([Code-ADR-0001](docs/adr/code/0001-go-toolchain-und-linting.md)).

- **Zeitplan:** Pull Requests kommen montags vor 6 Uhr (Europe/Berlin), ohne Limit, wie viele gleichzeitig offen sind. Die Gruppierung begrenzt sie ohnehin auf einen je Ökosystem plus dessen Major-Updates.
- **Ein Pull Request je Ökosystem**, damit sich jedes einzeln übernehmen oder zurückhalten lässt:

  | Scope und Label | Inhalt |
  |---|---|
  | `(go)` / `go` | alle Go-Module, dazu die `go`-Direktive in `go.mod`; danach läuft `go mod tidy` |
  | `(actions)` / `github-actions` | alle Actions, dazu die Werkzeugversionen in den Workflows (golangci-lint, `go-licenses`) |
  | `(docker)` / `docker` | Basis-Images, sobald es ein Dockerfile gibt |

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

[MIT](LICENSE) ([ADR-0002](docs/adr/0002-lizenz-des-projekts.md)). Jede Quelldatei beginnt mit `SPDX-License-Identifier: MIT`.
