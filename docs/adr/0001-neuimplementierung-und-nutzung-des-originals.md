# ADR-0001: Neuimplementierung und Nutzung des Originals

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-27 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §3, Roadmap Phase 0 und Gate O; [ADR-0002](0002-lizenz-des-projekts.md) |

## Kontext

- Das Projekt soll die Funktionen von Mix It Up in Go bereitstellen ([`starting.md`](../starting.md)).
- Mix It Up Desktop steht seit dem 2025-10-22 unter der „Mix It Up Business Source License (no automatic conversion)“ v1.0.1.
  - Erlaubt (§2): Lesen, Auditieren, interne Nutzung.
  - Untersagt (§3): Weitergabe (§3.2), Nutzung der Software zum Bau eines konkurrierenden Produkts (§3.3), Verwendung der Marken (§3.4), Zugriff auf Serverdienste von Blazing Cacti (§3.5).
  - Die EULA verbietet zusätzlich das Dekompilieren.
- Die offizielle Dokumentation (<https://mixitup.bot/docs>) beschreibt Plattformen, Actions, Consumables, Overlays, Commands, Spiele, Special Identifiers, Developer-API und MCP-Server. Sie ist urheberrechtlich geschützt („All rights reserved“).
- Das Projekt bleibt zunächst privat und soll später als Open Source veröffentlicht werden ([ADR-0002](0002-lizenz-des-projekts.md)).

## Entscheidung

1. Das Projekt ist eine **vollständige Neuimplementierung des dokumentierten Verhaltens** von Mix It Up in Go. Der C#-Code wird **nicht 1:1 übertragen**.
2. Der öffentlich einsehbare Quellcode von Mix It Up dient als **Hilfestellung**. Er hilft, Verhalten, Randfälle, Abläufe und Datenformate zu verstehen, wo die Dokumentation nicht ausreicht. Als Vorlage für Struktur oder Implementierung dient er nicht.
3. Dabei gelten diese Regeln:
   - **Spezifikation zuerst:** Pro Fachgebiet entsteht in `docs/spec/` eine Beschreibung des Verhaltens in eigenen Worten. Sie nennt ihre Quellen: Doku-Link, beobachtetes Verhalten oder Referenzstelle im Original (Pfad und Version, kein Code-Zitat).
   - **Zwei Schritte:** Zuerst wird gelesen und spezifiziert. Implementiert wird danach aus der Spezifikation, ohne den Originalcode daneben geöffnet zu haben.
   - **Nicht übernommen werden:**
     - Code, auch nicht übersetzt
     - Klassen- und Dateistruktur, Kommentare
     - UI-Texte und Übersetzungen, Texte der Dokumentation
     - Overlay-HTML/CSS/JS, Bilder und Icons
     - die Community-Wortliste
     - Namen und Branding
   - **Interop-Ausnahmen:** Dateiformate für den Import, die Namen der `$`-Identifier und die fachliche Bedeutung von Event- und Rollentypen, soweit die Kompatibilität sie erfordert. Sie gelten nur vorbehaltlich einer rechtlichen Prüfung und eines eigenen ADRs zum Import von Mix-It-Up-Daten. Dieses ADR ist noch nicht geschrieben (siehe ADR-Backlog in [`plan.md`](../plan.md), §12.1).
   - **KI-gestützte Arbeit:** Eingabe ist die Spezifikation, nicht der C#-Code. Aufträge wie „übersetze diese Datei nach Go“ sind ausgeschlossen.
   - Blazing-Cacti-Dienste (`*.mixitup.bot`) werden nicht genutzt, es wird nicht dekompiliert, und die Marken werden nicht verwendet.
4. **Das Projekt bleibt privat, bis der Projektinhaber es selbst öffentlich schaltet.** Voraussetzung dafür ist **Gate O** (Open-Sourcing): eine rechtliche Prüfung, ein Herkunfts-Review und eine Markenprüfung. Ein bestandenes Gate O löst die Veröffentlichung nicht aus.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Strenger Clean Room (Implementierung ohne jeden Blick in den Code) | rechtlich am saubersten, aber langsamer und bei undokumentierten Randfällen blind |
| Codenaher Port (Übersetzung nach Go) | nicht veröffentlichbar (§3.2), Konflikt mit §3.3, widerspricht dem Ziel einer eigenständigen Architektur |
| Letzter MIT-Stand als Basis | öffentlich nicht mehr verfügbar und fachlich veraltet (vor Kick, Velora, VPZone und dem EventSub-Umbau) |
| Schriftliche Erlaubnis von Blazing Cacti | bleibt als ergänzende Option offen (Folgearbeit), ersetzt diese Entscheidung aber nicht |

## Konsequenzen

**Positiv:**

- Verhalten und Randfälle lassen sich schnell und genau verstehen.
- Die Architektur ist frei gestaltbar, die Go-Implementierung eigenständig.
- Die Spezifikationen in `docs/spec/` dokumentieren das Produkt gleich mit.

**Negativ und Risiken:**

- Ein Restrisiko bleibt: Die Nutzung des Codes als Hilfestellung könnte als „Nutzung zum Bau eines konkurrierenden Produkts“ (§3.3) gelten, die Implementierung als abgeleitetes Werk. Die Regeln oben begrenzen das Risiko, schließen es aber nicht aus. Diese Einschätzung ist keine Rechtsberatung.
- Die Apache-2.0-Lizenz ([ADR-0002](0002-lizenz-des-projekts.md)) kann nur eigenständigen Code lizenzieren. Übernommene Teile könnten nicht unter ihr stehen, deshalb ist die Herkunftsdisziplin wichtig.
- Spezifikationen und Quellennachweise kosten zusätzliche Zeit.

**Folgearbeiten:**

- [x] [`docs/spec/README.md`](../spec/README.md) mit diesen Regeln und die Vorlage [`docs/spec/TEMPLATE.md`](../spec/TEMPLATE.md) angelegt (2026-09-28)
- [ ] Rechtliche Einschätzung einholen, spätestens vor Gate O
- [ ] Optional: schriftliche Erlaubnis bei Blazing Cacti anfragen
- [ ] Herkunfts-Review vor der Veröffentlichung (Gate O)

---

*Präzisierung (2026-09-28): Zuvor hieß es „privat bis Gate O“. Öffentlich wird das Projekt aber nur, wenn der Projektinhaber es selbst umschaltet. Gate O ist die Voraussetzung dafür, nicht der Auslöser.*
