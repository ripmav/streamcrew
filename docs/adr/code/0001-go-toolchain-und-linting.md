# Code-ADR-0001: Go-Toolchain und Linting

| | |
|---|---|
| **Status** | Vorgeschlagen |
| **Datum** | 2026-09-28 |
| **Entscheidung durch** | Projektinhaber (ripmav), Annahme steht aus |
| **Bezug** | [ADR-0002](../0002-lizenz-des-projekts.md), [ADR-0009](../0009-repositories-und-hosting.md); Plan §8, §11.1, §11.3; Roadmap Phase 1.1 |

## Kontext

- Die globalen Regeln verlangen:
  - immer die neueste stabile Go-Version und die neueste stabile Version von golangci-lint
  - eine feste Prüfreihenfolge vor jedem Commit (Plan §11.1)
  - keine Unterdrückung von Befunden ohne Freigabe
- [ADR-0002](../0002-lizenz-des-projekts.md) verlangt:
  - einen SPDX-Header in jeder Quelldatei
  - nur Abhängigkeiten, deren Lizenz mit Apache-2.0 kompatibel ist; die CI prüft das
- [ADR-0009](../0009-repositories-und-hosting.md) legt fest:
  - GitHub Actions als CI
  - automatische Abhängigkeits-Updates
  - begrenzte Actions-Minuten im privaten Repository, wobei macOS-Runner mehrfach zählen
- Verweise zwischen Plan, Roadmap, ADRs und Spezifikationen dürfen nicht ins Leere zeigen (Verweis-Konvention in [`../README.md`](../README.md)).
- Plan §11.1 legt Konventionen fest, die sich maschinell prüfen lassen, etwa: kein `init()`, keine globalen Variablen, Doc-Kommentare für exportierte Bezeichner.

## Entscheidung

1. **Go-Version:**
   - Die `go`-Direktive in `go.mod` nennt die neueste stabile Patch-Version, derzeit `go 1.27.1`.
   - Eine `toolchain`-Zeile gibt es nur, wenn sie von der `go`-Direktive abweicht.
   - Mit jedem Go-Release wird die Direktive von Hand angehoben, weil Dependabot sie nicht aktualisiert.
   - Die CI liest die Version aus `go.mod` (`go-version-file`). Lokal und in der CI läuft damit dieselbe Version.
2. **golangci-lint v2** mit der Konfiguration in `.golangci.yml`:
   - Basis ist `default: standard` (`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`) mit diesen Ergänzungen:

     | Zweck | Linter |
     |---|---|
     | Korrektheit | `bodyclose`, `contextcheck`, `exhaustive` (ein `default`-Zweig gilt als vollständig), `nilerr`, `noctx`, `unparam` |
     | Fehlerbehandlung | `errname`, `errorlint` |
     | Sicherheit | `gosec` |
     | Lizenz | `goheader` mit der Vorlage `SPDX-License-Identifier: Apache-2.0` ([ADR-0002](../0002-lizenz-des-projekts.md)) |
     | Konventionen aus Plan §11.1 | `gochecknoinits`, `gochecknoglobals`, `revive` (Doc-Kommentare), `godot`, `forbidigo` (kein `fmt.Print*`, `print`, `println`; Ausgaben über `log/slog` oder einen ausdrücklichen Writer) |
     | Unterdrückungen | `nolintlint`: `//nolint` nur für einen bestimmten Linter und mit Begründung |
     | Stil und modernes Go | `copyloopvar`, `gocritic`, `intrange`, `misspell` (US), `perfsprint`, `sloglint`, `unconvert`, `usestdlibvars` |

   - Als Formatter laufen `gofmt` und `goimports`, eigene Importe unter `github.com/ripmav/streamcrew` stehen in einer eigenen Gruppe.
   - Von den Ausschluss-Voreinstellungen gelten nur `common-false-positives` und `std-error-handling`. Generierter Code wird nachsichtig geprüft (`generated: lax`).
   - Lokal läuft die neueste Version (derzeit 2.14.0). Die CI pinnt die Version in `ci.yml`. Wer lokal aktualisiert, hebt sie dort mit an.
   - Kein `//nolint` und keine Lockerung der Konfiguration ohne Freigabe durch den Projektinhaber.
3. **SPDX-Header in anderen Dateien:** Shell-Skripte und YAML-Dateien tragen den Header als Kommentar. Das ist Konvention und wird nicht maschinell geprüft.
4. **Pre-Commit-Checkliste als Skript `scripts/check.sh`:**
   - Es führt die Schritte aus Plan §11.1 in der festgelegten Reihenfolge aus, inklusive `go install golang.org/x/vuln/cmd/govulncheck@latest`.
   - Danach zeigt es an, welche Dateien `go fix` und `gofmt` geändert haben.
   - Es ist bewusst kein Git-Hook, weil `go fix` und `gofmt` Dateien erst nach dem Staging ändern würden.
