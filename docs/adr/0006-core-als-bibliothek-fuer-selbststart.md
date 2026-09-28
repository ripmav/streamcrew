# ADR-0006: Core als Bibliothek für den Selbststart der Desktop-App

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-27 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Ergänzt [ADR-0005](0005-core-in-desktop-builds.md); Plan §6.14, §7.4, §9; Roadmap Phase 6, Desktop D0, D1 und D7 |
| **Ergänzt durch** | [ADR-0007](0007-release-artefakte-des-cores.md): Release-Artefakte des Cores (Binary und Bibliothek) |

## Kontext

- Laut [ADR-0005](0005-core-in-desktop-builds.md) läuft der Core auch bei Desktop-Nutzung als eigener Prozess. Das Desktop-Paket liefert dafür das Core-Binary mit.
- Zwei Binaries je Paket erschweren die Auslieferung, vor allem unter macOS. Dort muss das Core-Binary als Helfer ins `.app`-Bundle und mitsigniert und notarisiert werden.
- Wird der Core zusätzlich als Go-Bibliothek bereitgestellt, entsteht ein einzelnes Binary. Das ist auf zwei Arten möglich:
  - **Selbststart:** Die Desktop-App startet sich selbst ein zweites Mal als Core-Prozess.
  - **Im selben Prozess:** Die Desktop-App betreibt den Core in ihrem eigenen Prozess.

## Entscheidung

1. **Der Core wird zusätzlich als Go-Bibliothek bereitgestellt.** Das öffentliche Paket `streamcrew/core` enthält nur eine schmale Start-API: im Kern `Run(ctx context.Context, opts Options) error` samt Optionstyp. Domänentypen, Dienste und Interna werden nicht exportiert; jede weitere Interaktion läuft über die API.
2. **Kein paralleler Codepfad:** `streamcrew serve` nutzt dieselbe Funktion. Das Paket ist eine dünne Hülle um die Composition Root (`internal/app`).
3. **Die Bibliothek dient ausschließlich dem Selbststart.** Das Desktop-Binary startet sich selbst mit einem versteckten Unterkommando als eigenen Core-Prozess und initialisiert dabei keine Oberfläche.
4. **Kein Betrieb im selben Prozess.** Die Desktop-App betreibt den Core nicht in ihrem eigenen Prozess, weil das die Absturzsicherheit aus ADR-0005 aufgeben würde.
5. **Der Build-Prozess legt die Auslieferungsart fest.** Es gibt zwei Build-Varianten der Desktop-App:

   | Build-Variante | Core im Paket | Start des Core-Prozesses |
   |---|---|---|
   | **mitgeliefert** | separates Core-Binary neben dem Desktop-Binary ([ADR-0005](0005-core-in-desktop-builds.md)) | Desktop-App startet das Core-Binary |
   | **eingebunden** | Core als Bibliothek im Desktop-Binary (ein Binary) | Desktop-App startet sich selbst im Core-Modus |

   - Die Variante wird beim Bauen gewählt, per Build-Tag im Desktop-Repository, der die passende Startroutine einbindet. Ohne diesen Tag wird die Core-Bibliothek nicht mitgelinkt.
   - Zur Laufzeit verhalten sich beide Varianten gleich: losgelöster Start, Kommunikation nur über die API, Health-Check, Versions-Handshake, und der Core überlebt einen Absturz der App.
   - Welche Variante für welche Zielplattform gebaut wird, ist Build-Konfiguration (Taskfile/CI) und keine Architekturentscheidung. Sie lässt sich ohne neues ADR ändern.
   - In der Variante „mitgeliefert“ wird das Core-Binary aus der Core-Version gebaut, die das Desktop-Repository in `go.mod` festlegt. Desktop und Core passen so immer zusammen. *Durch [ADR-0007](0007-release-artefakte-des-cores.md) geändert: Das Binary wird nicht selbst gebaut, sondern aus dem Release derselben Version bezogen.*
6. **Die Start-API ist ein versionierter Vertrag** (SemVer) und bleibt bewusst minimal.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Betrieb im selben Prozess | ein Binary und ein Prozess, aber ein Absturz der Oberfläche beendet Bot und Overlays; widerspricht ADR-0005 |
| Keine Bibliothek, nur ADR-0005 | immer zwei Binaries, unter macOS mit verschachtelter Signierung und Notarisierung |
| Feste Zuordnung von Variante zu Plattform im ADR | unnötig starr; die passende Variante hängt von Paketierung, Signatur und Werkzeugen ab und gehört in die Build-Konfiguration |

## Konsequenzen

**Positiv:**

- Jede Zielplattform kann ohne Codeänderung als ein Binary oder als zwei Binaries ausgeliefert werden. Unter macOS vereinfacht ein einzelnes Binary Signierung und Notarisierung.
- Verbindung, Prozessverwaltung und Absturzsicherheit sind in beiden Varianten identisch; nur die Startroutine unterscheidet sich.
- Die Variante „mitgeliefert“ bleibt schlank, weil sie die Core-Bibliothek nicht mitlinkt.

**Negativ und Risiken:**

- Das Desktop-Modul hängt am gesamten Abhängigkeitsbaum des Core-Moduls. Updates müssen gemeinsam erfolgen.
- Die öffentliche Start-API ist ein zusätzlicher Vertrag neben der API.
- In der Variante „eingebunden“ enthält der Core-Prozess den mitgelinkten Fyne- und CGO-Code. Das Binary wird größer, und der Core-Prozess ist nicht CGO-frei. In der Variante „mitgeliefert“ bleibt das Core-Binary CGO-frei.
- In der Variante „eingebunden“ ist zu prüfen, dass der Core-Prozess keine Oberfläche initialisiert, unter macOS insbesondere kein Dock-Symbol erzeugt.
- Zwei Build-Varianten müssen in der CI gebaut und getestet werden.
- Aufwand: grob 0,5–1 PW.

**Folgearbeiten:**

- [x] Plan und Roadmap an diese Entscheidung angepasst (2026-09-27)
- [ ] Paket `core` mit Start-API anlegen; `streamcrew serve` nutzt sie (Phase 6)
- [ ] Beide Startroutinen und den Build-Tag in der Desktop-App umsetzen (Desktop D1)
- [ ] Paketierungs-Spike (Desktop D0): beide Varianten auf allen Zielplattformen bauen und vergleichen (Signatur, Notarisierung, Paketgröße, Virenscanner, kein Dock-Symbol); daraus die Standardvariante je Plattform in der Build-Konfiguration festlegen

---

*Korrektur (2026-09-27): Die erste Fassung ordnete die Varianten fest Plattformen zu (macOS Selbststart, Windows und Linux separates Binary). Das gab die Entscheidung falsch wieder. Tatsächlich legt der Build-Prozess die Auslieferungsart fest (Punkt 5).*
