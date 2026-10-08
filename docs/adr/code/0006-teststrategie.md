# Code-ADR-0006: Teststrategie

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0001](../0001-neuimplementierung-und-nutzung-des-originals.md); Plan §11.2, §11.4; Roadmap Phase 1.2; [`docs/spec/README.md`](../../spec/README.md); [Code-ADR-0001](0001-go-toolchain-und-linting.md), [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md) |

*Im ADR-Backlog (Plan §12.2) stand dieses ADR vorläufig unter der Nummer 0014. Es bekommt nach der ADR-Konvention die nächste freie Nummer; die bisherigen Backlog-Nummern 0006 bis 0013 rücken um eins auf.*

## Kontext

- Plan §11.2 beschreibt die Testebenen von Unit-Tests bis zu Lasttests, Plan §11.4 verlangt Tests für jede neue Funktion.
- Spezifikationen tragen Verhaltens-IDs (`B1`, `B2` …), auf die Tests verweisen sollen ([`docs/spec/README.md`](../../spec/README.md)).
- Viel Verhalten des Cores hängt von Zeit ab: Cooldowns, Timer, Backoff, Timeouts, Shutdown. Tests mit echten Wartezeiten sind langsam und unzuverlässig.
- Die CI testet nur unter Linux, weil Windows- und macOS-Runner im privaten Repository ein Vielfaches an Minuten kosten ([Code-ADR-0001](0001-go-toolchain-und-linting.md)). Ab Phase 1.3 entsteht Code mit plattformabhängigem Verhalten: Pfade, Signale, portabler Modus.
- `gochecknoglobals` gilt auch in Testdateien; ein paketweites `flag.Bool("update", …)` für Golden Files ist damit nicht möglich.

## Entscheidung

1. **`testing` mit testify als Assertion-Bibliothek** (`github.com/stretchr/testify`, MIT):
   - `require` für Bedingungen, ohne die der Test nicht sinnvoll weiterlaufen kann (Fehler beim Aufbau, erwartete Fehler), `assert` für die übrigen Prüfungen
   - Fehler mit `require.ErrorIs` bzw. `assert.ErrorIs` und `ErrorAs`, nie über den Text der Meldung
   - Nur `assert` und `require`: Die Pakete `mock` und `suite` werden nicht verwendet; Fakes bleiben handgeschrieben (Punkt 3), Tests bleiben gewöhnliche Funktionen mit `t.Run`.
   - Kein zusätzliches `go-cmp`: testify zeigt bei Abweichungen selbst einen Diff.
   - `require` nur in der Goroutine des Tests, weil es `t.FailNow` aufruft; in anderen Goroutinen gilt `assert`.
2. **Aufbau:**
   - tabellengetrieben mit `t.Run`; `t.Parallel()`, wo Tests keinen Zustand teilen (nicht zusammen mit `t.Setenv`)
   - Hilfsfunktionen rufen `t.Helper()`; `t.TempDir()`, `t.Setenv()`, `t.Cleanup()` und `t.Context()` statt eigener Auf- und Abbauten
   - Black-Box-Tests (`package x_test`) für das exportierte Verhalten; interne Tests (`package x`) nur für Details, die sich von außen nicht sinnvoll prüfen lassen
3. **Fakes** sind handgeschrieben und implementieren die Interfaces beim Konsumenten ([Code-ADR-0002](0002-dependency-injection.md)). Werden sie in mehreren Paketen gebraucht, liegen sie in einem Paket `<name>test`, z. B. `internal/platform/platformtest`.
4. **Zeit und Nebenläufigkeit:** Alles mit Timern, Backoff oder Timeouts läuft in `testing/synctest`: keine echten Wartezeiten, und Goroutinen, die am Ende noch laufen, lassen den Test scheitern. HTTP-Tests nutzen `net/http/httptest`, innerhalb von synctest das In-Memory-Netz von `httptest.NewTestServer`. Die CI testet mit `-race -shuffle=on`.
5. **Golden Files** liegen in `testdata/` mit der Endung `.golden`. Neu geschrieben werden sie mit `STREAMCREW_UPDATE_GOLDEN=1 go test ./…`, weil ein paketweites Flag gegen `gochecknoglobals` verstößt.
6. **Fuzzing** für Parser und alle Eingaben von außen (Templates, Trigger, Importer, Protokolle). Gefundene Fehlerfälle bleiben als Seed in `testdata/fuzz/`. Die CI lässt jedes Fuzz-Ziel 20 s laufen (`scripts/fuzz.sh`).
7. **Testebenen** wie in Plan §11.2.
   - Integrationstests, etwa mit echter SQLite in `t.TempDir()`, sind gewöhnliche Tests ohne Build-Tags, solange sie schnell und abgeschlossen sind.
   - Lange Tests überspringen sich mit `testing.Short()`.
   - Wo End-to-End-Tests mit Mock-Plattform liegen, klärt Phase 3.
