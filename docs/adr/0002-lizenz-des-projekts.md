# ADR-0002: Lizenz des Projekts: MIT

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-27 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §3 und §8, Roadmap Phasen 0, 1 und Gate O; [ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md) |

## Kontext

- Das Projekt bleibt zunächst privat und wird später als Open Source veröffentlicht. Die Lizenz soll von Anfang an feststehen. So steht jeder Commit von Beginn an unter klaren Bedingungen, und zur Veröffentlichung muss nichts nachlizenziert werden.
- Die geplanten Abhängigkeiten sind permissiv lizenziert (MIT, BSD, Apache-2.0), darunter kong, Bubble Tea, Fyne, goja, `modernc.org/sqlite` und ConnectRPC.
- Der Core läuft auch als Server-Anwendung ([ADR-0003](0003-betriebsmodi.md)).

## Entscheidung

- Aller eigene Code und alle eigene Dokumentation stehen unter der **MIT-Lizenz** (SPDX: `MIT`).
- Das gilt für alle Repositories des Projekts: Core, Desktop, Web und den optionalen Relay.
- Jedes Repository enthält ab dem ersten Commit den unveränderten offiziellen Lizenztext als `LICENSE` im Wurzelverzeichnis.
- Jede Quelldatei beginnt mit der Kennung `SPDX-License-Identifier: MIT` im Kommentarformat der jeweiligen Sprache, in Go also `// SPDX-License-Identifier: MIT`.
- Abhängigkeiten müssen mit MIT kompatibel sein. Die CI prüft das, z. B. mit `github.com/google/go-licenses`. Abhängigkeiten unter GPL-2.0-only sind ausgeschlossen.
- Eine `NOTICE`-Datei mit Hinweisen zu Drittkomponenten entsteht spätestens zum Open-Sourcing (Gate O).
- Beitragsregeln (DCO oder CLA) werden zum Open-Sourcing festgelegt. Bis dahin gibt es keine externen Beiträge.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Apache-2.0 | Mehr Schutz durch ausdrückliche Patentlizenz und NOTICE-Mechanismus, aber umfangreicher Text und im Go-Ökosystem für dieses Tool nicht nötig. |
| AGPL-3.0 | Schützt vor geschlossenen Server-Forks, schreckt aber manche Nutzer und Firmen ab; die Netzwerkklausel ist für ein primär lokal betriebenes Tool wenig relevant. |
| GPL-3.0 | Copyleft nur für weitergegebene Binaries, kein Schutz im Serverbetrieb; schränkt die Weiterverwendung als Bibliothek ein. |

## Konsequenzen

**Positiv:**

- Die Rechte sind von Anfang an klar; zur Veröffentlichung muss nichts relizenziert werden.
- Der Lizenztext ist kurz und für Nutzer sofort verständlich.
- Sie ist mit allen geplanten Abhängigkeiten kompatibel und im Go-Ökosystem verbreitet.

**Negativ und Risiken:**

- Keine ausdrückliche Patentlizenz; Dritte dürfen geschlossene Forks und gehostete Angebote bauen.
- Die Lizenz deckt nur eigenständigen Code ab (siehe [ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md)).

**Folgearbeiten:**

- [x] `LICENSE` mit dem offiziellen Text von opensource.org im Wurzelverzeichnis anlegen (2026-09-27)
- [ ] SPDX-Header-Konvention und `goheader`-Linter einrichten und im geplanten Code-ADR zu Toolchain und Linting festhalten (Phase 1)
- [ ] Lizenzprüfung der Abhängigkeiten in der CI (Phase 1)
- [ ] `NOTICE` und Beitragsregeln zum Open-Sourcing (Gate O)
