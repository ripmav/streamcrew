# Mitarbeit an streamcrew

Dieses Dokument fasst zusammen, wie im Repository gearbeitet wird. Die verbindlichen Grundlagen stehen in [Plan §11](docs/plan.md#11-qualität-und-entwicklungsprozess) und in den ADRs ([`docs/adr/`](docs/adr/README.md)); bei Widersprüchen gelten diese.

Das Repository ist privat. Nur der Projektinhaber schaltet es öffentlich ([ADR-0009](docs/adr/0009-repositories-und-hosting.md)). Die Beitragsregeln für externe Mitwirkende (DCO oder CLA) werden erst vor der Veröffentlichung festgelegt (Roadmap, Gate O).

## Sprache

- Dokumentation (`README.md`, `docs/`, ADRs, Spezifikationen) ist auf Deutsch.
- Code, Code-Kommentare, Log-Meldungen, CLI-Texte, Commit-Nachrichten und Branch-Namen sind auf Englisch.

## Branches und Pull Requests

- **Nie direkt auf `main` arbeiten.** Jede Arbeitssitzung hat einen eigenen Branch nach dem Muster `<präfix>/<kurzbeschreibung>`:
  - Präfixe nach Conventional Commits: `feat`, `fix`, `chore`, `refactor`, `docs`, `test`, `ci`, `perf`, `build`
  - Kurzbeschreibung klein, mit Bindestrichen; mit Issue: `<präfix>/<issue-nummer>-<kurzbeschreibung>`
  - Beispiele: `feat/twitch-eventsub`, `fix/supervisor-backoff`, `docs/close-phase-0-gate`
- **Merge nur per Pull Request.** Nach dem Merge löscht GitHub den Branch automatisch.
- **Fixes zu einem bestehenden Pull Request** kommen auf dessen Branch, auch bei Renovate-PRs. Danach steht im PR ein Kommentar, was falsch war und was die Korrektur ändert.
- **Branch-Schutz:** Für private Repositories ist er im aktuellen GitHub-Plan nicht verfügbar. Die Regel gilt deshalb per Konvention. Optional verhindert ein lokaler Hook Pushes auf `main`:

  ```bash
  git config core.hooksPath scripts/hooks
  ```

- **KI-Review:** `@claude` in einem Pull Request startet ein Review (Plan §11.3). Es gibt kein automatisches Review.

## Commits

- Nachrichten nach [Conventional Commits](https://www.conventionalcommits.org/), z. B. `feat(config): add portable mode`.
- Mit KI-Unterstützung entstandene Commits und Pull Requests tragen im Footer das verwendete Modell und Werkzeug:

  ```text
  Assisted-by: <Modell> (<Effort>) via <Werkzeug>
  ```

## Vor jedem Commit

```bash
scripts/check.sh
```

Das Skript führt die Pre-Commit-Checkliste aus Plan §11.1 in der festgelegten Reihenfolge aus: `go fix`, `gofmt`, `go vet`, golangci-lint, `govulncheck`, `go test` ([Code-ADR-0001](docs/adr/code/0001-go-toolchain-und-linting.md)).

- `go fix` und `gofmt` schreiben Dateien um. Den Diff vor dem Commit ansehen.
- Befunde werden behoben, nicht unterdrückt: kein `//nolint` und keine Lockerung von `.golangci.yml` ohne Freigabe des Projektinhabers.
- `govulncheck`-Befunde ohne korrigierte Version werden dem Projektinhaber gemeldet.
- Wer das Dockerfile ändert, baut und testet das Image vorher: `scripts/docker-smoke.sh`.

## Werkzeuge

- **Go** in der Version aus `go.mod` (stets die neueste stabile Version) und **golangci-lint** v2 in der neuesten Version ([Code-ADR-0001](docs/adr/code/0001-go-toolchain-und-linting.md)).
- **Private Module:** Die Repositories unter `github.com/ripmav` sind privat. Damit Go sie nicht über den öffentlichen Proxy und die Prüfsummen-Datenbank sucht, gilt lokal und in der CI ([ADR-0009](docs/adr/0009-repositories-und-hosting.md)):

  ```bash
  go env -w GOPRIVATE='github.com/ripmav/*'
  ```

- **Docker** für das Container-Image und den Smoke-Test.

## Code

- **Lizenz-Header:** Jede Quelldatei beginnt mit `// SPDX-License-Identifier: MIT` bzw. dem Kommentarformat ihrer Sprache ([ADR-0002](docs/adr/0002-lizenz-des-projekts.md)). Für Go-Dateien prüft das `goheader`.
- **Konventionen** für Go stehen in Plan §11.1 und in den Code-ADRs ([`docs/adr/code/`](docs/adr/code/README.md)): Verdrahtung, Fehler und Logging, Nebenläufigkeit, Konfiguration, Tests.
- **Abhängigkeiten:** Standardbibliothek zuerst, dann `golang.org/x/…`. Jede neue Drittabhängigkeit braucht eine Begründung im Pull Request und eine MIT-kompatible Lizenz; die CI prüft das mit einer Allowlist.
- **Datenbank:** Schemaänderungen kommen nur als neue Migration in `internal/store/migrations`, mit Up- und Down-Teil. Danach `go generate ./internal/store/...`; der generierte sqlc-Code wird eingecheckt ([Code-ADR-0008](docs/adr/code/0008-datenbankzugriff.md)).
- **Tests:** Neue Funktionen kommen nur mit Tests: `testing` mit testify (`assert`, `require`), Zeitverhalten in `testing/synctest`, handgeschriebene Fakes ([Code-ADR-0006](docs/adr/code/0006-teststrategie.md)).

## Entscheidungen (ADRs)

- Entscheidungen werden als ADR festgehalten, **bevor** sie umgesetzt werden: Architektur in `docs/adr/`, Code in `docs/adr/code/`. Konventionen und Vorlage: [`docs/adr/README.md`](docs/adr/README.md), [`docs/adr/TEMPLATE.md`](docs/adr/TEMPLATE.md).
- Ein neues ADR bekommt die nächste freie Nummer und den Status „Vorgeschlagen“. Die Nummern im ADR-Backlog (Plan §12) sind vorläufig.
- **Abnahme:** Der Projektinhaber nimmt jedes ADR einzeln ab. Erst dann wird der Status „Akzeptiert“ gesetzt, mit Datum und „Entscheidung durch“.
- Akzeptierte ADRs werden inhaltlich nicht umgeschrieben. Eine geänderte Entscheidung bekommt ein neues ADR, das das alte ablöst.

## Herkunft (ADR-0001)

streamcrew ist eine Neuimplementierung. Für jede fachliche Arbeit gilt [ADR-0001](docs/adr/0001-neuimplementierung-und-nutzung-des-originals.md), ausgeführt in [`docs/spec/README.md`](docs/spec/README.md):

1. Erst eine Spezifikation in `docs/spec/` schreiben, in eigenen Worten und mit Quellenangabe. Dann aus der Spezifikation implementieren, nicht aus dem Code des Originals.
2. Aus `../mixitup` wird kein Code übernommen oder übersetzt, auch keine Kommentare oder Texte.
3. KI-Assistenten bekommen die Spezifikation als Eingabe, keinen C#-Code und keine Übersetzungsaufträge.

## Dokumentation und Roadmap

- Dokumentation (README, `docs/`, Code-Kommentare) wird mit jeder Änderung aktualisiert.
- Erledigte Aufgaben werden in der [Roadmap](docs/roadmap.md) sofort abgehakt; größere Änderungen kommen in deren Änderungshistorie.