8. **Nachvollziehbarkeit:** Tests zu einer Spezifikation nennen die Verhaltens-ID im Testnamen oder als Kommentar, z. B. `TestCooldown_B4`.
9. **Abdeckung** wird gemessen, aber nicht als Schranke in der CI erzwungen. Die Ziele aus Plan §11.2 (mindestens 80 % in `engine`, `template`, `requirement`) werden an den Meilensteinen geprüft.
10. **Native Tests unter Windows und macOS** laufen als eigener CI-Job auf `windows-latest` und `macos-latest`, aber nur wöchentlich im bestehenden Zeitplan und auf Anforderung (`workflow_dispatch`), nicht bei jedem Pull Request. Das hält die Actions-Minuten klein: macOS-Runner zählen zehnfach, Windows-Runner doppelt.
11. **Testdaten** sind selbst erzeugt oder stammen aus der offiziellen Dokumentation der Plattformen; Nutzerdaten sind anonymisiert. Daten aus fremden Beständen oder aus dem Original werden nicht verwendet ([ADR-0001](../0001-neuimplementierung-und-nutzung-des-originals.md)).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Nur die Standardbibliothek | keine Abhängigkeit, aber längere Vergleiche von Hand und keine Diffs bei Abweichungen; Entscheidung des Projektinhabers für testify |
| `go-cmp` statt testify | liefert Diffs, aber keine Assertions; Prüfungen blieben von Hand geschrieben |
| testify `mock` bzw. `gomock`, `mockery` | prüfen eher Aufrufe als Verhalten und koppeln Tests an Implementierungsdetails; handgeschriebene Fakes sind robuster |
| testify `suite` | Zustand über Methoden einer Suite erschwert parallele Tests; `t.Run` und Hilfsfunktionen genügen |
| `-update`-Flag für Golden Files | verstößt als Paketvariable gegen `gochecknoglobals` |
| Native Tests bei jedem Pull Request | je Lauf grob zehn bis fünfzehn abgerechnete Minuten mehr; plattformabhängige Fehler sind selten und fallen wöchentlich rechtzeitig auf |
| Keine nativen Tests | Pfade, Signale und Dateirechte verhalten sich unter Windows anders; Fehler fielen erst beim Nutzer auf |
| Abdeckung als harte Schranke | verleitet zu Tests ohne Aussage; die Ziele werden an den Meilensteinen geprüft |

## Konsequenzen

**Positiv:**

- Tests sind schnell und deterministisch; Assertions sind kurz und zeigen bei Abweichungen einen Diff.
- Plattformabhängige Fehler fallen spätestens nach einer Woche auf, ohne jeden Pull Request zu verteuern.
- Jede Verhaltensregel einer Spezifikation lässt sich bis zum Test verfolgen.

**Negativ und Risiken:**

- testify ist eine Testabhängigkeit mehr (mit `github.com/stretchr/objx`, MIT, und `go.yaml.in/yaml/v3`, das der Core ohnehin nutzt). Sie landet nicht im Binary.
- Ein Fehler unter Windows oder macOS kann bis zum nächsten wöchentlichen Lauf unbemerkt auf `main` liegen. Wer plattformabhängigen Code ändert, startet den Job deshalb im Pull Request von Hand.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-09-29
- [x] testify einführen und Tests entsprechend schreiben (Roadmap Phase 1.3), erledigt 2026-09-29
- [x] CI-Job für native Tests unter Windows und macOS ergänzen (Roadmap Phase 1.3); damit ist die entsprechende Folgearbeit aus [Code-ADR-0001](0001-go-toolchain-und-linting.md) erledigt, erledigt 2026-09-29
- [ ] Ablageort für End-to-End-Tests festlegen (Roadmap Phase 3)
