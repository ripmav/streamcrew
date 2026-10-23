# Plan: TUI-Tests mit dem Bubble-Tea-v2-Test-Harness (Roadmap 6.4.3)

**Status:** offen
**Stand:** 2026-10-23, `main` bei `de57571`
**Roadmap:** [6.4 TUI](../../roadmap.md#64-tui), Punkt „TUI-Tests mit dem Test-Harness für Bubble Tea v2 (S)“

## 1. Ausgangslage

- 20 TUI-Tests in `internal/tui` (`model_test.go`, `views_test.go`, `settings_test.go`,
  `logfilter_test.go`) fahren das Modell per **eigener `keyMsg`-Fakes**
  (`next, cmd := m.Update(&keyMsg{code: tea.KeyTab})`) — das offizielle Test-Harness
  von Bubble Tea v2 wird nicht genutzt.
- Das Harness für `charm.land/bubbletea/v2` ist
  `charm.land/x/exp/teatest/v2` (API: `NewTestModel(tb, model, opts...)` mit
  `WithInitialTermSize`, `WithProgramOptions`; `Send`, `Type`, `WaitFinished`,
  `FinalModel`, `FinalOutput`, `Quit`). Aktuelle Version:
  `v2.0.0-20261018003744-cb897cb4ed72` (x/exp, experimentell).
- Die Integrationstests (`views_test.go`, `settings_test.go`) starten schon einen
  In-Prozess-Mock-Core (`startMockCore`) — der bleibt unverändert; das Harness
  ersetzt nur die Modell-Fahrt.

## 2. Ziel

Die TUI-Tests laufen durch das Bubble-Tea-v2-Harness: echte
`tea.KeyMsg`-Nachrichten durch den echten Programm-Loop, Assertions auf dem
finalen Modell und (wo sinnvoll) auf der gerenderten Ausgabe. Die
bestehenden Tests werden 1:1 migriert, plus die in §4 genannten Lücken.

## 3. Schritte

- [ ] **3.1 Dependency** `charm.land/x/exp/teatest/v2` (latest) nach
  `go.mod`/`go.sum` via `go get`; Renovate pflegt sie weiter (Go-Module-Gruppe).
  x/exp-Status in einen Kommentar in der import-Kommentar-Zeile oder im Plan notieren;
  Fallback bei Bruch: bestehendes `keyMsg`-Setup behalten.
- [ ] **3.2 Test-Helfer** in `internal/tui` (test-only, z. B. `teatest_helper_test.go`):
  - `newTestModel(t, opts Options, programOpts ...tea.ProgramOption) *teatest.TestModel`:
    baut das Modell per `newModel` und übergibt es an `teatest.NewTestModel` mit
    `WithInitialTermSize(120, 40)` und `tea.WithoutRenderer()` **nicht** — die
    `FinalOutput`-Assertions brauchen den Renderer.
  - `newTestModelWithCore(t)`: kombiniert den Helfer mit `startMockCore(t)`
    (bestehender Helper aus `views_test.go`), damit Tests einen Core unter sich haben.
- [ ] **3.3 Migration der Modell-Tests** (`model_test.go`, 9 Tests):
  `keyMsg`-Fakes durch `tm.Send(tea.KeyMsg{...})` / `tm.Type("…")` ersetzen,
  Assertions auf `tm.FinalModel(t).(model)`. `q`-Quit über `tm.Quit()` bzw.
  `WaitFinished` prüfen.
- [ ] **3.4 Migration der View-Tests** (`views_test.go`, `logfilter_test.go`,
  `settings_test.go`): gleiche Umstellung; wo ein Render-Vergleich lohnt
  (Header/Footer, Tab-Reihe, Theme-Schaltung `t`), Assertion auf
  `tm.FinalOutput(t)` statt nur Modell-Feld.
- [ ] **3.5 Lücken füllen** (je ein Test, gegen Mock-Core):
  - View-Schaltung: `Tab`/`1`–`8` wechselt die aktive Ansicht, `?` zeigt die Hilfe
  - Chat: Eingabe + Enter sendet über die API (Mock-Core erhält die Nachricht),
    `d` löscht die auslösende Nachricht, `c` leert den Chat
  - Queue: `p` pausiert, `x` bricht die gewählte Instanz ab, `r` wiederholt
  - Commands: `/`-Filter, Enter führt aus, `e`/`E` schaltet an/aus
  - Logs: `l` schaltet den Level-Filter, Kopfzeile zeigt den aktiven Filter
- [ ] **3.6 Cleanup**: `keyMsg`-Type und nur-noch-tot-Code aus den Tests löschen;
  `scripts/check.sh` grün (gofmt, vet, lint, race).

## 4. Hinweise und Grenzen

- `pollInterval` (2 s) und `retryInterval` (5 s) sind im Modell hart verdrahtet:
  Tests, die auf Status-Updates warten, dürfen `WaitFinished` mit
  `WithFinalTimeout` (≥ 5 s) fahren oder den Zustand per `Send(statusMsg{...})`
  direkt setzen. Keinen neuen Test-Dial im Modell für kürzere Intervalle bauen —
  das ist Produktionscode für Tests. `shortcut: hartes pollInterval,
  injizierbares Intervall nur, wenn Tests flakky werden.`
- teatest/v2 ist x/exp: API-Brüche sind möglich; deshalb nur die schmale API
  (§1) verwenden und die Dependency gepinnt lassen.
- `FinalOutput`-Golden-Files: erst wenn zwei Ansichten stabil gerendert werden;
  sonst nur Teilstring-Assertions (z. B. Header-Titel). Golden-Files gehören
  nach `testdata/` und in `.gitattributes` nicht als Text-markiert (sie sind
  Text, aber die Konvention `*.golden -text` gilt, wenn welche entstehen).
- Test-Matrix nach AGENTS: neue/veränderte Testdateien bekommen ihre Matrix
  in `docs/tests/` (Bestand: `I-6.4-tui-framework.md` erweitern oder
  `I-6.4-tui-teatest.md` anlegen — Entscheiung bei der Umsetzung).

## 5. Verifikation

- `go test ./internal/tui/... -race` grün, alle 20 alten + die Lücken-Tests von 3.5
- `scripts/check.sh` grün
- Roadmap 6.4.3 abhaken, Statusübersicht und Änderungshistorie aktualisieren
