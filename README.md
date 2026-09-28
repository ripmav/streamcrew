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

## Lizenz

[Apache-2.0](LICENSE) ([ADR-0002](docs/adr/0002-lizenz-des-projekts.md)). Jede Quelldatei beginnt mit `SPDX-License-Identifier: Apache-2.0`.
