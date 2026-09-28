# ADR-0007: Release-Artefakte des Cores: Binary und Bibliothek

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-27 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Ergänzt [ADR-0005](0005-core-in-desktop-builds.md) und [ADR-0006](0006-core-als-bibliothek-fuer-selbststart.md); Plan §7.4, §11.3; Roadmap Phase 6, Desktop D0, D1 und D7 |

## Kontext

- Laut [ADR-0006](0006-core-als-bibliothek-fuer-selbststart.md) legt der Build-Prozess der Desktop-App fest, ob der Core mitgeliefert (separates Binary) oder eingebunden (Bibliothek, Selbststart) wird. Die Desktop-Builds brauchen dafür beide Formen des Cores.
- Der Build-Prozess des Cores soll deshalb beide liefern und beide einem Release anhängen.
- **Technische Randbedingung:** Go-Programme können keine vorkompilierten Go-Bibliotheken einbinden.
  - Seit Go 1.13 gibt es keine Binary-only-Pakete mehr. Mit `-buildmode=archive` erzeugte `.a`-Dateien baut `go build` immer aus dem Quellcode neu.
  - `-buildmode=shared` und `-buildmode=plugin` sind auf bestimmte Plattformen beschränkt (`plugin` etwa nicht unter Windows) und verlangen identische Toolchain- und Abhängigkeitsversionen.
  - Für Go-Konsumenten ist eine Go-Bibliothek deshalb der versionierte Quellcode des Moduls.
  - `-buildmode=c-shared` bzw. `c-archive` erzeugen C-Bibliotheken für andere Sprachen. Ein Host würde den Core damit in seinem eigenen Prozess betreiben, was ADR-0006 ausschließt.

## Entscheidung

1. **Jedes Core-Release** (Git-Tag `vX.Y.Z`) entsteht in einer Build-Pipeline und enthält beide Artefakte derselben Version:
   - **Binary:** `streamcrew` für alle Zielplattformen (Linux, Windows, macOS; amd64 und arm64), CGO-frei. Sobald es sie gibt, kommt die Desktop-Variante mit lokaler Audioausgabe (Build-Tag, [ADR-0003](0003-betriebsmodi.md)) hinzu.
   - **Bibliothek:** das Go-Modul `streamcrew` als Quellarchiv im Layout eines Go-Modul-Proxys (`.zip`, `.mod`, `.info`). Es enthält die Start-API `core` (ADR-0006) mit allem, was sie braucht, und ist direkt über `GOPROXY=file://…` nutzbar. Das Modul-Zip entspricht dem Format, dessen Prüfsumme (`h1:`) in `go.sum` steht.
2. **Integrität:** Das Release enthält Prüfsummen für alle Artefakte und eine SBOM. Signaturen regelt das Release-ADR (Backlog, Plan §12.1).
3. **Die Desktop-Builds beziehen den Core aus dem Release** der Version, die das Desktop-Repository in `go.mod` festlegt:
   - Variante **mitgeliefert:** Das Binary für die Zielplattform wird aus dem Release geladen und gegen die Prüfsumme geprüft.
   - Variante **eingebunden:** Die Bibliothek kommt über die normale Modulauflösung des Tags. Für Offline- oder reproduzierbare Builds wird das Quellarchiv aus dem Release per `GOPROXY=file://…` genutzt.
4. **Abweichung von ADR-0006:** Dort sollte das Core-Binary aus der in `go.mod` festgelegten Version selbst gebaut werden. Stattdessen stammt es jetzt aus dem Release derselben Version. Desktop- und Server-Nutzer bekommen damit dasselbe geprüfte Binary.
5. **Pipeline:**
   - `goreleaser` baut die Binaries.
   - Ein eigener Schritt erzeugt das Modul-Quellarchiv (z. B. mit `golang.org/x/mod/zip`).
   - Beide landen im selben Release.
   - Die CI prüft das Quellarchiv, indem sie ein kleines Testprogramm per `GOPROXY=file://…` dagegen baut.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Bibliothek nur über das Git-Tag bzw. den Modul-Proxy, ohne Release-Artefakt | Standardweg in Go, aber kein eigenständiges Artefakt für Offline- oder reproduzierbare Builds; widerspricht der Vorgabe, beides dem Release anzuhängen |
| C-Bibliothek (`c-shared`, `c-archive`) | von der Go-Desktop-App nicht sinnvoll nutzbar; bedeutet Betrieb im selben Prozess (widerspricht ADR-0006); CGO im Core-Build |
| Go-Plugin oder `-buildmode=shared` | nicht auf allen Zielplattformen verfügbar; starre Kopplung an Toolchain- und Abhängigkeitsversionen |
| Desktop-Build baut das Core-Binary selbst (bisher ADR-0006) | doppelte Build-Logik; Desktop-Nutzer bekämen andere Binaries als die geprüften Release-Artefakte |

## Konsequenzen

**Positiv:**

- Eine Quelle für alle Auslieferungen: Server, Desktop „mitgeliefert“ und Desktop „eingebunden“ nutzen dieselbe Release-Version.
- Die Desktop-Pakete enthalten genau die getesteten, geprüften Binaries.
- Die Bibliothek ist auch ohne Git-Zugriff und Modul-Proxy nutzbar, etwa offline oder archiviert.

**Negativ und Risiken:**

- Die Release-Pipeline wird aufwendiger: Quellarchiv erzeugen und in der CI prüfen.
- Nach dem Open-Sourcing ist das Quellarchiv neben dem öffentlichen Modul-Proxy teilweise redundant.
- Solange die Repositories privat sind, braucht auch der Zugriff auf Release-Artefakte eine Authentifizierung.

**Folgearbeiten:**

- [x] Plan und Roadmap an diese Entscheidung angepasst (2026-09-27)
- [ ] Release-Pipeline: Binaries, Modul-Quellarchiv, Prüfsummen, SBOM und Prüfschritt (Phase 6)
- [ ] Desktop-Build: Core-Artefakte aus dem Release der in `go.mod` festgelegten Version beziehen und prüfen (Desktop D0/D1)
- [ ] Das Release-ADR (Backlog) baut auf dieser Entscheidung auf