5. **CI mit GitHub Actions:**

   | Workflow und Job | Inhalt |
   |---|---|
   | `ci.yml`, Checks | `go fix -diff` (schlägt fehl, wenn noch Umschreibungen ausstehen), `go vet`, golangci-lint, `govulncheck`, Lizenzprüfung |
   | `ci.yml`, Tests | `go test -race -shuffle=on`; kurze Fuzz-Läufe über `scripts/fuzz.sh`, 20 s je Fuzz-Ziel |
   | `ci.yml`, Build | Cross-Build mit `CGO_ENABLED=0` für linux, windows, darwin × amd64, arm64 |
   | `docs.yml`, Links | `lychee --offline --include-fragments` über alle Markdown-Dateien: Dateien und Überschriften-Anker, keine externen Links |

   - **Lizenzen:** `go-licenses check` mit einer Allowlist: Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause, ISC, 0BSD, Zlib.
     - Alles andere lässt die Prüfung fehlschlagen, auch MPL-2.0 und nicht erkannte Lizenzen.
     - Eine Erweiterung braucht die Freigabe des Projektinhabers und ein Code-ADR, das dieses ergänzt.
   - **Actions-Minuten sparen:**
     - nur `ubuntu-latest`
     - Cross-Compile statt macOS- oder Windows-Runnern
     - wenige Jobs, weil jeder Job mindestens eine Minute kostet
     - Pfadfilter: Go-Prüfungen nur bei Go-Dateien, Linkprüfung nur bei Markdown
     - Der wöchentliche Zeitplan startet nur den Checks-Job, damit neu veröffentlichte Schwachstellen ohne Codeänderung auffallen.
   - **Absicherung:**
     - Fremde Actions sind auf den vollen Commit-SHA gepinnt, mit der Version als Kommentar.
     - `permissions: contents: read`
     - `persist-credentials: false` beim Checkout
     - Werkzeuge, die sich mit `go install` oder `go run` holen lassen (`govulncheck`, `go-licenses`), laufen ohne eigene Action.
6. **Abhängigkeits-Updates mit Dependabot** (`.github/dependabot.yml`):
   - wöchentlich am Montag
   - je Ökosystem (`gomod`, `github-actions`) zu einem Pull Request gruppiert
   - Commit-Präfixe `build(deps)` und `ci(deps)` nach Conventional Commits

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| golangci-lint nur mit `default: standard` | prüft weder den SPDX-Header noch die Konventionen aus Plan §11.1 noch Sicherheitsaspekte |
| golangci-lint mit `default: all` | viele widersprüchliche oder laute Linter (z. B. `exhaustruct`, `varnamelen`, `wsl`); das erzeugt ständig Druck zu `//nolint` |
| Linter `modernize` in golangci-lint | doppelt zu `go fix`, das seit Go 1.26 dieselben Modernisierungen ausführt und in Checkliste und CI läuft |
| Renovate | mächtiger, braucht aber eine GitHub-App oder einen eigenen Betrieb; Dependabot ist eingebaut und reicht für zwei Ökosysteme |
| Taskfile oder Makefile | zusätzliches Werkzeug bzw. unter Windows unüblich; für eine feste Befehlskette reicht ein Bash-Skript. Ein Taskfile kann mit den Build-Varianten ([ADR-0006](../0006-core-als-bibliothek-fuer-selbststart.md)) später kommen. |
| Git-Hook statt Skript | `go fix` und `gofmt` ändern Dateien nach dem Staging; der Commit enthielte den ungeprüften Stand |
| Build als Job-Matrix oder auf nativen Runnern | sechs Jobs kosten mindestens sechs Minuten; macOS-Runner zählen mehrfach ([ADR-0009](../0009-repositories-und-hosting.md)) |
| Actions per Tag (`@v7`) | Tags sind veränderlich; ein kompromittierter Tag liefe sofort in der CI |
| Linkprüfung inklusive externer Links | braucht Netzzugriff und schlägt zufällig fehl; Verweise zwischen den Dokumenten sind intern |
| `golang/govulncheck-action` | eine weitere Fremd-Action; `go install …@latest` entspricht dem lokalen Ablauf |

## Konsequenzen

**Positiv:**

- Lokale Prüfung und CI laufen gleich ab: dieselbe Reihenfolge, dieselbe Go-Version aus `go.mod`.
- Konventionen aus Plan §11.1 und der SPDX-Header aus ADR-0002 werden maschinell geprüft.
- Der Minutenverbrauch bleibt gering, Fremd-Actions sind gegen veränderte Tags abgesichert.

**Negativ und Risiken:**

- Die `go`-Direktive und die golangci-lint-Version in `ci.yml` werden von Hand angehoben; Dependabot erkennt beide nicht.
- Unter Windows und macOS laufen keine Tests, nur Builds. Plattformabhängiges Verhalten bleibt in der CI ungetestet, etwa Pfade, Signale oder der lokale Transport ([ADR-0010](../0010-api-protokoll.md)).
- `gochecknoglobals` und `forbidigo` können legitime Fälle treffen. Dafür braucht es eine Freigabe, die `nolintlint` dann mit Begründung dokumentiert.

**Folgearbeiten:**

- [ ] Nach der Annahme den Status setzen und in ADR-0002 und ADR-0009 den Vermerk „Ergänzt durch“ eintragen
- [ ] `claude.yml` ebenfalls auf Commit-SHAs pinnen, sobald der Review-Nachweis vorliegt (Roadmap Phase 1.1)
- [ ] Native Test-Läufe unter Windows und macOS gezielt ergänzen, sobald plattformabhängiger Code entsteht (Roadmap Phase 1.3)
- [ ] CI erweitern: Docker-Build mit Smoke-Test (Roadmap Phase 1.4), `sqlc diff` (Phase 2), `buf lint` und `buf breaking` (Phase 6)
