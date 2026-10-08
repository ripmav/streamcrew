# ADR-0005: Core in Desktop-Builds: mitgeliefert und als eigener Prozess gestartet

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-27 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Ergänzt [ADR-0003](0003-betriebsmodi.md) (Variante „Desktop-App“); Plan §6.5, §6.14, §7.4; Roadmap Phase 6 und Desktop-Track |
| **Ergänzt durch** | [ADR-0006](0006-core-als-bibliothek-fuer-selbststart.md): Core als Bibliothek für den Selbststart; mitgeliefert oder eingebunden legt der Build fest |

## Kontext

- Der Core läuft primär auf dem Streaming-PC, bedient über die Desktop-App (Fyne, eigenes Repository) ([ADR-0003](0003-betriebsmodi.md)).
- Für Desktop-Builds gibt es zwei Wege:
  - **A: Einbinden.** Die Desktop-App importiert den Core als Go-Bibliothek und startet ihn im eigenen Prozess (ein Binary, ein Prozess).
  - **B: Mitliefern.** Das Desktop-Paket enthält das Core-Binary, die App startet es automatisch als eigenen Prozess (zwei Binaries, zwei Prozesse).
- Die Kräfte:
  - **Stabilität im Live-Stream:** Ein Absturz der Oberfläche darf Bot und Overlays nicht beenden.
  - **Einheitliche Architektur:** Daemon- und Server-Modus existieren ohnehin.
  - **Einfache Auslieferung:** am liebsten ein Binary.
  - Aufwand bis zur Desktop-Beta.

## Entscheidung

1. **Wir liefern den Core mit (B).** Auch bei Nutzung der Desktop-App läuft der Core als eigener Prozess. Das Desktop-Paket enthält das passende Core-Binary, und die App startet es bei Bedarf automatisch.
2. **Start:**
   - Läuft für das Profil bereits ein Core, verbindet sich die App mit ihm. Erkannt wird das an Laufzeitdatei, Profilsperre und Health-Check.
   - Andernfalls startet die App das mitgelieferte Core-Binary und verbindet sich, sobald `/readyz` Bereitschaft meldet.
3. **Lebensdauer:**
   - Der Core wird **losgelöst** von der App gestartet: unter Unix als eigene Session, unter Windows als eigene Prozessgruppe ohne Konsole. Stürzt die Oberfläche ab oder wird sie geschlossen, laufen Bot und Overlays weiter. Nach einem Neustart verbindet sich die App wieder.
   - Beendet wird der Core nur ausdrücklich: über die Oberfläche, den API-Aufruf zum Herunterfahren oder das Abmelden vom System.
   - Eine Einstellung „Core mit der App beenden“ sendet beim regulären Schließen der App den Shutdown-Aufruf.
   - Die Profilsperre verhindert doppelte Instanzen.
4. **Kommunikation** läuft ausschließlich über die öffentliche API, auf demselben Weg wie im Remote- und Server-Modus.
   - Lokal über einen Unix-Socket (unter Windows 10+ ebenfalls möglich) oder Loopback-TCP.
   - Das Token liegt im Datenverzeichnis (Dateirechte 0600) und wird nie über die Kommandozeile übergeben.
5. **Überwachung:** Die App prüft die Gesundheit des Cores, zeigt seinen Zustand an und startet ihn nach einem Absturz mit Backoff neu.
6. **Versions-Handshake:** Beim Verbinden prüft die App die API-Version des Cores. Ist sie inkompatibel, bietet sie an, den laufenden Core zu beenden und das mitgelieferte Binary zu starten.
7. **Build:** Das Core-Binary im Desktop-Paket stammt aus derselben Codebasis wie das Server-Binary. Unterschiede entstehen nur über Build-Tags, z. B. für die lokale Audioausgabe ([ADR-0003](0003-betriebsmodi.md)).
8. **Host-Funktionen:** Globale Hotkeys sowie Tastatur- und Mausaktionen liefert die Desktop-App über das Agent-Protokoll der API.

**Ausdrücklich offen:** Ob der Core **zusätzlich als Go-Bibliothek** bereitgestellt wird, wird separat entschieden (ADR-Backlog, Plan §12.1). Das würde etwa ein einzelnes Binary für macOS ermöglichen. Diese Entscheidung schließt das nicht aus. *Inzwischen entschieden in [ADR-0006](0006-core-als-bibliothek-fuer-selbststart.md): Bibliothek nur für den Selbststart; ob der Core mitgeliefert oder eingebunden wird, legt der Build-Prozess fest.*

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| A: Einbinden (ein Prozess) | Ein Absturz der Oberfläche oder ein Go-Panic im Core beendet Bot und Overlays mitten im Stream. Außerdem entstünde ein dritter Betriebsweg und ein zusätzlicher Go-API-Vertrag, und der Core liefe im CGO-Prozess von Fyne. Vorteile wären ein Binary und weniger Aufwand. |
| C: Selbststart (ein Binary, zwei Prozesse) | Die Desktop-App startet sich selbst ein zweites Mal als Core-Prozess. Das setzt den Core als Bibliothek voraus und gehört deshalb zur offenen Folgeentscheidung. |

## Konsequenzen

**Positiv:**

- Abstürze der Oberfläche beenden weder Bot noch Overlays.
- Der Bot kann ohne offenes Fenster laufen, und die TUI kann parallel zur Desktop-App genutzt werden.
- Die Desktop-App hat einen einzigen Verbindungsweg: Lokal und remote unterscheiden sich nur in der Adresse.
- Der Core-Prozess enthält keinen GUI-Code; die API-Grenze ist erzwungen.

**Negativ und Risiken:**

- Zwei Binaries je Plattform müssen zusammen paketiert und beide signiert werden. Unter macOS liegt das Core-Binary als Helfer im `.app`-Bundle und muss mitsigniert und notarisiert werden.
- Die Prozessverwaltung ist zu bauen: Start, Erkennung, Health-Check, Neustart, Versions-Handshake.
- Hotkeys und Eingabeaktionen in der Desktop-App brauchen das Agent-Protokoll früher als ursprünglich geplant.
- Der zweite Prozess braucht etwas mehr Speicher.
- Mehraufwand gegenüber A: grob 1–1,5 PW.

**Folgearbeiten:**

- [x] Plan und Roadmap an diese Entscheidung angepasst (2026-09-27)
- [x] Lokalen Transport im API-ADR festgelegt: [ADR-0010](0010-api-protokoll.md) (2026-09-28)
- [ ] Paketierung mit mitgeliefertem Core je Plattform im Desktop-Spike prüfen, vor allem Signatur und Notarisierung unter macOS (Desktop D0)
- [x] Folgeentscheidung zur Bibliothek getroffen: [ADR-0006](0006-core-als-bibliothek-fuer-selbststart.md) (2026-09-27)
